package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dengw8/Gian/internal/model"
	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/tools"
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
	Message           string
	Attachments       []runtime.Attachment
	History           []runtime.Message
	Plan              runtime.PlanState
	Compaction        runtime.CompactionState
	TurnID            string
	StreamModelOutput bool
	Events            EventSink
}

type TurnOutput struct {
	Messages        []runtime.Message             `json:"messages"`
	NewMessages     []runtime.Message             `json:"new_messages"`
	Plan            runtime.PlanState             `json:"plan"`
	Compaction      runtime.CompactionState       `json:"compaction"`
	PendingApproval *runtime.PendingShellApproval `json:"pending_approval,omitempty"`
	Events          []runtime.Event               `json:"events,omitempty"`
	ModelLogs       []runtime.ModelLog            `json:"model_logs,omitempty"`
}

type ResumeInput struct {
	History    []runtime.Message
	Plan       runtime.PlanState
	Compaction runtime.CompactionState
	Approval   runtime.PendingShellApproval
	Decision   runtime.ShellApprovalDecision
	TurnID     string
	Events     EventSink
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
	recorder := newTurnRecorder(input.Events, input.TurnID, input.StreamModelOutput)

	if err := r.beforeAgent(ctx, state); err != nil {
		return TurnOutput{}, err
	}
	defer func() {
		_ = r.afterAgent(context.WithoutCancel(ctx), state)
	}()
	return r.runModelLoop(ctx, state, newMessageStart, recorder)
}

func (r *Runner) ResumeTurn(ctx context.Context, input ResumeInput) (TurnOutput, error) {
	if r.model == nil {
		return TurnOutput{}, errors.New("agent runner requires a model provider")
	}
	if input.Approval.ToolCall.Name != "shell" {
		return TurnOutput{}, errors.New("only shell approvals can be resumed")
	}
	state := &State{
		Messages:   append([]runtime.Message(nil), input.History...),
		Metadata:   map[string]any{},
		Plan:       normalizePlan(input.Plan),
		Compaction: input.Compaction,
	}
	newMessageStart := len(state.Messages)
	recorder := newTurnRecorder(input.Events, input.TurnID, false)
	if err := r.beforeAgent(ctx, state); err != nil {
		return TurnOutput{}, err
	}
	defer func() {
		_ = r.afterAgent(context.WithoutCancel(ctx), state)
	}()

	result := runtime.ToolResult{ToolCallID: input.Approval.ToolCall.ID, Name: input.Approval.ToolCall.Name}
	switch input.Decision {
	case runtime.ShellApprovalApprove:
		if err := recorder.emit(toolEvent("approval_approved", "Shell approval approved", input.Approval.ToolCall.ID)); err != nil {
			return TurnOutput{}, err
		}
		result = r.executeApprovedTool(ctx, input.Approval.ToolCall, state)
	case runtime.ShellApprovalDeny:
		if err := recorder.emit(toolEvent("approval_denied", "Shell approval denied", input.Approval.ToolCall.ID)); err != nil {
			return TurnOutput{}, err
		}
		result.Error = "shell execution denied by user"
		_ = r.afterTool(ctx, result, state)
	case runtime.ShellApprovalCancel:
		if err := recorder.emit(toolEvent("approval_cancelled", "Shell approval cancelled", input.Approval.ToolCall.ID)); err != nil {
			return TurnOutput{}, err
		}
		result.Error = "shell execution cancelled by user"
		_ = r.afterTool(ctx, result, state)
	default:
		return TurnOutput{}, fmt.Errorf("unsupported shell approval decision %q", input.Decision)
	}
	if err := recorder.emit(toolEvent("tool_completed", result.Name, result.ToolCallID)); err != nil {
		return TurnOutput{}, err
	}
	appendToolMessage(state, result)

	pending, err := r.runToolCalls(ctx, input.Approval.RemainingToolCalls, state, recorder)
	if err != nil {
		return r.turnOutput(state, newMessageStart, recorder, nil), err
	}
	if pending != nil {
		return r.turnOutput(state, newMessageStart, recorder, pending), nil
	}
	return r.runModelLoop(ctx, state, newMessageStart, recorder)
}

type turnRecorder struct {
	events            []runtime.Event
	modelLogs         []runtime.ModelLog
	sink              EventSink
	turnID            string
	streamModelOutput bool
}

func newTurnRecorder(sink EventSink, turnID string, streamModelOutput bool) *turnRecorder {
	return &turnRecorder{sink: sink, turnID: turnID, streamModelOutput: streamModelOutput}
}

func (r *turnRecorder) emit(event runtime.Event) error {
	if event.TurnID == "" {
		event.TurnID = r.turnID
	}
	r.events = append(r.events, event)
	return emit(r.sink, event)
}

func (r *Runner) runModelLoop(ctx context.Context, state *State, newMessageStart int, recorder *turnRecorder) (TurnOutput, error) {

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

		modelCallID := modelCallID(recorder.turnID, i+1)
		if err := recorder.emit(modelEvent("model_started", "Model started", modelCallID)); err != nil {
			return TurnOutput{}, err
		}
		modelLog := newModelLog(len(recorder.modelLogs)+1, modelCallID, req)
		var resp model.Response
		var err error
		if recorder.streamModelOutput {
			if streamer, ok := r.model.(model.StreamingProvider); ok {
				resp, err = streamer.GenerateStream(ctx, req, func(delta string) error {
					event := modelEvent("model_output_delta", "Model output delta", modelCallID)
					event.Data = map[string]any{"delta": delta}
					return recorder.emit(event)
				})
			} else {
				resp, err = r.model.Generate(ctx, req)
			}
		} else {
			resp, err = r.model.Generate(ctx, req)
		}
		if err != nil {
			modelLog.CompletedAt = time.Now().UTC()
			modelLog.Error = err.Error()
			recorder.modelLogs = append(recorder.modelLogs, modelLog)
			return r.turnOutput(state, newMessageStart, recorder, nil), err
		}
		modelLog.CompletedAt = time.Now().UTC()
		modelLog.Response = runtime.ModelResponseLog{
			Message: resp.Message,
			Usage:   resp.Usage,
		}
		recorder.modelLogs = append(recorder.modelLogs, modelLog)
		if err := recorder.emit(modelCompletedEvent(modelCallID, resp.Usage)); err != nil {
			return TurnOutput{}, err
		}
		if err := r.afterModel(ctx, &resp, state); err != nil {
			return TurnOutput{}, err
		}

		resp.Message.Usage = resp.Usage
		state.Messages = append(state.Messages, timestampMessage(resp.Message))
		if err := r.updatePlanFromAssistant(recorder.emit, state, resp.Message); err != nil {
			return TurnOutput{}, err
		}
		if len(resp.Message.ToolCalls) == 0 {
			return r.turnOutput(state, newMessageStart, recorder, nil), nil
		}
		pending, err := r.runToolCalls(ctx, resp.Message.ToolCalls, state, recorder)
		if err != nil {
			return r.turnOutput(state, newMessageStart, recorder, nil), err
		}
		if pending != nil {
			return r.turnOutput(state, newMessageStart, recorder, pending), nil
		}
	}

	return r.turnOutput(state, newMessageStart, recorder, nil), errors.New("agent turn exceeded max iterations")
}

func (r *Runner) runToolCalls(ctx context.Context, calls []runtime.ToolCall, state *State, recorder *turnRecorder) (*runtime.PendingShellApproval, error) {
	for i, call := range calls {
		if err := recorder.emit(toolEvent("tool_started", call.Name, call.ID)); err != nil {
			return nil, err
		}
		result, pending := r.executeTool(ctx, call, state)
		if pending != nil {
			pending.RemainingToolCalls = append([]runtime.ToolCall(nil), calls[i+1:]...)
			if err := recorder.emit(toolEvent("approval_required", "Shell approval required", call.ID)); err != nil {
				return nil, err
			}
			return pending, nil
		}
		if err := recorder.emit(toolEvent("tool_completed", result.Name, result.ToolCallID)); err != nil {
			return nil, err
		}
		appendToolMessage(state, result)
	}
	return nil, nil
}

func appendToolMessage(state *State, result runtime.ToolResult) {
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

func (r *Runner) turnOutput(state *State, newMessageStart int, recorder *turnRecorder, pending *runtime.PendingShellApproval) TurnOutput {
	return TurnOutput{
		Messages:        append([]runtime.Message(nil), state.Messages...),
		NewMessages:     append([]runtime.Message(nil), state.Messages[newMessageStart:]...),
		Plan:            state.Plan,
		Compaction:      state.Compaction,
		PendingApproval: pending,
		Events:          append([]runtime.Event(nil), recorder.events...),
		ModelLogs:       append([]runtime.ModelLog(nil), recorder.modelLogs...),
	}
}

func newModelLog(index int, callID string, req model.Request) runtime.ModelLog {
	return runtime.ModelLog{
		Index:     index,
		CallID:    callID,
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

func modelCompletedEvent(modelCallID string, usage *runtime.TokenUsage) runtime.Event {
	event := runtime.NewEvent("model_completed", "Model completed")
	event.ModelCallID = modelCallID
	event.Usage = usage
	return event
}

func modelEvent(eventType, message, modelCallID string) runtime.Event {
	event := runtime.NewEvent(eventType, message)
	event.ModelCallID = modelCallID
	return event
}

func toolEvent(eventType, message, toolCallID string) runtime.Event {
	event := runtime.NewEvent(eventType, message)
	event.ToolCallID = toolCallID
	return event
}

func modelCallID(turnID string, index int) string {
	if turnID == "" {
		return fmt.Sprintf("model_call_%d", index)
	}
	return fmt.Sprintf("%s_model_%d", turnID, index)
}

func (r *Runner) executeTool(ctx context.Context, call runtime.ToolCall, state *State) (runtime.ToolResult, *runtime.PendingShellApproval) {
	result := runtime.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
	}
	if err := r.beforeTool(ctx, call, state); err != nil {
		var approval approvalRequiredError
		if errors.As(err, &approval) {
			pending := approval.approval
			return result, &pending
		}
		result.Error = err.Error()
		_ = r.afterTool(ctx, result, state)
		return result, nil
	}
	return r.executeRegisteredTool(ctx, call, state, result), nil
}

func (r *Runner) executeApprovedTool(ctx context.Context, call runtime.ToolCall, state *State) runtime.ToolResult {
	result := runtime.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
	}
	return r.executeRegisteredTool(ctx, call, state, result)
}

func (r *Runner) executeRegisteredTool(ctx context.Context, call runtime.ToolCall, state *State, result runtime.ToolResult) runtime.ToolResult {
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
