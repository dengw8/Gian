package store

import (
	"context"
	"time"

	"github.com/dengw8/harness-practice/internal/runtime"
)

type RunStatus string

const (
	RunStatusCreated   RunStatus = "created"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
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
	AppendMessages(ctx context.Context, runID string, messages []runtime.Message) error
	ListMessages(ctx context.Context, runID string) ([]runtime.Message, error)
	AppendEvents(ctx context.Context, runID string, events []runtime.Event) error
	ListEvents(ctx context.Context, runID string) ([]runtime.Event, error)
	AppendModelLogs(ctx context.Context, runID string, logs []runtime.ModelLog) error
	ListModelLogs(ctx context.Context, runID string) ([]runtime.ModelLog, error)
}
