package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	Message     string
	Attachments []runtime.Attachment
	History     []runtime.Message
	Plan        runtime.PlanState
	Compaction  runtime.CompactionState
	Events      EventSink
}

type TurnOutput struct {
	Messages    []runtime.Message       `json:"messages"`
	NewMessages []runtime.Message       `json:"new_messages"`
	Plan        runtime.PlanState       `json:"plan"`
	Compaction  runtime.CompactionState `json:"compaction"`
	Events      []runtime.Event         `json:"events,omitempty"`
	ModelLogs   []runtime.ModelLog      `json:"model_logs,omitempty"`
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
	if input.Message == "" && len(input.Attachments) == 0 {
		return TurnOutput{}, errors.New("message is required")
	}

	history := append([]runtime.Message(nil), input.History...)
	state := &State{
		Messages: append(history, timestampMessage(runtime.Message{
			Role:        runtime.RoleUser,
			Content:     input.Message,
			Attachments: append([]runtime.Attachment(nil), input.Attachments...),
		})),
		Metadata:   map[string]any{},
		Plan:       normalizePlan(input.Plan),
		Compaction: input.Compaction,
	}
	newMessageStart := len(history)
	events := []runtime.Event{}
	modelLogs := []runtime.ModelLog{}
	eventSink := func(event runtime.Event) error {
		events = append(events, event)
		return emit(input.Events, event)
	}

	if err := r.beforeAgent(ctx, state); err != nil {
		return TurnOutput{}, err
	}
	defer func() {
		_ = r.afterAgent(context.WithoutCancel(ctx), state)
	}()

	for i := 0; i < r.maxIterations; i++ {
		toolDefinitions := r.toolDefinitions()
		if state.Plan.Mode == runtime.PlanModePlanning {
			toolDefinitions = planningTools(toolDefinitions)
		}
		state.SystemPrompt = r.systemPrompt(toolDefinitions, state.Plan)
		state.ActiveTools = toolDefinitions
		req := model.Request{
			SystemPrompt: state.SystemPrompt,
			Messages:     append([]runtime.Message(nil), state.Messages...),
			Tools:        toolDefinitions,
		}
		if err := r.beforeModel(ctx, &req, state); err != nil {
			return TurnOutput{}, err
		}

		if err := eventSink(runtime.NewEvent("model_started", "Model started")); err != nil {
			return TurnOutput{}, err
		}
		modelLog := newModelLog(len(modelLogs)+1, req)
		resp, err := r.model.Generate(ctx, req)
		if err != nil {
			modelLog.CompletedAt = time.Now().UTC()
			modelLog.Error = err.Error()
			modelLogs = append(modelLogs, modelLog)
			return TurnOutput{
				Messages:    append([]runtime.Message(nil), state.Messages...),
				NewMessages: append([]runtime.Message(nil), state.Messages[newMessageStart:]...),
				Plan:        state.Plan,
				Compaction:  state.Compaction,
				Events:      append([]runtime.Event(nil), events...),
				ModelLogs:   append([]runtime.ModelLog(nil), modelLogs...),
			}, err
		}
		modelLog.CompletedAt = time.Now().UTC()
		modelLog.Response = runtime.ModelResponseLog{
			Message: resp.Message,
			Usage:   resp.Usage,
		}
		modelLogs = append(modelLogs, modelLog)
		if err := eventSink(modelCompletedEvent(resp.Usage)); err != nil {
			return TurnOutput{}, err
		}
		if err := r.afterModel(ctx, &resp, state); err != nil {
			return TurnOutput{}, err
		}

		resp.Message.Usage = resp.Usage
		state.Messages = append(state.Messages, timestampMessage(resp.Message))
		if err := r.updatePlanFromAssistant(eventSink, state, resp.Message); err != nil {
			return TurnOutput{}, err
		}
		if len(resp.Message.ToolCalls) == 0 {
			return TurnOutput{
				Messages:    append([]runtime.Message(nil), state.Messages...),
				NewMessages: append([]runtime.Message(nil), state.Messages[newMessageStart:]...),
				Plan:        state.Plan,
				Compaction:  state.Compaction,
				Events:      append([]runtime.Event(nil), events...),
				ModelLogs:   append([]runtime.ModelLog(nil), modelLogs...),
			}, nil
		}
		for _, call := range resp.Message.ToolCalls {
			if err := eventSink(runtime.NewEvent("tool_started", call.Name)); err != nil {
				return TurnOutput{}, err
			}
			result := r.executeTool(ctx, call, state)
			if err := eventSink(runtime.NewEvent("tool_completed", result.Name)); err != nil {
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
				CreatedAt:  time.Now().UTC(),
			})
		}
	}

	return TurnOutput{
		Messages:    append([]runtime.Message(nil), state.Messages...),
		NewMessages: append([]runtime.Message(nil), state.Messages[newMessageStart:]...),
		Plan:        state.Plan,
		Compaction:  state.Compaction,
		Events:      append([]runtime.Event(nil), events...),
		ModelLogs:   append([]runtime.ModelLog(nil), modelLogs...),
	}, errors.New("agent turn exceeded max iterations")
}

func newModelLog(index int, req model.Request) runtime.ModelLog {
	return runtime.ModelLog{
		Index:     index,
		StartedAt: time.Now().UTC(),
		Request: runtime.ModelRequestLog{
			SystemPrompt: req.SystemPrompt,
			Messages:     append([]runtime.Message(nil), req.Messages...),
			Tools:        append([]runtime.ToolDefinition(nil), req.Tools...),
		},
	}
}

func timestampMessage(message runtime.Message) runtime.Message {
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	return message
}

func normalizePlan(plan runtime.PlanState) runtime.PlanState {
	if plan.Mode == "" {
		plan.Mode = runtime.PlanModeOff
	}
	return plan
}

func (r *Runner) updatePlanFromAssistant(sink EventSink, state *State, message runtime.Message) error {
	if message.Role != runtime.RoleAssistant {
		return nil
	}
	switch state.Plan.Mode {
	case runtime.PlanModePlanning:
		steps := extractPlanSteps(message.Content)
		if len(steps) == 0 {
			return emit(sink, runtime.NewEvent("plan_not_found", "No numbered Plan section found"))
		}
		state.Plan.Steps = steps
		return emit(sink, runtime.NewEvent("plan_created", fmt.Sprintf("Plan created with %d step(s)", len(steps))))
	case runtime.PlanModeExecuting:
		updated, completed := markCompletedSteps(state.Plan, message.Content)
		state.Plan = updated
		for _, step := range completed {
			if err := emit(sink, runtime.NewEvent("plan_step_completed", fmt.Sprintf("Completed plan step %d: %s", step.Index, step.Text))); err != nil {
				return err
			}
		}
		if len(state.Plan.Steps) > 0 && state.Plan.CompletedCount() == len(state.Plan.Steps) {
			state.Plan.Mode = runtime.PlanModeOff
			return emit(sink, runtime.NewEvent("plan_completed", "Plan completed"))
		}
	}
	return nil
}

func emit(sink EventSink, event runtime.Event) error {
	if sink == nil {
		return nil
	}
	return sink(event)
}

func modelCompletedEvent(usage *runtime.TokenUsage) runtime.Event {
	event := runtime.NewEvent("model_completed", "Model completed")
	event.Usage = usage
	return event
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

func (r *Runner) systemPrompt(definitions []runtime.ToolDefinition, plan runtime.PlanState) string {
	builder := r.promptBuilder
	if len(builder.StaticSections) == 0 {
		builder = NewSystemPromptBuilder()
	}
	return builder.WithTools(definitions).WithPlan(plan).Build()
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
