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

type recordingMiddleware struct {
	NoopMiddleware
	calls     []string
	toolCalls []string
}

type requestRecordingProvider struct {
	requests []model.Request
	response runtime.Message
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
