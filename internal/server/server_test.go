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

	"github.com/dengw8/Gian/internal/agent"
	"github.com/dengw8/Gian/internal/model"
	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
	"github.com/dengw8/Gian/internal/store/local"
	"github.com/dengw8/Gian/internal/tools"
	traceplugin "github.com/dengw8/Gian/internal/trace"
)

func TestCreateRunCompletesNoToolTurn(t *testing.T) {
	runner := agent.NewRunner(agent.Config{
		Model:         model.NewFakeProvider(),
		MaxIterations: 2,
	})
	root := t.TempDir()
	runStore := local.New(root)
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
	if _, err := os.Stat(filepath.Join(root, "runs", resp.Run.ID, "compaction.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compaction file err = %v, want not exist when compaction is disabled", err)
	}
}

func TestTraceEndpointReturnsObservedRunTree(t *testing.T) {
	runStore := local.New(t.TempDir())
	observer := traceplugin.NewObserver(runStore)
	t.Cleanup(observer.Close)
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider(), MaxIterations: 2, TraceObserver: observer}),
		Store:         runStore,
		TraceStore:    runStore,
		TraceObserver: observer,
		ModelProvider: "fake",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"message":"hello"}`))
	req.Header.Set("content-type", "application/json")
	res := httptest.NewRecorder()
	srv.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", res.Code, res.Body.String())
	}
	var created createRunResponse
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	appendReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"second"}`))
	appendReq.Header.Set("content-type", "application/json")
	appendRes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(appendRes, appendReq)
	if appendRes.Code != http.StatusOK {
		t.Fatalf("append status = %d, body=%s", appendRes.Code, appendRes.Body.String())
	}
	observer.Close()

	traceReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.Run.ID+"/trace", nil)
	traceRes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(traceRes, traceReq)
	if traceRes.Code != http.StatusOK {
		t.Fatalf("trace status = %d, body=%s", traceRes.Code, traceRes.Body.String())
	}
	var got traceResponse
	if err := json.Unmarshal(traceRes.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Spans) < 3 {
		t.Fatalf("trace = %#v, want available run/turn/model spans", got)
	}
	var firstTurn, laterTurn runtime.TraceSpan
	for _, span := range got.Spans {
		if span.Kind == runtime.TraceSpanTurn {
			var input map[string]json.RawMessage
			if err := json.Unmarshal(span.Input, &input); err != nil {
				t.Fatalf("decode turn input: %v", err)
			}
			if _, ok := input["system_prompt"]; ok {
				firstTurn = span
			} else {
				laterTurn = span
			}
		}
	}
	if firstTurn.ID == "" || laterTurn.ID == "" {
		t.Fatalf("turn spans = %#v, want first and later turns", got.Spans)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(firstTurn.Input, &input); err != nil {
		t.Fatalf("decode turn input: %v", err)
	}
	if string(input["system_prompt"]) == "\"\"" || len(input["system_prompt"]) == 0 {
		t.Fatalf("first turn lacks system prompt: %s", firstTurn.Input)
	}
	var userPrompt runtime.Message
	if err := json.Unmarshal(input["user_prompt"], &userPrompt); err != nil {
		t.Fatalf("decode user prompt: %v", err)
	}
	if userPrompt.Role != runtime.RoleUser || userPrompt.Content != "hello" {
		t.Fatalf("user prompt = %#v, want hello", userPrompt)
	}
	if !userPrompt.CreatedAt.Equal(created.Messages[0].CreatedAt) {
		t.Fatalf("user prompt timestamp = %s, want observed message timestamp %s", userPrompt.CreatedAt, created.Messages[0].CreatedAt)
	}
	var output []runtime.Message
	if err := json.Unmarshal(firstTurn.Output, &output); err != nil {
		t.Fatalf("decode turn output: %v", err)
	}
	if len(output) == 0 || output[0].Role != runtime.RoleAssistant {
		t.Fatalf("turn output = %#v, want assistant output", output)
	}
	input = nil
	if err := json.Unmarshal(laterTurn.Input, &input); err != nil {
		t.Fatalf("decode later turn input: %v", err)
	}
	if _, ok := input["system_prompt"]; ok {
		t.Fatalf("later turn should not include system prompt: %s", laterTurn.Input)
	}
	if err := json.Unmarshal(input["user_prompt"], &userPrompt); err != nil {
		t.Fatalf("decode later user prompt: %v", err)
	}
	if userPrompt.Content != "second" {
		t.Fatalf("later user prompt = %#v, want second", userPrompt)
	}
}

func TestTraceRunSpanExcludesIdleTimeBeforeATurn(t *testing.T) {
	runStore := local.New(t.TempDir())
	observer := traceplugin.NewObserver(runStore)
	t.Cleanup(observer.Close)
	srv := New(Config{
		Runner:        agent.NewRunner(agent.Config{Model: model.NewFakeProvider(), TraceObserver: observer}),
		Store:         runStore,
		TraceStore:    runStore,
		TraceObserver: observer,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{}`)
	const idle = 80 * time.Millisecond
	time.Sleep(idle)
	appendReq := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/turns", bytes.NewBufferString(`{"message":"hello"}`))
	appendReq.Header.Set("content-type", "application/json")
	appendRes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(appendRes, appendReq)
	if appendRes.Code != http.StatusOK {
		t.Fatalf("append status = %d, body=%s", appendRes.Code, appendRes.Body.String())
	}
	observer.Close()

	spans, err := runStore.ListTraceSpans(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range spans {
		if span.Kind != runtime.TraceSpanRun {
			continue
		}
		if !span.StartedAt.After(created.Run.CreatedAt) {
			t.Fatalf("run span started_at = %s, must start when execution begins after run creation %s", span.StartedAt, created.Run.CreatedAt)
		}
		if span.DurationMS >= idle.Milliseconds()/2 {
			t.Fatalf("run duration = %dms, should exclude %dms of idle time", span.DurationMS, idle.Milliseconds())
		}
		return
	}
	t.Fatal("expected a run span")
}

func TestGetPendingApprovalReturnsEmptySuccessWhenIdle(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{Store: runStore, ModelProvider: "fake"})
	created := createRunForTest(t, srv, `{}`)
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.Run.ID+"/approvals/pending", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got pendingApprovalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Approval != nil {
		t.Fatalf("approval = %#v, want nil", got.Approval)
	}
}

func TestTraceAPIExposesDiagnosticsFilteringAndComparison(t *testing.T) {
	runStore := local.New(t.TempDir())
	first, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runStore.CreateRun(context.Background(), store.CreateRunInput{ModelProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Second)
	spans := []runtime.TraceSpan{
		{ID: "trace_run_" + first.ID, RunID: first.ID, Kind: runtime.TraceSpanRun, Name: "Run", Status: runtime.TraceStatusSucceeded, Revision: 1, Sequence: 1, StartedAt: started, CompletedAt: time.Now().UTC()},
		{ID: "tool_call", ParentID: "trace_run_" + first.ID, RunID: first.ID, Kind: runtime.TraceSpanTool, Name: "read_file", Status: runtime.TraceStatusFailed, Revision: 1, Sequence: 2, StartedAt: started, CompletedAt: time.Now().UTC(), Error: "missing"},
	}
	for _, span := range spans {
		if err := runStore.UpsertTraceSpan(context.Background(), span); err != nil {
			t.Fatal(err)
		}
	}
	if err := runStore.UpsertTraceSpan(context.Background(), runtime.TraceSpan{ID: "trace_run_" + second.ID, RunID: second.ID, Kind: runtime.TraceSpanRun, Name: "Run", Status: runtime.TraceStatusSucceeded, Revision: 1, Sequence: 1, StartedAt: started, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: runStore, TraceStore: runStore})
	traceReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+first.ID+"/trace?kind=tool&limit=1", nil)
	traceRes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(traceRes, traceReq)
	if traceRes.Code != http.StatusOK {
		t.Fatalf("trace status = %d body=%s", traceRes.Code, traceRes.Body.String())
	}
	var trace traceResponse
	if err := json.Unmarshal(traceRes.Body.Bytes(), &trace); err != nil {
		t.Fatal(err)
	}
	if trace.TotalSpans != 2 || len(trace.Spans) != 1 || trace.Spans[0].Kind != runtime.TraceSpanTool || !trace.FilterActive {
		t.Fatalf("trace = %#v", trace)
	}
	compareReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+first.ID+"/trace/compare/"+second.ID, nil)
	compareRes := httptest.NewRecorder()
	srv.Routes().ServeHTTP(compareRes, compareReq)
	if compareRes.Code != http.StatusOK {
		t.Fatalf("compare status = %d body=%s", compareRes.Code, compareRes.Body.String())
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
	if !strings.Contains(rec.Body.String(), "Gian Console") {
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

func TestAppendTurnPersistsCompactionAndLogsCompactedRequest(t *testing.T) {
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model: model.NewFakeProvider(),
			Middleware: []agent.Middleware{agent.CompactionMiddleware{
				Enabled:     true,
				MaxMessages: 2,
				KeepRecent:  1,
			}},
		}),
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

	messages, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("persisted message count = %d, want full history length 4", len(messages))
	}

	compaction, err := runStore.GetCompaction(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !compaction.Enabled || compaction.CompactedMessages != 2 || compaction.KeepRecent != 1 {
		t.Fatalf("compaction = %#v", compaction)
	}
	if !strings.Contains(compaction.Summary, "fake response: first") {
		t.Fatalf("compaction summary = %q, want first turn summary", compaction.Summary)
	}

	modelLogs, err := runStore.ListModelLogs(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelLogs) != 2 {
		t.Fatalf("model log count = %d, want 2", len(modelLogs))
	}
	secondRequest := modelLogs[1].Request
	if strings.Contains(secondRequest.SystemPrompt, "# Compacted Conversation Context") {
		t.Fatalf("second request system prompt should not include compaction: %s", secondRequest.SystemPrompt)
	}
	if len(secondRequest.Messages) != 2 {
		t.Fatalf("second request messages = %#v, want summary and current user message", secondRequest.Messages)
	}
	if secondRequest.Messages[0].Role != runtime.RoleUser || !strings.Contains(secondRequest.Messages[0].Content, "# Compacted Conversation Context") {
		t.Fatalf("second request first message should be compaction summary: %#v", secondRequest.Messages[0])
	}
	if secondRequest.Messages[1].Role != runtime.RoleUser || secondRequest.Messages[1].Content != "second" {
		t.Fatalf("second request second message = %#v, want current user message", secondRequest.Messages[1])
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

func TestCreateRunPersistsPendingShellApproval(t *testing.T) {
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
			Middleware:    []agent.Middleware{agent.ShellApprovalMiddleware{Workspace: workspace}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})

	created := createRunForTest(t, srv, `{"message":"shell:rm file.txt"}`)
	if created.Run.Status != store.RunStatusAwaitingApproval {
		t.Fatalf("run status = %q, want awaiting approval", created.Run.Status)
	}
	if len(created.Messages) != 2 {
		t.Fatalf("response message count = %d, want 2", len(created.Messages))
	}
	pending, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.ToolCall.Name != "shell" {
		t.Fatalf("pending approval = %#v", pending)
	}

	persisted, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 {
		t.Fatalf("persisted message count = %d, want 2", len(persisted))
	}
	if persisted[1].Role != runtime.RoleAssistant || len(persisted[1].ToolCalls) != 1 {
		t.Fatalf("persisted assistant message = %#v", persisted[1])
	}
}

func TestResolveShellApprovalExecutesStoredScriptOnce(t *testing.T) {
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
			Middleware:    []agent.Middleware{agent.ShellApprovalMiddleware{Workspace: workspace}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{"message":"shell:touch approved.txt"}`)
	pending, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil {
		t.Fatal("expected pending approval")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.Run.ID+"/approvals/pending", nil)
	getRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("pending status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	body := bytes.NewBufferString(`{"decision":"approve"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/approvals/"+pending.ID, body)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "approved.txt")); err != nil {
		t.Fatalf("approved script did not execute: %v", err)
	}
	meta, err := runStore.GetRun(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", meta.Status)
	}
	if _, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID); err != nil {
		t.Fatal(err)
	}

	retry := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/approvals/"+pending.ID, bytes.NewBufferString(`{"decision":"approve"}`))
	retryRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(retryRec, retry)
	if retryRec.Code != http.StatusConflict {
		t.Fatalf("retry status = %d, body = %s", retryRec.Code, retryRec.Body.String())
	}
}

func TestResolveShellApprovalRejectsReplacementScript(t *testing.T) {
	workspace := t.TempDir()
	registry, err := tools.NewRegistry(tools.NewShellTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:      model.NewFakeProvider(),
			Tools:      registry,
			Middleware: []agent.Middleware{agent.ShellApprovalMiddleware{Workspace: workspace}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{"message":"shell:touch original.txt"}`)
	pending, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/approvals/"+pending.ID, bytes.NewBufferString(`{"decision":"approve","command":"touch replaced.txt"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "original.txt")); !os.IsNotExist(err) {
		t.Fatalf("original script executed before approval: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "replaced.txt")); !os.IsNotExist(err) {
		t.Fatalf("replacement script executed: %v", err)
	}
}

func TestResolveShellApprovalDenyResumesWithoutExecution(t *testing.T) {
	workspace := t.TempDir()
	registry, err := tools.NewRegistry(tools.NewShellTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:      model.NewFakeProvider(),
			Tools:      registry,
			Middleware: []agent.Middleware{agent.ShellApprovalMiddleware{Workspace: workspace}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{"message":"shell:touch denied.txt"}`)
	pending, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+created.Run.ID+"/approvals/"+pending.ID, bytes.NewBufferString(`{"decision":"deny"}`))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied script executed: %v", err)
	}
	messages, err := runStore.ListMessages(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 3 || messages[2].Role != runtime.RoleTool || !strings.Contains(messages[2].Content, "denied by user") {
		t.Fatalf("resolved messages = %#v", messages)
	}
	meta, err := runStore.GetRun(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.RunStatusCompleted {
		t.Fatalf("status = %q, want completed", meta.Status)
	}
}

func TestGetPendingApprovalMarksInterruptedExecutionFailed(t *testing.T) {
	workspace := t.TempDir()
	registry, err := tools.NewRegistry(tools.NewShellTool(workspace))
	if err != nil {
		t.Fatal(err)
	}
	runStore := local.New(t.TempDir())
	srv := New(Config{
		Runner: agent.NewRunner(agent.Config{
			Model:      model.NewFakeProvider(),
			Tools:      registry,
			Middleware: []agent.Middleware{agent.ShellApprovalMiddleware{Workspace: workspace}},
		}),
		Store:         runStore,
		ModelProvider: "fake",
	})
	created := createRunForTest(t, srv, `{"message":"shell:touch interrupted.txt"}`)
	pending, err := runStore.GetPendingShellApproval(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runStore.ClaimPendingShellApproval(context.Background(), created.Run.ID, pending.ID, pending.Version); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.Run.ID+"/approvals/pending", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "interrupted.txt")); !os.IsNotExist(err) {
		t.Fatalf("interrupted script was retried: %v", err)
	}
	meta, err := runStore.GetRun(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.RunStatusFailed || !strings.Contains(meta.Error, "manual recovery") {
		t.Fatalf("run metadata = %#v", meta)
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
	for _, eventName := range []string{"turn_started", "model_started", "model_completed", "model_output_delta", "tool_started", "tool_completed", "turn_completed"} {
		if !strings.Contains(body, "event: "+eventName) {
			t.Fatalf("SSE body missing %s: %s", eventName, body)
		}
	}

	events, err := runStore.ListEvents(context.Background(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventName := range []string{"turn_started", "model_started", "model_completed", "model_output_delta", "tool_started", "tool_completed", "turn_completed"} {
		if !hasEventType(events, eventName) {
			t.Fatalf("persisted events missing %s: %#v", eventName, events)
		}
	}
	for _, event := range events {
		if event.ID == "" || event.RunID != created.Run.ID {
			t.Fatalf("event lacks trace identity: %#v", event)
		}
		if event.Type == "model_output_delta" {
			if event.TurnID == "" || event.ModelCallID == "" || event.Data["delta"] == "" {
				t.Fatalf("model delta lacks structured fields: %#v", event)
			}
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
