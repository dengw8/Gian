package runtime

import (
	"context"
	"encoding/json"
	"time"
)

// ExecutionEventSchemaVersion identifies the persisted execution-event wire
// format. New fields are additive within a schema version.
const ExecutionEventSchemaVersion = 1

type ExecutionEventType string

const (
	ExecutionEventRunStarted         ExecutionEventType = "run.started"
	ExecutionEventRunCompleted       ExecutionEventType = "run.completed"
	ExecutionEventRunFailed          ExecutionEventType = "run.failed"
	ExecutionEventTurnStarted        ExecutionEventType = "turn.started"
	ExecutionEventTurnCompleted      ExecutionEventType = "turn.completed"
	ExecutionEventTurnFailed         ExecutionEventType = "turn.failed"
	ExecutionEventIterationStarted   ExecutionEventType = "iteration.started"
	ExecutionEventIterationCompleted ExecutionEventType = "iteration.completed"
	ExecutionEventModelStarted       ExecutionEventType = "model.started"
	ExecutionEventModelText          ExecutionEventType = "model.text"
	ExecutionEventModelToolCall      ExecutionEventType = "model.tool_call"
	ExecutionEventModelCompleted     ExecutionEventType = "model.completed"
	ExecutionEventModelFailed        ExecutionEventType = "model.failed"
	ExecutionEventToolStarted        ExecutionEventType = "tool.started"
	ExecutionEventToolCompleted      ExecutionEventType = "tool.completed"
	ExecutionEventToolFailed         ExecutionEventType = "tool.failed"
	ExecutionEventApprovalRequested  ExecutionEventType = "approval.requested"
	ExecutionEventApprovalResolved   ExecutionEventType = "approval.resolved"
	ExecutionEventError              ExecutionEventType = "error"
)

// ExecutionEvent is the append-only, ordered source of truth for one Run's
// execution lifecycle. Span fields allow consumers to build containment trees
// without inferring relationships from display messages.
type ExecutionEvent struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	RunID         string             `json:"run_id"`
	TurnID        string             `json:"turn_id,omitempty"`
	Iteration     int                `json:"iteration,omitempty"`
	Sequence      int64              `json:"sequence"`
	OccurredAt    time.Time          `json:"occurred_at"`
	Type          ExecutionEventType `json:"type"`
	Status        TraceSpanStatus    `json:"status,omitempty"`
	SpanID        string             `json:"span_id,omitempty"`
	ParentSpanID  string             `json:"parent_span_id,omitempty"`
	ModelCallID   string             `json:"model_call_id,omitempty"`
	ToolCallID    string             `json:"tool_call_id,omitempty"`
	Payload       json.RawMessage    `json:"payload,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// ExecutionEventDiagnostics reports the intentionally best-effort condition
// of event capture and Trace projection. It is separate from Run state.
type ExecutionEventDiagnostics struct {
	Complete          bool   `json:"complete"`
	DroppedEvents     uint64 `json:"dropped_events,omitempty"`
	TruncatedEvents   uint64 `json:"truncated_events,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	Pending           int    `json:"pending,omitempty"`
	PersistedSequence int64  `json:"persisted_sequence,omitempty"`
	ProjectedSequence int64  `json:"projected_sequence,omitempty"`
}

// ExecutionEventPublisher must be best-effort. It never returns an error so
// observation cannot become part of the Agent's failure path.
type ExecutionEventPublisher interface {
	PublishExecutionEvent(ctx context.Context, event ExecutionEvent)
}

// ExecutionEventSubscriber is implemented by in-process publishers that can
// notify a live SSE connection after an event has been safely persisted.
type ExecutionEventSubscriber interface {
	SubscribeExecutionEvents(runID string) (<-chan ExecutionEvent, func())
}

type NoopExecutionEventPublisher struct{}

func (NoopExecutionEventPublisher) PublishExecutionEvent(context.Context, ExecutionEvent) {}
