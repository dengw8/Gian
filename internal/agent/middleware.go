package agent

import (
	"context"

	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/runtime"
)

type State struct {
	SystemPrompt string            `json:"system_prompt"`
	Messages     []runtime.Message `json:"messages"`
	Metadata     map[string]any    `json:"metadata"`
	Plan         runtime.PlanState `json:"plan"`
	Compaction   runtime.CompactionState
	ActiveTools  []runtime.ToolDefinition
}

type Middleware interface {
	Name() string
	BeforeAgent(ctx context.Context, state *State) error
	BeforeModel(ctx context.Context, req *model.Request, state *State) error
	AfterModel(ctx context.Context, resp *model.Response, state *State) error
	BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error
	AfterTool(ctx context.Context, result runtime.ToolResult, state *State) error
	AfterAgent(ctx context.Context, state *State) error
}

type NoopMiddleware struct {
	NameValue string
}

func (m NoopMiddleware) Name() string {
	if m.NameValue == "" {
		return "noop"
	}
	return m.NameValue
}

func (m NoopMiddleware) BeforeAgent(ctx context.Context, state *State) error {
	return nil
}

func (m NoopMiddleware) BeforeModel(ctx context.Context, req *model.Request, state *State) error {
	return nil
}

func (m NoopMiddleware) AfterModel(ctx context.Context, resp *model.Response, state *State) error {
	return nil
}

func (m NoopMiddleware) BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	return nil
}

func (m NoopMiddleware) AfterTool(ctx context.Context, result runtime.ToolResult, state *State) error {
	return nil
}

func (m NoopMiddleware) AfterAgent(ctx context.Context, state *State) error {
	return nil
}
