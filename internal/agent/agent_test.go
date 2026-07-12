package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/tools"
)

func TestRunnerCompletesNoToolTurnWithFakeModel(t *testing.T) {
	runner := NewRunner(Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 2,
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	want := []runtime.Message{
		{Role: runtime.RoleUser, Content: "hello"},
		{Role: runtime.RoleAssistant, Content: "fake response: hello"},
	}
	if !reflect.DeepEqual(clearMessageTimes(out.Messages), want) {
		t.Fatalf("messages = %#v, want %#v", out.Messages, want)
	}
	assertMessagesHaveTimes(t, out.NewMessages)
	if len(out.ModelLogs) != 1 {
		t.Fatalf("model log count = %d, want 1", len(out.ModelLogs))
	}
	if out.ModelLogs[0].Request.Messages[0].Content != "hello" || out.ModelLogs[0].Response.Message.Content != "fake response: hello" {
		t.Fatalf("model log = %#v", out.ModelLogs[0])
	}
	if out.ModelLogs[0].StartedAt.IsZero() || out.ModelLogs[0].CompletedAt.IsZero() {
		t.Fatalf("model log timestamps not set: %#v", out.ModelLogs[0])
	}
}

func TestSystemPromptUsesStaticDynamicBoundary(t *testing.T) {
	prompt := NewSystemPromptBuilder().
		WithTools([]runtime.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			ReadOnly:    true,
		}}).
		WithPlan(runtime.PlanState{Mode: runtime.PlanModePlanning}).
		Build()

	roleIndex := strings.Index(prompt, "# Role")
	boundaryIndex := strings.Index(prompt, dynamicContextBoundary)
	toolsIndex := strings.Index(prompt, "# Available Tools")
	planIndex := strings.Index(prompt, "# Plan Mode Context")
	if roleIndex < 0 || boundaryIndex < 0 || toolsIndex < 0 || planIndex < 0 {
		t.Fatalf("prompt missing expected sections:\n%s", prompt)
	}
	if !(roleIndex < boundaryIndex && boundaryIndex < toolsIndex && toolsIndex < planIndex) {
		t.Fatalf("unexpected prompt order: role=%d boundary=%d tools=%d plan=%d\n%s", roleIndex, boundaryIndex, toolsIndex, planIndex, prompt)
	}
}

func TestRunnerCallsMiddlewareLifecycleForNoToolTurn(t *testing.T) {
	recorder := &recordingMiddleware{}
	runner := NewRunner(Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 2,
		Middleware:    []Middleware{recorder},
	})

	if _, err := runner.RunTurn(context.Background(), TurnInput{Message: "hello"}); err != nil {
		t.Fatal(err)
	}

	want := []string{"before_agent", "before_model", "after_model", "after_agent"}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("calls = %#v, want %#v", recorder.calls, want)
	}
}

func TestRunnerLoadsHistoryBeforeNewTurn(t *testing.T) {
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "second response"},
	}
	runner := NewRunner(Config{
		Model:         recorder,
		MaxIterations: 2,
	})

	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: "first"},
		{Role: runtime.RoleAssistant, Content: "first response"},
	}
	out, err := runner.RunTurn(context.Background(), TurnInput{
		History: history,
		Message: "second",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	wantRequestMessages := []runtime.Message{
		{Role: runtime.RoleUser, Content: "first"},
		{Role: runtime.RoleAssistant, Content: "first response"},
		{Role: runtime.RoleUser, Content: "second"},
	}
	if !reflect.DeepEqual(clearMessageTimes(recorder.requests[0].Messages), wantRequestMessages) {
		t.Fatalf("request messages = %#v, want %#v", recorder.requests[0].Messages, wantRequestMessages)
	}

	wantNewMessages := []runtime.Message{
		{Role: runtime.RoleUser, Content: "second"},
		{Role: runtime.RoleAssistant, Content: "second response"},
	}
	if !reflect.DeepEqual(clearMessageTimes(out.NewMessages), wantNewMessages) {
		t.Fatalf("new messages = %#v, want %#v", out.NewMessages, wantNewMessages)
	}
	assertMessagesHaveTimes(t, out.NewMessages)
	if len(out.Messages) != 4 {
		t.Fatalf("all message count = %d, want 4", len(out.Messages))
	}
}

func TestCompactionMiddlewareCompactsOldMessagesBeforeModel(t *testing.T) {
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "fifth response"},
	}
	runner := NewRunner(Config{
		Model: recorder,
		Middleware: []Middleware{CompactionMiddleware{
			Enabled:     true,
			MaxMessages: 4,
			KeepRecent:  2,
		}},
	})

	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: "first"},
		{Role: runtime.RoleAssistant, Content: "second"},
		{Role: runtime.RoleUser, Content: "third"},
		{Role: runtime.RoleAssistant, Content: "fourth"},
	}
	out, err := runner.RunTurn(context.Background(), TurnInput{
		History: history,
		Message: "fifth",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	gotMessages := clearMessageTimes(recorder.requests[0].Messages)
	wantMessages := []runtime.Message{
		{
			Role: runtime.RoleUser,
			Content: "# Compacted Conversation Context\n" +
				"The following is a deterministic summary of earlier conversation messages. Recent original messages are preserved separately.\n\n" +
				"Compacted 3 earlier message(s); keeping 2 recent message(s).\n\n" +
				"1. user: first\n" +
				"2. assistant: second\n" +
				"3. user: third",
		},
		{Role: runtime.RoleAssistant, Content: "fourth"},
		{Role: runtime.RoleUser, Content: "fifth"},
	}
	if !reflect.DeepEqual(gotMessages, wantMessages) {
		t.Fatalf("request messages = %#v, want %#v", gotMessages, wantMessages)
	}
	if strings.Contains(recorder.requests[0].SystemPrompt, "# Compacted Conversation Context") {
		t.Fatalf("system prompt should not include compaction context: %s", recorder.requests[0].SystemPrompt)
	}
	if len(out.Messages) != 6 {
		t.Fatalf("output messages = %d, want full un-compacted history length 6", len(out.Messages))
	}
	if !out.Compaction.Enabled || out.Compaction.CompactedMessages != 3 || out.Compaction.KeepRecent != 2 {
		t.Fatalf("compaction = %#v", out.Compaction)
	}
}

func TestCompactionMiddlewareKeepsAssistantToolCallWithToolResult(t *testing.T) {
	args := json.RawMessage(`{"path":"note.txt"}`)
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "next response"},
	}
	runner := NewRunner(Config{
		Model: recorder,
		Middleware: []Middleware{CompactionMiddleware{
			Enabled:     true,
			MaxMessages: 4,
			KeepRecent:  3,
		}},
	})

	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: "read note"},
		{Role: runtime.RoleAssistant, ToolCalls: []runtime.ToolCall{{ID: "call_1", Name: "read_file", Arguments: args}}},
		{Role: runtime.RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "note content"},
		{Role: runtime.RoleAssistant, Content: "note content says hello"},
	}
	if _, err := runner.RunTurn(context.Background(), TurnInput{
		History: history,
		Message: "continue",
	}); err != nil {
		t.Fatal(err)
	}

	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	gotMessages := recorder.requests[0].Messages
	if len(gotMessages) != 5 {
		t.Fatalf("request messages = %d, want 5 including summary and tool call/result pair: %#v", len(gotMessages), gotMessages)
	}
	if gotMessages[0].Role != runtime.RoleUser || !strings.Contains(gotMessages[0].Content, "# Compacted Conversation Context") {
		t.Fatalf("first request message should be compaction summary: %#v", gotMessages[0])
	}
	if gotMessages[1].Role != runtime.RoleAssistant || len(gotMessages[1].ToolCalls) != 1 {
		t.Fatalf("second request message should preserve assistant tool call: %#v", gotMessages[1])
	}
	if gotMessages[2].Role != runtime.RoleTool || gotMessages[2].ToolCallID != "call_1" {
		t.Fatalf("third request message should preserve tool result: %#v", gotMessages[2])
	}
}

func TestRunnerPreservesUserAttachments(t *testing.T) {
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "saw image"},
	}
	runner := NewRunner(Config{
		Model:         recorder,
		MaxIterations: 2,
	})

	attachments := []runtime.Attachment{{
		Type:      "image",
		MediaType: "image/png",
		Data:      "aW1hZ2U=",
		Name:      "image.png",
	}}
	out, err := runner.RunTurn(context.Background(), TurnInput{
		Message:     "describe",
		Attachments: attachments,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	if !reflect.DeepEqual(recorder.requests[0].Messages[0].Attachments, attachments) {
		t.Fatalf("request attachments = %#v, want %#v", recorder.requests[0].Messages[0].Attachments, attachments)
	}
	if !reflect.DeepEqual(out.NewMessages[0].Attachments, attachments) {
		t.Fatalf("new message attachments = %#v, want %#v", out.NewMessages[0].Attachments, attachments)
	}
}

func TestRunnerExecutesWorkspaceToolAndFeedsResultBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("file content"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(tools.NewReadFileTool(dir), tools.NewListFilesTool(dir))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingMiddleware{}
	runner := NewRunner(Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 4,
		Tools:         registry,
		Middleware:    []Middleware{recorder},
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{Message: "read_file:note.txt"})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.NewMessages) != 4 {
		t.Fatalf("new message count = %d, want 4: %#v", len(out.NewMessages), out.NewMessages)
	}
	assertMessagesHaveTimes(t, out.NewMessages)
	if len(out.NewMessages[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls = %#v", out.NewMessages[1].ToolCalls)
	}
	if out.NewMessages[2].Role != runtime.RoleTool {
		t.Fatalf("third message role = %q, want tool", out.NewMessages[2].Role)
	}
	if out.NewMessages[2].Content != "file content" {
		t.Fatalf("tool content = %q, want file content", out.NewMessages[2].Content)
	}
	if out.NewMessages[3].Content != "fake tool response from read_file: file content" {
		t.Fatalf("final content = %q", out.NewMessages[3].Content)
	}

	wantToolCalls := []string{"before_tool:read_file", "after_tool:read_file"}
	if !reflect.DeepEqual(recorder.toolCalls, wantToolCalls) {
		t.Fatalf("tool calls = %#v, want %#v", recorder.toolCalls, wantToolCalls)
	}
}

func TestRunnerAdvertisesToolsToModel(t *testing.T) {
	registry, err := tools.NewRegistry(tools.NewReadFileTool(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "done"},
	}
	runner := NewRunner(Config{
		Model:         recorder,
		MaxIterations: 2,
		Tools:         registry,
	})

	if _, err := runner.RunTurn(context.Background(), TurnInput{Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	if len(recorder.requests[0].Tools) != 1 || recorder.requests[0].Tools[0].Name != "read_file" {
		t.Fatalf("tools = %#v", recorder.requests[0].Tools)
	}
	if recorder.requests[0].SystemPrompt == "" {
		t.Fatal("expected system prompt")
	}
}

func TestSafetyMiddlewareRejectsShellBeforeExecution(t *testing.T) {
	shell := &recordingShellTool{}
	registry, err := tools.NewRegistry(shell)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 4,
		Tools:         registry,
		Middleware:    []Middleware{SafetyMiddleware{}},
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{Message: "shell:rm file.txt"})
	if err != nil {
		t.Fatal(err)
	}

	if shell.executed {
		t.Fatal("expected safety middleware to reject before tool execution")
	}
	if len(out.NewMessages) != 4 {
		t.Fatalf("new message count = %d, want 4", len(out.NewMessages))
	}
	assertMessagesHaveTimes(t, out.NewMessages)
	toolMessage := out.NewMessages[2]
	if toolMessage.Role != runtime.RoleTool {
		t.Fatalf("tool message role = %q", toolMessage.Role)
	}
	if !strings.Contains(toolMessage.Content, "destructive command") {
		t.Fatalf("tool message content = %q, want rejection", toolMessage.Content)
	}
}

func TestRunnerPlanModeExtractsPlanAndFiltersTools(t *testing.T) {
	registry, err := tools.NewRegistry(
		tools.NewReadFileTool(t.TempDir()),
		tools.NewShellTool(t.TempDir()),
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &requestRecordingProvider{
		response: runtime.Message{
			Role:    runtime.RoleAssistant,
			Content: "Plan:\n1. Inspect the code.\n2. Make the change.",
		},
	}
	runner := NewRunner(Config{
		Model:      recorder,
		Tools:      registry,
		Middleware: []Middleware{PlanModeMiddleware{}},
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{
		Message: "plan this",
		Plan:    runtime.PlanState{Mode: runtime.PlanModePlanning},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(recorder.requests))
	}
	if !strings.Contains(recorder.requests[0].SystemPrompt, "[PLAN MODE ACTIVE]") {
		t.Fatalf("system prompt missing plan mode context: %s", recorder.requests[0].SystemPrompt)
	}
	for _, definition := range recorder.requests[0].Tools {
		if definition.Name != "shell" && (!definition.ReadOnly || definition.Destructive) {
			t.Fatalf("non-read-only tool advertised in plan mode: %#v", definition)
		}
	}
	if out.Plan.Mode != runtime.PlanModePlanning || len(out.Plan.Steps) != 2 {
		t.Fatalf("plan = %#v", out.Plan)
	}
}

func TestRunnerPlanModeRejectsWriteShellCommand(t *testing.T) {
	shell := &recordingShellTool{}
	registry, err := tools.NewRegistry(shell)
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]string{"command": "touch file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(Config{
		Model: &sequenceProvider{responses: []runtime.Message{
			{
				Role:      runtime.RoleAssistant,
				ToolCalls: []runtime.ToolCall{{ID: "call_1", Name: "shell", Arguments: args}},
			},
			{Role: runtime.RoleAssistant, Content: "done"},
		}},
		MaxIterations: 4,
		Tools:         registry,
		Middleware:    []Middleware{PlanModeMiddleware{}},
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{
		Message: "shell:touch file.txt",
		Plan:    runtime.PlanState{Mode: runtime.PlanModePlanning},
	})
	if err != nil {
		t.Fatal(err)
	}
	if shell.executed {
		t.Fatal("expected plan mode to reject shell command before execution")
	}
	if !strings.Contains(out.NewMessages[2].Content, "plan mode") {
		t.Fatalf("tool message content = %q, want plan mode rejection", out.NewMessages[2].Content)
	}
}

func TestRunnerExecutionModeMarksCompletedSteps(t *testing.T) {
	recorder := &requestRecordingProvider{
		response: runtime.Message{Role: runtime.RoleAssistant, Content: "Finished the second step. [DONE:2]"},
	}
	runner := NewRunner(Config{
		Model:      recorder,
		Middleware: []Middleware{PlanModeMiddleware{}},
	})

	out, err := runner.RunTurn(context.Background(), TurnInput{
		Message: "continue",
		Plan: runtime.PlanState{
			Mode: runtime.PlanModeExecuting,
			Steps: []runtime.PlanStep{
				{Index: 1, Text: "First", Status: runtime.PlanStepPending},
				{Index: 2, Text: "Second", Status: runtime.PlanStepPending},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan.Steps[1].Status != runtime.PlanStepCompleted {
		t.Fatalf("plan = %#v", out.Plan)
	}
	if out.Plan.Mode != runtime.PlanModeExecuting {
		t.Fatalf("mode = %q, want executing until all steps are complete", out.Plan.Mode)
	}
}

type recordingMiddleware struct {
	NoopMiddleware
	calls     []string
	toolCalls []string
}

type requestRecordingProvider struct {
	requests []model.Request
	response runtime.Message
}

type sequenceProvider struct {
	requests  []model.Request
	responses []runtime.Message
}

type recordingShellTool struct {
	executed bool
}

func (t *recordingShellTool) Name() string {
	return "shell"
}

func (t *recordingShellTool) Description() string {
	return "recording shell"
}

func (t *recordingShellTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (t *recordingShellTool) ReadOnly() bool {
	return false
}

func (t *recordingShellTool) Destructive() bool {
	return true
}

func (t *recordingShellTool) Concurrent() bool {
	return false
}

func (t *recordingShellTool) Execute(ctx context.Context, args json.RawMessage) (runtime.ToolResult, error) {
	t.executed = true
	return runtime.ToolResult{Name: t.Name(), Content: "executed"}, nil
}

func (p *requestRecordingProvider) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	p.requests = append(p.requests, req)
	return model.Response{Message: p.response}, nil
}

func (p *sequenceProvider) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return model.Response{Message: runtime.Message{Role: runtime.RoleAssistant, Content: "done"}}, nil
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return model.Response{Message: response}, nil
}

func (m *recordingMiddleware) Name() string {
	return "recording"
}

func (m *recordingMiddleware) BeforeAgent(ctx context.Context, state *State) error {
	m.calls = append(m.calls, "before_agent")
	return nil
}

func (m *recordingMiddleware) BeforeModel(ctx context.Context, req *model.Request, state *State) error {
	m.calls = append(m.calls, "before_model")
	return nil
}

func (m *recordingMiddleware) AfterModel(ctx context.Context, resp *model.Response, state *State) error {
	m.calls = append(m.calls, "after_model")
	return nil
}

func (m *recordingMiddleware) AfterAgent(ctx context.Context, state *State) error {
	m.calls = append(m.calls, "after_agent")
	return nil
}

func (m *recordingMiddleware) BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	m.toolCalls = append(m.toolCalls, "before_tool:"+call.Name)
	return nil
}

func (m *recordingMiddleware) AfterTool(ctx context.Context, result runtime.ToolResult, state *State) error {
	m.toolCalls = append(m.toolCalls, "after_tool:"+result.Name)
	return nil
}

func clearMessageTimes(messages []runtime.Message) []runtime.Message {
	cleared := append([]runtime.Message(nil), messages...)
	for i := range cleared {
		cleared[i].CreatedAt = runtime.Message{}.CreatedAt
	}
	return cleared
}

func assertMessagesHaveTimes(t *testing.T, messages []runtime.Message) {
	t.Helper()
	for i, message := range messages {
		if message.CreatedAt.IsZero() {
			t.Fatalf("message %d has zero created_at: %#v", i, message)
		}
	}
}
