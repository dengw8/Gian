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
	ID            string    `json:"id"`
	Status        RunStatus `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ModelProvider string    `json:"model_provider"`
	Error         string    `json:"error,omitempty"`
}

type CreateRunInput struct {
	ModelProvider string
}

type UpdateRunInput struct {
	Status RunStatus
	Error  string
}

type RunStore interface {
	CreateRun(ctx context.Context, input CreateRunInput) (RunMetadata, error)
	GetRun(ctx context.Context, runID string) (RunMetadata, error)
	UpdateRun(ctx context.Context, runID string, input UpdateRunInput) (RunMetadata, error)
	AppendMessages(ctx context.Context, runID string, messages []runtime.Message) error
	ListMessages(ctx context.Context, runID string) ([]runtime.Message, error)
	AppendEvents(ctx context.Context, runID string, events []runtime.Event) error
	ListEvents(ctx context.Context, runID string) ([]runtime.Event, error)
}
