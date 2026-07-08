package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dengw8/harness-practice/internal/agent"
	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
	"github.com/dengw8/harness-practice/internal/store/local"
	"github.com/dengw8/harness-practice/internal/tools"
)

func TestCreateRunCompletesNoToolTurn(t *testing.T) {
	runner := agent.NewRunner(agent.Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 2,
	})
	runStore := local.New(t.TempDir())
	srv := New(Config{Runner: runner, Store: runStore, ModelProvider: "fake"})

	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"message":"hello"}`))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp createRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Run.Status != store.RunStatusCompleted {
		t.Fatalf("status = %q, want completed", resp.Run.Status)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(resp.Messages))
	}
	if resp.Messages[1].Content != "fake response: hello" {
		t.Fatalf("assistant content = %q", resp.Messages[1].Content)
	}
	for i, msg := range resp.Messages {
		if msg.CreatedAt.IsZero() {
			t.Fatalf("response message %d has zero created_at: %#v", i, msg)
		}
	}

	persistedMessages, err := runStore.ListMessages(req.Context(), resp.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedMessages) != 2 {
		t.Fatalf("persisted message count = %d, want 2", len(persistedMessages))
	}
}

func TestCreateRunPersistsTokenUsageInMetadataAndEvents(t *testing.T) {
	usage := &runtime.TokenUsage{
		PromptTokens:     intPtr(10),
		CompletionTokens: intPtr(3),
		TotalTokens:      intPtr(13),
		CachedTokens:     intPtr(4),
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: usageProvider{usage: usage}}),
		Store:         runStore,
		ModelProvider: "usage-test",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp createRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Run.TokenUsage == nil || *resp.Run.TokenUsage.PromptTokens != 10 || *resp.Run.TokenUsage.CachedTokens != 4 {
		t.Fatalf("response token usage = %#v", resp.Run.TokenUsage)
	}
	if len(resp.Messages) != 2 || resp.Messages[1].Usage == nil || *resp.Messages[1].Usage.TotalTokens != 13 {
		t.Fatalf("response message usage = %#v", resp.Messages)
	}

	messages, err := runStore.ListMessages(context.Background(), resp.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Usage == nil || *messages[1].Usage.CachedTokens != 4 {
		t.Fatalf("stored message usage = %#v", messages)
	}

	events, err := runStore.ListEvents(context.Background(), resp.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var modelCompleted *runtime.Event
	for i := range events {
		if events[i].Type == "model_completed" {
			modelCompleted = &events[i]
			break
		}
	}
	if modelCompleted == nil || modelCompleted.Usage == nil || *modelCompleted.Usage.TotalTokens != 13 {
		t.Fatalf("model_completed event = %#v", modelCompleted)
	}
}

func TestServesEmbeddedConsoleApp(t *testing.T) {
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         local.New(t.TempDir()),
		ModelProvider: "fake",
	})

	req := httptest.NewRequest(http.MethodGet, "/app/", nil)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Agent Service Console") {
		t.Fatalf("console HTML missing expected title: %s", rec.Body.String())
	}
}

func TestCreateRunWithoutMessagePersistsMetadata(t *testing.T) {
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         local.New(t.TempDir()),
		ModelProvider: "fake",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp createRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Run.ID == "" {
		t.Fatal("expected run id")
	}
	if resp.Run.Status != store.RunStatusCreated {
		t.Fatalf("status = %q, want created", resp.Run.Status)
	}
	if len(resp.Messages) != 0 {
		t.Fatalf("message count = %d, want 0", len(resp.Messages))
	}
}

func TestCreateRunPersistsUserAttachments(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	body := `{"message":"describe","attachments":[{"type":"image","media_type":"image/png","data":"aW1hZ2U=","name":"image.png"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp createRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(resp.Messages))
	}
	if len(resp.Messages[0].Attachments) != 1 {
		t.Fatalf("response attachments = %#v", resp.Messages[0].Attachments)
	}

	persistedMessages, err := runStore.ListMessages(req.Context(), resp.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedMessages) != 2 || len(persistedMessages[0].Attachments) != 1 {
		t.Fatalf("persisted messages = %#v", persistedMessages)
	}
	if persistedMessages[0].Attachments[0].MediaType != "image/png" {
		t.Fatalf("attachment = %#v", persistedMessages[0].Attachments[0])
	}
}

func TestAppendTurnAfterLargeImageHistory(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	body := `{"message":"describe","attachments":[{"type":"image","media_type":"image/png","data":"` + strings.Repeat("a", 128*1024) + `","name":"large.png"}]}`
	created := createRunForTest(t, srv, body)

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"second turn"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp appendTurnResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(resp.Messages))
	}
}

func TestListRunsReturnsStoredRunsNewestUpdatedFirst(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	first := createRunForTest(t, srv, `{}`)
	time.Sleep(time.Millisecond)
	second := createRunForTest(t, srv, `{}`)
	time.Sleep(time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+first.Run.ID+"/turns", bytes.NewBufferString(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("append status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	rec = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp listRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("run count = %d, want 2", len(resp.Runs))
	}
	if resp.Runs[0].ID != first.Run.ID || resp.Runs[1].ID != second.Run.ID {
		t.Fatalf("runs = %#v, want updated run first", resp.Runs)
	}
}

func TestPlanModeHTTPLifecycle(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:      model.NewFakeProvider(),
			Middleware: []agent.Middleware{agent.PlanModeMiddleware{}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{"message":"please plan","plan_mode":true}`)
	if created.Run.PlanMode != string(runtime.PlanModePlanning) {
		t.Fatalf("run plan mode = %q, want planning", created.Run.PlanMode)
	}
	if len(created.Plan.Steps) != 3 {
		t.Fatalf("created plan = %#v", created.Plan)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/plan/execute", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"continue"}`))
	rec = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("turn status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var turn appendTurnResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &turn); err != nil {
		t.Fatal(err)
	}
	if turn.Plan.Steps[0].Status != runtime.PlanStepCompleted {
		t.Fatalf("turn plan = %#v", turn.Plan)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.Run.ID+"/plan", nil)
	rec = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get plan status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var plan runtime.PlanState
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.CompletedCount() != 1 {
		t.Fatalf("persisted plan = %#v", plan)
	}
}

func TestRunReadAPIsReturnPersistedData(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	createReq := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"message":"hello"}`))
	createRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created createRunResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	getJSON := func(path string, target any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}

	var meta store.RunMetadata
	getJSON("/v1/runs/"+created.Run.ID, &meta)
	if meta.ID != created.Run.ID {
		t.Fatalf("metadata id = %q, want %q", meta.ID, created.Run.ID)
	}

	var messages []runtime.Message
	getJSON("/v1/runs/"+created.Run.ID+"/messages", &messages)
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}

	var events []runtime.Event
	getJSON("/v1/runs/"+created.Run.ID+"/events", &events)
	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5", len(events))
	}
	wantEvents := []string{"run_created", "turn_started", "model_started", "model_completed", "turn_completed"}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events = %#v", events)
	}

	var modelLogs []runtime.ModelLog
	getJSON("/v1/runs/"+created.Run.ID+"/model-logs", &modelLogs)
	if len(modelLogs) != 1 {
		t.Fatalf("model log count = %d, want 1", len(modelLogs))
	}
	if modelLogs[0].Request.Messages[0].Content != "hello" || modelLogs[0].Response.Message.Content != "fake response: hello" {
		t.Fatalf("model log = %#v", modelLogs[0])
	}
}

func TestAppendTurnLoadsHistoryAndPersistsOnlyNewMessages(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{"message":"first"}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"second"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp appendTurnResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 4 {
		t.Fatalf("all message count = %d, want 4", len(resp.Messages))
	}
	if len(resp.NewMessages) != 2 {
		t.Fatalf("new message count = %d, want 2", len(resp.NewMessages))
	}
	for i, msg := range resp.NewMessages {
		if msg.CreatedAt.IsZero() {
			t.Fatalf("new response message %d has zero created_at: %#v", i, msg)
		}
	}
	if resp.NewMessages[0].Content != "second" || resp.NewMessages[1].Content != "fake response: second" {
		t.Fatalf("new messages = %#v", resp.NewMessages)
	}

	persistedMessages, err := runStore.ListMessages(req.Context(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedMessages) != 4 {
		t.Fatalf("persisted message count = %d, want 4", len(persistedMessages))
	}
}

func TestAppendTurnRejectsRunningRun(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{}`)
	if _, err := runStore.UpdateRun(context.Background(), created.Run.ID, store.UpdateRunInput{Status: store.RunStatusRunning}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"second"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAppendTurnFailurePersistsFailedRunAndEvent(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: failingProvider{err: errors.New("model failed")}}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider()}),
		Store:         runStore,
		ModelProvider: "fake",
	}), `{}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"fail"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}

	meta, err := runStore.GetRun(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.RunStatusFailed {
		t.Fatalf("status = %q, want failed", meta.Status)
	}
	if meta.Error != "model failed" {
		t.Fatalf("error = %q, want model failed", meta.Error)
	}

	events, err := runStore.ListEvents(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "turn_failed" {
		t.Fatalf("last event = %#v, want turn_failed", events[len(events)-1])
	}

	modelLogs, err := runStore.ListModelLogs(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelLogs) != 1 || modelLogs[0].Error != "model failed" {
		t.Fatalf("model logs = %#v", modelLogs)
	}
	if modelLogs[0].Request.Messages[0].Content != "fail" {
		t.Fatalf("model log request = %#v", modelLogs[0].Request)
	}
}

func TestCreateRunPersistsWorkspaceToolMessages(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("file content"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(tools.NewReadFileTool(workspace), tools.NewListFilesTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:         model.NewFakeProvider(),
			MaxIterations: 4,
			Tools:         registry,
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{"message":"read_file:note.txt"}`)
	if len(created.Messages) != 4 {
		t.Fatalf("response message count = %d, want 4", len(created.Messages))
	}
	if created.Messages[2].Role != "tool" || created.Messages[2].Content != "file content" {
		t.Fatalf("tool message = %#v", created.Messages[2])
	}

	persisted, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 4 {
		t.Fatalf("persisted message count = %d, want 4", len(persisted))
	}
	if len(persisted[1].ToolCalls) != 1 || persisted[1].ToolCalls[0].Name != "read_file" {
		t.Fatalf("assistant tool call = %#v", persisted[1].ToolCalls)
	}
	if persisted[2].Role != runtime.RoleTool || persisted[2].Content != "file content" {
		t.Fatalf("persisted tool message = %#v", persisted[2])
	}
}

func TestCreateRunPersistsRejectedShellToolMessage(t *testing.T) {
	workspace := t.TempDir()
	registry, err := tools.NewRegistry(tools.NewShellTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:         model.NewFakeProvider(),
			MaxIterations: 4,
			Tools:         registry,
			Middleware:    []agent.Middleware{agent.SafetyMiddleware{}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{"message":"shell:rm file.txt"}`)
	if len(created.Messages) != 4 {
		t.Fatalf("response message count = %d, want 4", len(created.Messages))
	}
	if created.Messages[2].Role != "tool" || !strings.Contains(created.Messages[2].Content, "destructive command") {
		t.Fatalf("tool message = %#v", created.Messages[2])
	}

	persisted, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 4 {
		t.Fatalf("persisted message count = %d, want 4", len(persisted))
	}
	if persisted[2].Role != runtime.RoleTool || !strings.Contains(persisted[2].Content, "destructive command") {
		t.Fatalf("persisted rejected tool message = %#v", persisted[2])
	}
}

func TestAppendTurnStreamEmitsAndPersistsEvents(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("file content"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := tools.NewRegistry(tools.NewReadFileTool(workspace), tools.NewListFilesTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:         model.NewFakeProvider(),
			MaxIterations: 4,
			Tools:         registry,
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns/stream", bytes.NewBufferString(`{"message":"read_file:note.txt"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("content-type"); contentType != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", contentType)
	}
	body := rec.Body.String()
	for _, eventName := range []string{"turn_started", "model_started", "model_completed", "tool_started", "tool_completed", "turn_completed"} {
		if !strings.Contains(body, "event: "+eventName) {
			t.Fatalf("SSE body missing %s: %s", eventName, body)
		}
	}

	events, err := runStore.ListEvents(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventName := range []string{"turn_started", "model_started", "model_completed", "tool_started", "tool_completed", "turn_completed"} {
		if !hasEventType(events, eventName) {
			t.Fatalf("persisted events missing %s: %#v", eventName, events)
		}
	}

	messages, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(messages))
	}
	if messages[2].Role != runtime.RoleTool || messages[2].Content != "file content" {
		t.Fatalf("tool message = %#v", messages[2])
	}
}

func createRunForTest(t *testing.T, srv *Server, body string) createRunResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp createRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

type failingProvider struct {
	err error
}

type usageProvider struct {
	usage *runtime.TokenUsage
}

func (p usageProvider) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	return model.Response{
		Message: runtime.Message{Role: runtime.RoleAssistant, Content: "done"},
		Usage:   p.usage,
	}, nil
}

func (p failingProvider) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	return model.Response{}, p.err
}

func eventTypes(events []runtime.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func hasEventType(events []runtime.Event, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func intPtr(value int) *int {
	return &value
}
