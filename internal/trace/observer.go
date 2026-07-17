// Package trace contains the optional, best-effort execution Trace plugin.
package trace

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
)

const defaultBufferSize = 256

type Observer struct {
	store   store.TraceStore
	spans   chan runtime.TraceSpan
	done    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
	pending atomic.Int64
	mu      sync.RWMutex
	lastErr string
}

func NewObserver(traceStore store.TraceStore) *Observer {
	if traceStore == nil {
		return nil
	}
	o := &Observer{
		store: traceStore,
		spans: make(chan runtime.TraceSpan, defaultBufferSize),
		done:  make(chan struct{}),
	}
	go o.consume()
	return o
}

func (o *Observer) Observe(_ context.Context, span runtime.TraceSpan) {
	if o == nil {
		return
	}
	span = redactSpan(span)
	select {
	case o.spans <- span:
		o.pending.Add(1)
	default:
		o.dropped.Add(1)
		log.Printf("trace observer dropped span update run_id=%s span_id=%s", span.RunID, span.ID)
	}
}

func (o *Observer) Diagnostics() runtime.TraceDiagnostics {
	if o == nil {
		return runtime.TraceDiagnostics{Complete: true}
	}
	o.mu.RLock()
	lastErr := o.lastErr
	o.mu.RUnlock()
	dropped := o.dropped.Load()
	return runtime.TraceDiagnostics{
		Complete:       dropped == 0 && lastErr == "",
		DroppedUpdates: dropped,
		LastError:      lastErr,
		Pending:        int(o.pending.Load()),
	}
}

func (o *Observer) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		close(o.spans)
		<-o.done
	})
}

func (o *Observer) Dropped() uint64 {
	if o == nil {
		return 0
	}
	return o.dropped.Load()
}

func (o *Observer) consume() {
	defer close(o.done)
	for span := range o.spans {
		if err := o.store.UpsertTraceSpan(context.Background(), span); err != nil {
			o.mu.Lock()
			o.lastErr = err.Error()
			o.mu.Unlock()
			log.Printf("trace observer write failed run_id=%s span_id=%s: %v", span.RunID, span.ID, err)
		}
		o.pending.Add(-1)
	}
}

func redactSpan(span runtime.TraceSpan) runtime.TraceSpan {
	span.Input = redactJSON(span.Input)
	span.Output = redactJSON(span.Output)
	span.Attributes = redactMap(span.Attributes)
	return span
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

func redactMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	redacted, ok := redactValue(value).(map[string]any)
	if !ok {
		return nil
	}
	return redacted
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
