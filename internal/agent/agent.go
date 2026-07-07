package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/tools"
)

type Config struct {
	Model         model.Provider
	MaxIterations int
	Middleware    []Middleware
	Tools         *tools.Registry
	PromptBuilder SystemPromptBuilder
}

type Runner struct {
	model         model.Provider
	maxIterations int
	middleware    []Middleware
	tools         *tools.Registry
	promptBuilder SystemPromptBuilder
}

type TurnInput struct {
	Message string
	History []runtime.Message
	Events  EventSink
}

type TurnOutput struct {
	Messages    []runtime.Message `json:"messages"`
	NewMessages []runtime.Message `json:"new_messages"`
}

type EventSink func(event runtime.Event) error

func NewRunner(cfg Config) *Runner {
	maxIterations := cfg.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 4
	}
	return &Runner{
		model:         cfg.Model,
		maxIterations: maxIterations,
		middleware:    append([]Middleware(nil), cfg.Middleware...),
		tools:         cfg.Tools,
		promptBuilder: cfg.PromptBuilder,
	}
}

func (r *Runner) RunTurn(ctx context.Context, input TurnInput) (TurnOutput, error) {
	if r.model == nil {
		return TurnOutput{}, errors.New("agent runner requires a model provider")
	}
	if input.Message == "" {
		return TurnOutput{}, errors.New("message is required")
	}

	history := append([]runtime.Message(nil), input.History...)
	state := &State{
		Messages: append(history, runtime.Message{Role: runtime.RoleUser, Content: input.Message}),
		Metadata: map[string]any{},
	}
	newMessageStart := len(history)

	if err := r.beforeAgent(ctx, state); err != nil {
		return TurnOutput{}, err
	}
	defer func() {
		_ = r.afterAgent(context.WithoutCancel(ctx), state)
	}()

	for i := 0; i < r.maxIterations; i++ {
		toolDefinitions := r.toolDefinitions()
		state.SystemPrompt = r.systemPrompt(toolDefinitions)
		req := model.Request{
			SystemPrompt: state.SystemPrompt,
			Messages:     append([]runtime.Message(nil), state.Messages...),
			Tools:        toolDefinitions,
		}
		if err := r.beforeModel(ctx, &req, state); err != nil {
			return TurnOutput{}, err
		}

		if err := emit(input.Events, runtime.NewEvent("model_started", "Model started")); err != nil {
			return TurnOutput{}, err
		}
		resp, err := r.model.Generate(ctx, req)
		if err != nil {
			return TurnOutput{}, err
		}
		if err := emit(input.Events, runtime.NewEvent("model_completed", "Model completed")); err != nil {
			return TurnOutput{}, err
		}
		if err := r.afterModel(ctx, &resp, state); err != nil {
			return TurnOutput{}, err
		}

		state.Messages = append(state.Messages, resp.Message)
		if len(resp.Message.ToolCalls) == 0 {
			return TurnOutput{
				Messages:    append([]runtime.Message(nil), state.Messages...),
				NewMessages: append([]runtime.Message(nil), state.Messages[newMessageStart:]...),
			}, nil
		}
		for _, call := range resp.Message.ToolCalls {
			if err := emit(input.Events, runtime.NewEvent("tool_started", call.Name)); err != nil {
				return TurnOutput{}, err
			}
			result := r.executeTool(ctx, call, state)
			if err := emit(input.Events, runtime.NewEvent("tool_completed", result.Name)); err != nil {
				return TurnOutput{}, err
			}
			content := result.Content
			if result.Error != "" {
				content = "error: " + result.Error
			}
			state.Messages = append(state.Messages, runtime.Message{
				Role:       runtime.RoleTool,
				ToolCallID: result.ToolCallID,
				Name:       result.Name,
				Content:    content,
			})
		}
	}

	return TurnOutput{}, errors.New("agent turn exceeded max iterations")
}

func emit(sink EventSink, event runtime.Event) error {
	if sink == nil {
		return nil
	}
	return sink(event)
}

func (r *Runner) executeTool(ctx context.Context, call runtime.ToolCall, state *State) runtime.ToolResult {
	result := runtime.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
	}
	if err := r.beforeTool(ctx, call, state); err != nil {
		result.Error = err.Error()
		_ = r.afterTool(ctx, result, state)
		return result
	}
	tool, ok := r.tools.Get(call.Name)
	if !ok {
		result.Error = fmt.Sprintf("tool %q is not registered", call.Name)
		_ = r.afterTool(ctx, result, state)
		return result
	}
	toolResult, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Content = toolResult.Content
		if toolResult.Name != "" {
			result.Name = toolResult.Name
		}
	}
	_ = r.afterTool(ctx, result, state)
	return result
}

func (r *Runner) toolDefinitions() []runtime.ToolDefinition {
	return r.tools.Definitions()
}

func (r *Runner) systemPrompt(definitions []runtime.ToolDefinition) string {
	builder := r.promptBuilder
	if len(builder.StaticSections) == 0 {
		builder = NewSystemPromptBuilder()
	}
	return builder.WithTools(definitions).Build()
}

func (r *Runner) beforeAgent(ctx context.Context, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.BeforeAgent(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) beforeModel(ctx context.Context, req *model.Request, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.BeforeModel(ctx, req, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) afterModel(ctx context.Context, resp *model.Response, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.AfterModel(ctx, resp, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) beforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.BeforeTool(ctx, call, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) afterTool(ctx context.Context, result runtime.ToolResult, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.AfterTool(ctx, result, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) afterAgent(ctx context.Context, state *State) error {
	for _, mw := range r.middleware {
		if err := mw.AfterAgent(ctx, state); err != nil {
			return err
		}
	}
	return nil
}
