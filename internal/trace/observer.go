// Package trace contains the optional, best-effort execution Trace plugin.
package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
)

const defaultBufferSize = 256

const (
	maxModelTextPayloadBytes = 16 * 1024
	maxModelTextRunBytes     = 256 * 1024
)

// Observer is an asynchronous sidecar. It persists canonical execution events
// first and only then projects the event to a Trace span. Its public methods
// deliberately return no error so observability cannot affect Run execution.
type Observer struct {
	traceStore  store.TraceStore
	eventStore  store.ExecutionEventStore
	events      chan runtime.ExecutionEvent
	done        chan struct{}
	once        sync.Once
	dropped     atomic.Uint64
	truncated   atomic.Uint64
	pending     atomic.Int64
	nextID      atomic.Uint64
	mu          sync.RWMutex
	lastErr     string
	sequences   map[string]int64
	persisted   map[string]int64
	projected   map[string]int64
	modelText   map[string]int
	subscribers map[string]map[chan runtime.ExecutionEvent]struct{}
}

func NewObserver(traceStore store.TraceStore, eventStores ...store.ExecutionEventStore) *Observer {
	if traceStore == nil {
		return nil
	}
	var eventStore store.ExecutionEventStore
	if len(eventStores) > 0 {
		eventStore = eventStores[0]
	}
	o := &Observer{
		traceStore:  traceStore,
		eventStore:  eventStore,
		events:      make(chan runtime.ExecutionEvent, defaultBufferSize),
		done:        make(chan struct{}),
		sequences:   map[string]int64{},
		persisted:   map[string]int64{},
		projected:   map[string]int64{},
		modelText:   map[string]int{},
		subscribers: map[string]map[chan runtime.ExecutionEvent]struct{}{},
	}
	go o.consume()
	return o
}

// PublishExecutionEvent implements runtime.ExecutionEventPublisher.
func (o *Observer) PublishExecutionEvent(_ context.Context, event runtime.ExecutionEvent) {
	if o == nil || event.RunID == "" {
		return
	}
	event = redactEvent(event)
	event, truncated := o.limitModelText(event)
	if truncated {
		o.truncated.Add(1)
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = runtime.ExecutionEventSchemaVersion
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("execution_%s_%d", event.RunID, o.nextID.Add(1))
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	select {
	case o.events <- event:
		o.pending.Add(1)
	default:
		o.dropped.Add(1)
		log.Printf("trace observer dropped execution event run_id=%s type=%s", event.RunID, event.Type)
	}
}

// Observe remains as a compatibility adapter for the existing internal Trace
// call sites while they emit lifecycle spans. It does not write a span: the
// span is carried by a canonical event and the consumer projects it later.
func (o *Observer) Observe(ctx context.Context, span runtime.TraceSpan) {
	if o == nil || span.RunID == "" {
		return
	}
	payload, err := json.Marshal(map[string]runtime.TraceSpan{"span": span})
	if err != nil {
		return
	}
	iteration := 0
	if value, ok := span.Attributes["iteration"].(int); ok {
		iteration = value
	}
	o.PublishExecutionEvent(ctx, runtime.ExecutionEvent{
		RunID:        span.RunID,
		TurnID:       span.TurnID,
		Iteration:    iteration,
		OccurredAt:   executionSpanTime(span),
		Type:         executionEventTypeForSpan(span),
		Status:       span.Status,
		SpanID:       span.ID,
		ParentSpanID: span.ParentID,
		ModelCallID:  span.ModelCallID,
		ToolCallID:   span.ToolCallID,
		Payload:      payload,
		Error:        span.Error,
	})
	if span.Error != "" {
		o.PublishExecutionEvent(ctx, runtime.ExecutionEvent{
			RunID:        span.RunID,
			TurnID:       span.TurnID,
			Iteration:    iteration,
			OccurredAt:   executionSpanTime(span),
			Type:         runtime.ExecutionEventError,
			Status:       runtime.TraceStatusFailed,
			SpanID:       span.ID,
			ParentSpanID: span.ParentID,
			ModelCallID:  span.ModelCallID,
			ToolCallID:   span.ToolCallID,
			Error:        span.Error,
		})
	}
}

func (o *Observer) Diagnostics() runtime.TraceDiagnostics {
	return o.diagnosticsForRun("")
}

func (o *Observer) DiagnosticsForRun(runID string) runtime.TraceDiagnostics {
	return o.diagnosticsForRun(runID)
}

func (o *Observer) diagnosticsForRun(runID string) runtime.TraceDiagnostics {
	if o == nil {
		return runtime.TraceDiagnostics{Complete: true}
	}
	o.mu.RLock()
	lastErr := o.lastErr
	persisted, projected := int64(0), int64(0)
	if runID != "" {
		persisted = o.persisted[runID]
		projected = o.projected[runID]
	} else {
		for _, sequence := range o.persisted {
			if sequence > persisted {
				persisted = sequence
			}
		}
		for _, sequence := range o.projected {
			if sequence > projected {
				projected = sequence
			}
		}
	}
	// Projection is synchronous with a successful event write. If an error
	// occurs it is represented by Complete=false/LastError rather than a false
	// success value.
	o.mu.RUnlock()
	dropped := o.dropped.Load()
	truncated := o.truncated.Load()
	return runtime.TraceDiagnostics{
		Complete:          dropped == 0 && truncated == 0 && lastErr == "" && persisted == projected,
		DroppedUpdates:    dropped,
		TruncatedEvents:   truncated,
		LastError:         lastErr,
		Pending:           int(o.pending.Load()),
		PersistedSequence: persisted,
		ProjectedSequence: projected,
	}
}

func (o *Observer) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		close(o.events)
		<-o.done
	})
}

func (o *Observer) Dropped() uint64 {
	if o == nil {
		return 0
	}
	return o.dropped.Load()
}

func (o *Observer) SubscribeExecutionEvents(runID string) (<-chan runtime.ExecutionEvent, func()) {
	if o == nil || runID == "" {
		closed := make(chan runtime.ExecutionEvent)
		close(closed)
		return closed, func() {}
	}
	ch := make(chan runtime.ExecutionEvent, 64)
	o.mu.Lock()
	if o.subscribers[runID] == nil {
		o.subscribers[runID] = map[chan runtime.ExecutionEvent]struct{}{}
	}
	o.subscribers[runID][ch] = struct{}{}
	o.mu.Unlock()
	return ch, func() {
		o.mu.Lock()
		if subscribers := o.subscribers[runID]; subscribers != nil {
			delete(subscribers, ch)
			if len(subscribers) == 0 {
				delete(o.subscribers, runID)
			}
		}
		o.mu.Unlock()
	}
}

func (o *Observer) consume() {
	defer close(o.done)
	for event := range o.events {
		o.persistAndProject(event)
		o.pending.Add(-1)
	}
}

func (o *Observer) persistAndProject(event runtime.ExecutionEvent) {
	if event.Sequence == 0 {
		event.Sequence = o.nextSequence(event.RunID)
	}
	if o.eventStore != nil {
		if err := o.eventStore.AppendExecutionEvent(context.Background(), event); err != nil {
			o.recordError(err)
			log.Printf("trace observer event write failed run_id=%s sequence=%d: %v", event.RunID, event.Sequence, err)
			return
		}
	}
	o.recordPersisted(event.RunID, event.Sequence)
	if span, ok := spanFromEvent(event); ok {
		span.Sequence = int(event.Sequence)
		span.Revision = int(event.Sequence)
		if err := o.traceStore.UpsertTraceSpan(context.Background(), span); err != nil {
			o.recordError(err)
			log.Printf("trace observer projection failed run_id=%s sequence=%d: %v", event.RunID, event.Sequence, err)
			return
		}
	}
	o.recordProjected(event.RunID, event.Sequence)
	o.publishToSubscribers(event)
}

func (o *Observer) recordPersisted(runID string, sequence int64) {
	o.mu.Lock()
	if sequence > o.persisted[runID] {
		o.persisted[runID] = sequence
	}
	o.mu.Unlock()
}

func (o *Observer) recordProjected(runID string, sequence int64) {
	o.mu.Lock()
	if sequence > o.projected[runID] {
		o.projected[runID] = sequence
	}
	o.mu.Unlock()
}

func (o *Observer) publishToSubscribers(event runtime.ExecutionEvent) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for subscriber := range o.subscribers[event.RunID] {
		select {
		case subscriber <- event:
		default:
			// The durable event API is the recovery mechanism for a slow SSE
			// consumer; it must not slow down the projection worker.
		}
	}
}

func (o *Observer) nextSequence(runID string) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.sequences[runID]; !ok && o.eventStore != nil {
		sequence, err := o.eventStore.LastExecutionEventSequence(context.Background(), runID)
		if err != nil {
			o.lastErr = err.Error()
			log.Printf("trace observer sequence lookup failed run_id=%s: %v", runID, err)
		}
		o.sequences[runID] = sequence
		o.persisted[runID] = sequence
		o.projected[runID] = sequence
	}
	o.sequences[runID]++
	return o.sequences[runID]
}

func (o *Observer) recordError(err error) {
	o.mu.Lock()
	o.lastErr = err.Error()
	o.mu.Unlock()
}

func spanFromEvent(event runtime.ExecutionEvent) (runtime.TraceSpan, bool) {
	var payload struct {
		Span runtime.TraceSpan `json:"span"`
	}
	if len(event.Payload) == 0 || json.Unmarshal(event.Payload, &payload) != nil || payload.Span.ID == "" {
		return runtime.TraceSpan{}, false
	}
	return payload.Span, true
}

func executionSpanTime(span runtime.TraceSpan) time.Time {
	if !span.CompletedAt.IsZero() {
		return span.CompletedAt
	}
	return span.StartedAt
}

func executionEventTypeForSpan(span runtime.TraceSpan) runtime.ExecutionEventType {
	completed := !span.CompletedAt.IsZero()
	failed := span.Status == runtime.TraceStatusFailed
	switch span.Kind {
	case runtime.TraceSpanRun:
		if failed {
			return runtime.ExecutionEventRunFailed
		}
		if completed {
			return runtime.ExecutionEventRunCompleted
		}
		return runtime.ExecutionEventRunStarted
	case runtime.TraceSpanTurn, runtime.TraceSpanGraph:
		if failed {
			return runtime.ExecutionEventTurnFailed
		}
		if completed {
			return runtime.ExecutionEventTurnCompleted
		}
		return runtime.ExecutionEventTurnStarted
	case runtime.TraceSpanModel:
		if failed {
			return runtime.ExecutionEventModelFailed
		}
		if completed {
			return runtime.ExecutionEventModelCompleted
		}
		return runtime.ExecutionEventModelStarted
	case runtime.TraceSpanTool:
		if failed {
			return runtime.ExecutionEventToolFailed
		}
		if completed {
			return runtime.ExecutionEventToolCompleted
		}
		return runtime.ExecutionEventToolStarted
	case runtime.TraceSpanApproval:
		if completed {
			return runtime.ExecutionEventApprovalResolved
		}
		return runtime.ExecutionEventApprovalRequested
	default:
		return runtime.ExecutionEventError
	}
}

func redactEvent(event runtime.ExecutionEvent) runtime.ExecutionEvent {
	event.Payload = redactJSON(event.Payload)
	return event
}

func (o *Observer) limitModelText(event runtime.ExecutionEvent) (runtime.ExecutionEvent, bool) {
	if event.Type != runtime.ExecutionEventModelText {
		return event, false
	}
	size := len(event.Payload)
	o.mu.Lock()
	total := o.modelText[event.RunID]
	if size <= maxModelTextPayloadBytes && total+size <= maxModelTextRunBytes {
		o.modelText[event.RunID] = total + size
		o.mu.Unlock()
		return event, false
	}
	o.mu.Unlock()
	event.Payload = json.RawMessage(fmt.Sprintf(`{"truncated":true,"original_size":%d}`, size))
	return event, true
}

func redactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	data, err := json.Marshal(redactValue(value))
	if err != nil {
		return nil
	}
	return data
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey(key) || strings.EqualFold(key, "data") && attachmentMap(typed) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = redactValue(typed[i])
		}
		return out
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return key == "api_key" || key == "apikey" || key == "token" || key == "access_token" || key == "authorization" || key == "password" || key == "secret"
}

func attachmentMap(value map[string]any) bool {
	_, hasType := value["type"]
	_, hasMediaType := value["media_type"]
	return hasType && hasMediaType
}
