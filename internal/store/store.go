package store

import (
	"context"
	"errors"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
)

var ErrApprovalConflict = errors.New("shell approval conflict")

type RunStatus string

const (
	RunStatusCreated          RunStatus = "created"
	RunStatusRunning          RunStatus = "running"
	RunStatusAwaitingApproval RunStatus = "awaiting_approval"
	RunStatusCompleted        RunStatus = "completed"
	RunStatusFailed           RunStatus = "failed"
)

type RunMetadata struct {
	ID            string              `json:"id"`
	Status        RunStatus           `json:"status"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	ModelProvider string              `json:"model_provider"`
	Error         string              `json:"error,omitempty"`
	PlanMode      string              `json:"plan_mode,omitempty"`
	PlanSteps     int                 `json:"plan_steps,omitempty"`
	PlanCompleted int                 `json:"plan_completed,omitempty"`
	TokenUsage    *runtime.TokenUsage `json:"token_usage,omitempty"`
}

type CreateRunInput struct {
	ModelProvider string
}

type UpdateRunInput struct {
	Status   RunStatus
	Error    string
	AddUsage *runtime.TokenUsage
}

type RunStore interface {
	CreateRun(ctx context.Context, input CreateRunInput) (RunMetadata, error)
	ListRuns(ctx context.Context) ([]RunMetadata, error)
	GetRun(ctx context.Context, runID string) (RunMetadata, error)
	UpdateRun(ctx context.Context, runID string, input UpdateRunInput) (RunMetadata, error)
	GetPlan(ctx context.Context, runID string) (runtime.PlanState, error)
	SavePlan(ctx context.Context, runID string, plan runtime.PlanState) (RunMetadata, error)
	GetCompaction(ctx context.Context, runID string) (runtime.CompactionState, error)
	SaveCompaction(ctx context.Context, runID string, state runtime.CompactionState) error
	GetPendingShellApproval(ctx context.Context, runID string) (*runtime.PendingShellApproval, error)
	SavePendingShellApproval(ctx context.Context, runID string, approval runtime.PendingShellApproval) error
	ClaimPendingShellApproval(ctx context.Context, runID, approvalID string, version int) (runtime.PendingShellApproval, error)
	ClearPendingShellApproval(ctx context.Context, runID, approvalID string) error
	CompleteShellApproval(ctx context.Context, runID, executingApprovalID string, next *runtime.PendingShellApproval) error
	AppendMessages(ctx context.Context, runID string, messages []runtime.Message) error
	ListMessages(ctx context.Context, runID string) ([]runtime.Message, error)
	AppendEvents(ctx context.Context, runID string, events []runtime.Event) error
	ListEvents(ctx context.Context, runID string) ([]runtime.Event, error)
	AppendModelLogs(ctx context.Context, runID string, logs []runtime.ModelLog) error
	ListModelLogs(ctx context.Context, runID string) ([]runtime.ModelLog, error)
}

// TraceStore is separate from RunStore so Trace remains an optional observer
// plugin instead of a dependency of the main Run execution path.
type TraceStore interface {
	UpsertTraceSpan(ctx context.Context, span runtime.TraceSpan) error
	ListTraceSpans(ctx context.Context, runID string) ([]runtime.TraceSpan, error)
}
