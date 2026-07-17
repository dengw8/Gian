package runtime

import (
	"context"
	"encoding/json"
	"time"
)

type TraceSpanKind string

const (
	TraceSpanRun      TraceSpanKind = "run"
	TraceSpanGraph    TraceSpanKind = "graph"
	TraceSpanTurn     TraceSpanKind = "turn"
	TraceSpanModel    TraceSpanKind = "model"
	TraceSpanTool     TraceSpanKind = "tool"
	TraceSpanApproval TraceSpanKind = "approval"
)

// TraceLink expresses a data-flow relationship that cannot be inferred from
// the parent/child execution containment tree alone.
type TraceLink struct {
	Type       string `json:"type"`
	SpanID     string `json:"span_id"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// TraceDiagnostics describes the best-effort observer's ability to retain a
// complete trace. It is deliberately separate from individual span state.
type TraceDiagnostics struct {
	Complete          bool   `json:"complete"`
	DroppedUpdates    uint64 `json:"dropped_updates,omitempty"`
	TruncatedEvents   uint64 `json:"truncated_events,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	Pending           int    `json:"pending,omitempty"`
	PersistedSequence int64  `json:"persisted_sequence,omitempty"`
	ProjectedSequence int64  `json:"projected_sequence,omitempty"`
}

type TraceSpanStatus string

const (
	TraceStatusRunning          TraceSpanStatus = "running"
	TraceStatusAwaitingApproval TraceSpanStatus = "awaiting_approval"
	TraceStatusSucceeded        TraceSpanStatus = "succeeded"
	TraceStatusFailed           TraceSpanStatus = "failed"
	TraceStatusDenied           TraceSpanStatus = "denied"
	TraceStatusCancelled        TraceSpanStatus = "cancelled"
)

// TraceSpan is the durable, parent-linked execution record used by the Trace UI.
// Revision is monotonically increasing per span so asynchronous persistence cannot
// replace a completed span with an earlier running version.
type TraceSpan struct {
	ID          string          `json:"id"`
	ParentID    string          `json:"parent_id,omitempty"`
	RunID       string          `json:"run_id"`
	TurnID      string          `json:"turn_id,omitempty"`
	Kind        TraceSpanKind   `json:"kind"`
	Name        string          `json:"name"`
	Status      TraceSpanStatus `json:"status"`
	Revision    int             `json:"revision"`
	Sequence    int             `json:"sequence"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt time.Time       `json:"completed_at,omitempty"`
	DurationMS  int64           `json:"duration_ms,omitempty"`
	Component   string          `json:"component,omitempty"`
	ModelCallID string          `json:"model_call_id,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Links       []TraceLink     `json:"links,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	Usage       *TokenUsage     `json:"usage,omitempty"`
	Attributes  map[string]any  `json:"attributes,omitempty"`
}

// MarshalJSON avoids serializing Go's zero time as year 0001, which is not an
// actual completion timestamp and would otherwise produce invalid UI durations.
func (s TraceSpan) MarshalJSON() ([]byte, error) {
	type wire struct {
		ID          string          `json:"id"`
		ParentID    string          `json:"parent_id,omitempty"`
		RunID       string          `json:"run_id"`
		TurnID      string          `json:"turn_id,omitempty"`
		Kind        TraceSpanKind   `json:"kind"`
		Name        string          `json:"name"`
		Status      TraceSpanStatus `json:"status"`
		Revision    int             `json:"revision"`
		Sequence    int             `json:"sequence"`
		StartedAt   time.Time       `json:"started_at"`
		CompletedAt *time.Time      `json:"completed_at,omitempty"`
		DurationMS  int64           `json:"duration_ms,omitempty"`
		Component   string          `json:"component,omitempty"`
		ModelCallID string          `json:"model_call_id,omitempty"`
		ToolCallID  string          `json:"tool_call_id,omitempty"`
		Links       []TraceLink     `json:"links,omitempty"`
		Input       json.RawMessage `json:"input,omitempty"`
		Output      json.RawMessage `json:"output,omitempty"`
		Error       string          `json:"error,omitempty"`
		Usage       *TokenUsage     `json:"usage,omitempty"`
		Attributes  map[string]any  `json:"attributes,omitempty"`
	}
	var completedAt *time.Time
	if !s.CompletedAt.IsZero() {
		value := s.CompletedAt
		completedAt = &value
	}
	return json.Marshal(wire{ID: s.ID, ParentID: s.ParentID, RunID: s.RunID, TurnID: s.TurnID, Kind: s.Kind, Name: s.Name, Status: s.Status, Revision: s.Revision, Sequence: s.Sequence, StartedAt: s.StartedAt, CompletedAt: completedAt, DurationMS: s.DurationMS, Component: s.Component, ModelCallID: s.ModelCallID, ToolCallID: s.ToolCallID, Links: s.Links, Input: s.Input, Output: s.Output, Error: s.Error, Usage: s.Usage, Attributes: s.Attributes})
}

// ExecutionObserver is intentionally best-effort: implementations must not let
// observation errors alter the Agent's execution path.
type ExecutionObserver interface {
	Observe(ctx context.Context, span TraceSpan)
}

type NoopExecutionObserver struct{}

func (NoopExecutionObserver) Observe(context.Context, TraceSpan) {}

func TraceRunSpanID(runID string) string       { return "trace_run_" + runID }
func TraceTurnSpanID(turnID string) string     { return "trace_turn_" + turnID }
func TraceModelSpanID(callID string) string    { return "trace_model_" + callID }
func TraceToolSpanID(callID string) string     { return "trace_tool_" + callID }
func TraceApprovalSpanID(callID string) string { return "trace_approval_" + callID }
