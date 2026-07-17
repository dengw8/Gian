package server

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengw8/Gian/internal/agent"
	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
	"github.com/dengw8/Gian/internal/tools"
)

//go:embed web/*
var appAssets embed.FS

type Config struct {
	Runner        *agent.Runner
	Store         store.RunStore
	TraceStore    store.TraceStore
	TraceObserver runtime.ExecutionObserver
	ModelProvider string
}

type Server struct {
	runner        *agent.Runner
	store         store.RunStore
	modelProvider string
	traceStore    store.TraceStore
	traceObserver runtime.ExecutionObserver
}

type createRunRequest struct {
	Message     string               `json:"message"`
	Attachments []runtime.Attachment `json:"attachments,omitempty"`
	PlanMode    bool                 `json:"plan_mode,omitempty"`
}

type appendTurnRequest struct {
	Message     string               `json:"message"`
	Attachments []runtime.Attachment `json:"attachments,omitempty"`
}

type setPlanModeRequest struct {
	Enabled bool `json:"enabled"`
}

type approvalDecisionRequest struct {
	Decision runtime.ShellApprovalDecision `json:"decision"`
}

type pendingApprovalResponse struct {
	Approval *runtime.PendingShellApproval `json:"approval"`
}

type createRunResponse struct {
	Run      store.RunMetadata `json:"run"`
	Messages []messageOutput   `json:"messages"`
	Plan     runtime.PlanState `json:"plan"`
}

type listRunsResponse struct {
	Runs []store.RunMetadata `json:"runs"`
}

type traceResponse struct {
	Available    bool                     `json:"available"`
	Reason       string                   `json:"reason,omitempty"`
	Spans        []runtime.TraceSpan      `json:"spans"`
	Diagnostics  runtime.TraceDiagnostics `json:"diagnostics"`
	TotalSpans   int                      `json:"total_spans,omitempty"`
	HiddenSpans  int                      `json:"hidden_spans,omitempty"`
	FilterActive bool                     `json:"filter_active,omitempty"`
}

type traceSpanResponse struct {
	Span runtime.TraceSpan `json:"span"`
}

type traceComparison struct {
	RunID       string              `json:"run_id"`
	Status      store.RunStatus     `json:"status"`
	TotalSpans  int                 `json:"total_spans"`
	FailedSpans int                 `json:"failed_spans"`
	DurationMS  int64               `json:"duration_ms"`
	TokenUsage  *runtime.TokenUsage `json:"token_usage,omitempty"`
	Kinds       map[string]int      `json:"kinds"`
}

type traceCompareResponse struct {
	Current traceComparison `json:"current"`
	Other   traceComparison `json:"other"`
}

type appendTurnResponse struct {
	Run         store.RunMetadata `json:"run"`
	Messages    []messageOutput   `json:"messages"`
	NewMessages []messageOutput   `json:"new_messages"`
	Plan        runtime.PlanState `json:"plan"`
}

type messageOutput struct {
	Role        string               `json:"role"`
	Content     string               `json:"content,omitempty"`
	ToolCallID  string               `json:"tool_call_id,omitempty"`
	Name        string               `json:"name,omitempty"`
	ToolCalls   []runtime.ToolCall   `json:"tool_calls,omitempty"`
	Attachments []runtime.Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time            `json:"created_at,omitempty"`
	Usage       *runtime.TokenUsage  `json:"usage,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(cfg Config) *Server {
	return &Server{
		runner:        cfg.Runner,
		store:         cfg.Store,
		modelProvider: cfg.ModelProvider,
		traceStore:    cfg.TraceStore,
		traceObserver: cfg.TraceObserver,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	appFS, err := fs.Sub(appAssets, "web")
	if err != nil {
		panic(err)
	}
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /app/runs/{runID}/{page}", s.handleAppIndex)
	mux.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.FS(appFS))))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/runs", s.handleListRuns)
	mux.HandleFunc("POST /v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /v1/runs/{runID}", s.handleGetRun)
	mux.HandleFunc("GET /v1/runs/{runID}/messages", s.handleListMessages)
	mux.HandleFunc("GET /v1/runs/{runID}/events", s.handleListEvents)
	mux.HandleFunc("GET /v1/runs/{runID}/model-logs", s.handleListModelLogs)
	mux.HandleFunc("GET /v1/runs/{runID}/trace", s.handleGetTrace)
	mux.HandleFunc("GET /v1/runs/{runID}/trace/spans/{spanID}", s.handleGetTraceSpan)
	mux.HandleFunc("GET /v1/runs/{runID}/trace/compare/{otherRunID}", s.handleCompareTrace)
	mux.HandleFunc("GET /v1/runs/{runID}/plan", s.handleGetPlan)
	mux.HandleFunc("GET /v1/runs/{runID}/approvals/pending", s.handleGetPendingApproval)
	mux.HandleFunc("POST /v1/runs/{runID}/approvals/{approvalID}", s.handleResolveApproval)
	mux.HandleFunc("POST /v1/runs/{runID}/plan-mode", s.handleSetPlanMode)
	mux.HandleFunc("POST /v1/runs/{runID}/plan/execute", s.handleExecutePlan)
	mux.HandleFunc("POST /v1/runs/{runID}/plan/reset", s.handleResetPlan)
	mux.HandleFunc("POST /v1/runs/{runID}/turns", s.handleAppendTurn)
	mux.HandleFunc("POST /v1/runs/{runID}/turns/stream", s.handleAppendTurnStream)
	return mux
}

func (s *Server) handleAppIndex(w http.ResponseWriter, r *http.Request) {
	page := r.PathValue("page")
	if page != "messages" && page != "trace" {
		http.NotFound(w, r)
		return
	}
	data, err := appAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "load app", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "run store is not configured"})
		return
	}

	runs, err := s.store.ListRuns(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, listRunsResponse{Runs: runs})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "run store is not configured"})
		return
	}

	var req createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON request body"})
		return
	}

	meta, err := s.store.CreateRun(r.Context(), store.CreateRunInput{ModelProvider: s.modelProvider})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if err := s.appendEvent(r.Context(), meta.ID, runtime.NewEvent("run_created", "Run created")); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	plan := runtime.EmptyPlanState()
	if req.PlanMode {
		plan.Mode = runtime.PlanModePlanning
		meta, err = s.store.SavePlan(r.Context(), meta.ID, plan)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}
		plan, _ = s.store.GetPlan(r.Context(), meta.ID)
		if err := s.appendEvent(r.Context(), meta.ID, runtime.NewEvent("plan_mode_enabled", "Plan mode enabled")); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}
	}

	message := strings.TrimSpace(req.Message)
	if message == "" && len(req.Attachments) == 0 {
		writeJSON(w, http.StatusCreated, createRunResponse{Run: meta, Plan: plan})
		return
	}

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, nil, plan, runtime.EmptyCompactionState(), nil, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, createRunResponse{
		Run:      meta,
		Messages: toMessageOutput(out.Messages),
		Plan:     out.Plan,
	})
}

func (s *Server) handleAppendTurn(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "run store is not configured"})
		return
	}

	runID := r.PathValue("runID")
	meta, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := rejectUnavailableRun(meta); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}

	var req appendTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON request body"})
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" && len(req.Attachments) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "message or attachment is required"})
		return
	}

	history, err := s.store.ListMessages(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	compaction, err := s.store.GetCompaction(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, history, plan, compaction, nil, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, appendTurnResponse{
		Run:         meta,
		Messages:    toMessageOutput(out.Messages),
		NewMessages: toMessageOutput(out.NewMessages),
		Plan:        out.Plan,
	})
}

func (s *Server) handleAppendTurnStream(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "run store is not configured"})
		return
	}
	stream, ok := newSSEStream(w)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "streaming is not supported"})
		return
	}

	runID := r.PathValue("runID")
	meta, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		stream.write(runtime.NewEvent("turn_failed", err.Error()))
		return
	}
	if err := rejectUnavailableRun(meta); err != nil {
		stream.write(runtime.NewEvent("turn_failed", err.Error()))
		return
	}

	var req appendTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		stream.write(runtime.NewEvent("turn_failed", "invalid JSON request body"))
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" && len(req.Attachments) == 0 {
		stream.write(runtime.NewEvent("turn_failed", "message or attachment is required"))
		return
	}

	history, err := s.store.ListMessages(r.Context(), runID)
	if err != nil {
		stream.write(runtime.NewEvent("turn_failed", err.Error()))
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		stream.write(runtime.NewEvent("turn_failed", err.Error()))
		return
	}
	compaction, err := s.store.GetCompaction(r.Context(), runID)
	if err != nil {
		stream.write(runtime.NewEvent("turn_failed", err.Error()))
		return
	}

	_, _, err = s.executeTurn(r, meta, message, req.Attachments, history, plan, compaction, stream.write, true)
	if err != nil {
		return
	}
}

func (s *Server) executeTurn(r *http.Request, meta store.RunMetadata, message string, attachments []runtime.Attachment, history []runtime.Message, plan runtime.PlanState, compaction runtime.CompactionState, sink agent.EventSink, streamModelOutput bool) (agent.TurnOutput, store.RunMetadata, error) {
	if s.runner == nil {
		return agent.TurnOutput{}, meta, errors.New("agent runner is not configured")
	}

	executionStarted := time.Now().UTC()
	updated, err := s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusRunning})
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
	meta = updated
	s.observeRunSpan(meta.ID, runtime.TraceStatusRunning, executionStarted, time.Time{}, "")
	turnID := newTraceID("turn")

	if err := s.emitAndPersist(r, meta.ID, turnEvent("turn_started", "Turn started", turnID), sink); err != nil {
		return agent.TurnOutput{}, meta, err
	}

	out, err := s.runner.RunTurn(r.Context(), agent.TurnInput{
		RunID:             meta.ID,
		History:           history,
		Message:           message,
		Attachments:       attachments,
		Plan:              plan,
		Compaction:        compaction,
		TurnID:            turnID,
		StreamModelOutput: streamModelOutput,
		Events: func(event runtime.Event) error {
			return s.emitAndPersist(r, meta.ID, event, sink)
		},
	})
	if err != nil {
		if len(out.ModelLogs) > 0 {
			_ = s.appendModelLogs(r, meta.ID, out.ModelLogs)
		}
		updated, updateErr := s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{
			Status: store.RunStatusFailed,
			Error:  err.Error(),
		})
		if updateErr == nil {
			meta = updated
		}
		_ = s.emitAndPersist(r, meta.ID, turnEvent("turn_failed", err.Error(), turnID), sink)
		now := time.Now().UTC()
		s.observeTurnDetails(meta.ID, turnID, history, message, attachments, out, runtime.TraceStatusFailed, 3, now)
		s.observeRunSpan(meta.ID, runtime.TraceStatusFailed, executionStarted, now, err.Error())
		return agent.TurnOutput{}, meta, err
	}

	if err := s.store.AppendMessages(r.Context(), meta.ID, out.NewMessages); err != nil {
		return agent.TurnOutput{}, meta, err
	}
	if err := s.appendModelLogs(r, meta.ID, out.ModelLogs); err != nil {
		return agent.TurnOutput{}, meta, err
	}
	if usage := totalUsage(out.Events); usage != nil {
		meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: meta.Status, Error: meta.Error, AddUsage: usage})
		if err != nil {
			return agent.TurnOutput{}, meta, err
		}
	}
	meta, err = s.store.SavePlan(r.Context(), meta.ID, out.Plan)
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
	if out.Compaction.Enabled {
		if err := s.store.SaveCompaction(r.Context(), meta.ID, out.Compaction); err != nil {
			return agent.TurnOutput{}, meta, err
		}
	}
	if out.PendingApproval != nil {
		out.PendingApproval.TurnID = turnID
		if err := s.store.SavePendingShellApproval(r.Context(), meta.ID, *out.PendingApproval); err != nil {
			return agent.TurnOutput{}, meta, err
		}
		meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusAwaitingApproval})
		if err != nil {
			return agent.TurnOutput{}, meta, err
		}
		s.observeTurnDetails(meta.ID, turnID, history, message, attachments, out, runtime.TraceStatusAwaitingApproval, 3, time.Time{})
		return out, meta, nil
	}
	if err := s.emitAndPersist(r, meta.ID, turnEvent("turn_completed", "Turn completed", turnID), sink); err != nil {
		return agent.TurnOutput{}, meta, err
	}
	meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusCompleted})
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
	now := time.Now().UTC()
	s.observeTurnDetails(meta.ID, turnID, history, message, attachments, out, runtime.TraceStatusSucceeded, 3, now)
	s.observeRunSpan(meta.ID, runtime.TraceStatusSucceeded, executionStarted, now, "")
	return out, meta, nil
}

func (s *Server) appendModelLogs(r *http.Request, runID string, logs []runtime.ModelLog) error {
	if len(logs) == 0 {
		return nil
	}
	existing, err := s.store.ListModelLogs(r.Context(), runID)
	if err != nil {
		return err
	}
	for i := range logs {
		logs[i].Index = len(existing) + i + 1
	}
	return s.store.AppendModelLogs(r.Context(), runID, logs)
}

func totalUsage(events []runtime.Event) *runtime.TokenUsage {
	var total *runtime.TokenUsage
	for _, event := range events {
		if event.Usage == nil {
			continue
		}
		total = addUsage(total, event.Usage)
	}
	return total
}

func addUsage(current *runtime.TokenUsage, next *runtime.TokenUsage) *runtime.TokenUsage {
	if next == nil {
		return current
	}
	sum := runtime.TokenUsage{}
	if current != nil {
		sum = *current
	}
	sum.PromptTokens = addIntPointers(sum.PromptTokens, next.PromptTokens)
	sum.CompletionTokens = addIntPointers(sum.CompletionTokens, next.CompletionTokens)
	sum.TotalTokens = addIntPointers(sum.TotalTokens, next.TotalTokens)
	sum.CachedTokens = addIntPointers(sum.CachedTokens, next.CachedTokens)
	sum.ReasoningTokens = addIntPointers(sum.ReasoningTokens, next.ReasoningTokens)
	return &sum
}

func addIntPointers(a *int, b *int) *int {
	if b == nil {
		return a
	}
	value := *b
	if a != nil {
		value += *a
	}
	return &value
}

func (s *Server) emitAndPersist(r *http.Request, runID string, event runtime.Event, sink agent.EventSink) error {
	event = stampEvent(runID, event)
	s.observeEvent(event)
	if sink != nil {
		if err := sink(event); err != nil {
			return err
		}
	}
	return s.store.AppendEvents(r.Context(), runID, []runtime.Event{event})
}

func (s *Server) appendEvent(ctx context.Context, runID string, event runtime.Event) error {
	return s.store.AppendEvents(ctx, runID, []runtime.Event{stampEvent(runID, event)})
}

func stampEvent(runID string, event runtime.Event) runtime.Event {
	if event.ID == "" {
		event.ID = newTraceID("evt")
	}
	event.RunID = runID
	return event
}

func (s *Server) handleGetPendingApproval(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if _, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeStoreError(w, err)
		return
	}
	approval, err := s.store.GetPendingShellApproval(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if approval == nil {
		// A missing approval is the normal idle state for the inspector. Return a
		// successful empty response so regular polling does not create spurious
		// browser-network errors or mask real approval failures.
		writeJSON(w, http.StatusOK, pendingApprovalResponse{})
		return
	}
	if approval.Status == runtime.ShellApprovalExecuting {
		s.markExecutingApprovalFailed(r.Context(), runID, approval.ID)
		writeJSON(w, http.StatusConflict, errorResponse{Error: "shell approval execution was interrupted; manual recovery is required"})
		return
	}
	writeJSON(w, http.StatusOK, pendingApprovalResponse{Approval: approval})
}

func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	meta, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if meta.Status != store.RunStatusAwaitingApproval {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "run is not awaiting shell approval"})
		return
	}
	var req approvalDecisionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid approval request body"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid approval request body"})
		return
	}
	if req.Decision != runtime.ShellApprovalApprove && req.Decision != runtime.ShellApprovalDeny && req.Decision != runtime.ShellApprovalCancel {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "decision must be approve, deny, or cancel"})
		return
	}
	approval, err := s.store.GetPendingShellApproval(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if approval == nil || approval.ID != r.PathValue("approvalID") || approval.Status != runtime.ShellApprovalPending {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "shell approval is no longer pending"})
		return
	}
	claimed, err := s.store.ClaimPendingShellApproval(r.Context(), runID, approval.ID, approval.Version)
	if err != nil {
		writeApprovalError(w, err)
		return
	}
	if err := verifyStoredShellApproval(claimed); err != nil {
		s.markExecutingApprovalFailed(r.Context(), runID, claimed.ID)
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	executionStarted := time.Now().UTC()
	meta, err = s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusRunning})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.observeRunSpan(runID, runtime.TraceStatusRunning, executionStarted, time.Time{}, "")
	history, err := s.store.ListMessages(r.Context(), runID)
	if err != nil {
		s.failResumedTurn(r.Context(), runID, err)
		writeStoreError(w, err)
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		s.failResumedTurn(r.Context(), runID, err)
		writeStoreError(w, err)
		return
	}
	compaction, err := s.store.GetCompaction(r.Context(), runID)
	if err != nil {
		s.failResumedTurn(r.Context(), runID, err)
		writeStoreError(w, err)
		return
	}
	turnID := claimed.TurnID
	if turnID == "" {
		turnID = newTraceID("turn")
	}
	if err := s.emitAndPersist(r, runID, turnEvent("turn_started", "Turn started", turnID), nil); err != nil {
		s.failResumedTurn(r.Context(), runID, err)
		writeStoreError(w, err)
		return
	}
	out, err := s.runner.ResumeTurn(r.Context(), agent.ResumeInput{
		RunID:      runID,
		History:    history,
		Plan:       plan,
		Compaction: compaction,
		Approval:   claimed,
		Decision:   req.Decision,
		TurnID:     turnID,
		Events: func(event runtime.Event) error {
			return s.emitAndPersist(r, runID, event, nil)
		},
	})
	if err != nil {
		if len(out.NewMessages) > 0 {
			_ = s.store.AppendMessages(r.Context(), runID, out.NewMessages)
		}
		if len(out.ModelLogs) > 0 {
			_ = s.appendModelLogs(r, runID, out.ModelLogs)
		}
		s.failResumedTurn(r.Context(), runID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if err := s.persistResumedOutput(r, runID, meta, history, claimed, out, turnID, executionStarted); err != nil {
		s.failResumedTurn(r.Context(), runID, err)
		writeStoreError(w, err)
		return
	}
	updated, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appendTurnResponse{
		Run:         updated,
		Messages:    toMessageOutput(out.Messages),
		NewMessages: toMessageOutput(out.NewMessages),
		Plan:        out.Plan,
	})
}

func (s *Server) persistResumedOutput(r *http.Request, runID string, meta store.RunMetadata, history []runtime.Message, claimed runtime.PendingShellApproval, out agent.TurnOutput, turnID string, executionStarted time.Time) error {
	if err := s.store.AppendMessages(r.Context(), runID, out.NewMessages); err != nil {
		return err
	}
	if err := s.appendModelLogs(r, runID, out.ModelLogs); err != nil {
		return err
	}
	if usage := totalUsage(out.Events); usage != nil {
		if _, err := s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: meta.Status, AddUsage: usage}); err != nil {
			return err
		}
	}
	if _, err := s.store.SavePlan(r.Context(), runID, out.Plan); err != nil {
		return err
	}
	if out.Compaction.Enabled {
		if err := s.store.SaveCompaction(r.Context(), runID, out.Compaction); err != nil {
			return err
		}
	}
	if out.PendingApproval != nil {
		out.PendingApproval.TurnID = turnID
	}
	if err := s.store.CompleteShellApproval(r.Context(), runID, claimed.ID, out.PendingApproval); err != nil {
		return err
	}
	if out.PendingApproval != nil {
		_, err := s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusAwaitingApproval})
		s.observeTurnDetails(runID, turnID, history, "", nil, out, runtime.TraceStatusAwaitingApproval, 4, time.Time{})
		return err
	}
	if err := s.emitAndPersist(r, runID, turnEvent("turn_completed", "Turn completed", turnID), nil); err != nil {
		return err
	}
	if _, err := s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusCompleted}); err != nil {
		return err
	}
	now := time.Now().UTC()
	s.observeTurnDetails(runID, turnID, history, "", nil, out, runtime.TraceStatusSucceeded, 4, now)
	s.observeRunSpan(runID, runtime.TraceStatusSucceeded, executionStarted, now, "")
	return nil
}

func verifyStoredShellApproval(approval runtime.PendingShellApproval) error {
	input, err := tools.DecodeShellInput(approval.ToolCall.Arguments)
	if err != nil {
		return fmt.Errorf("stored shell approval is invalid: %w", err)
	}
	hash := sha256.Sum256([]byte(input.Command))
	if fmt.Sprintf("%x", hash) != approval.ScriptHash {
		return errors.New("stored shell approval script hash does not match")
	}
	if _, err := tools.AnalyzeShellScript(input.Command); err != nil {
		return fmt.Errorf("stored shell approval is no longer valid: %w", err)
	}
	return nil
}

func (s *Server) markExecutingApprovalFailed(ctx context.Context, runID, approvalID string) {
	message := "shell approval execution was interrupted; manual recovery is required"
	_, _ = s.store.UpdateRun(ctx, runID, store.UpdateRunInput{Status: store.RunStatusFailed, Error: message})
	_ = s.appendEvent(ctx, runID, runtime.NewEvent("approval_recovery_required", message))
	_ = s.store.ClearPendingShellApproval(ctx, runID, approvalID)
}

func (s *Server) failResumedTurn(ctx context.Context, runID string, cause error) {
	_, _ = s.store.UpdateRun(ctx, runID, store.UpdateRunInput{Status: store.RunStatusFailed, Error: cause.Error()})
	_ = s.appendEvent(ctx, runID, runtime.NewEvent("turn_failed", cause.Error()))
}

func turnEvent(eventType, message, turnID string) runtime.Event {
	event := runtime.NewEvent(eventType, message)
	event.TurnID = turnID
	return event
}

func newTraceID(prefix string) string {
	bytes := make([]byte, 12)
	if _, err := cryptorand.Read(bytes); err == nil {
		return prefix + "_" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}

func rejectUnavailableRun(meta store.RunMetadata) error {
	switch meta.Status {
	case store.RunStatusRunning:
		return errors.New("run is already running")
	case store.RunStatusAwaitingApproval:
		return errors.New("run is awaiting shell approval")
	default:
		return nil
	}
}

func writeApprovalError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrApprovalConflict) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "shell approval is no longer pending"})
		return
	}
	writeStoreError(w, err)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	meta, err := s.store.GetRun(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.store.ListMessages(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListEvents(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleListModelLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.store.ListModelLogs(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	meta, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if s.traceStore == nil {
		writeJSON(w, http.StatusOK, traceResponse{Available: false, Reason: "trace is disabled or unavailable", Spans: []runtime.TraceSpan{}, Diagnostics: runtime.TraceDiagnostics{Complete: true}})
		return
	}
	spans, err := s.traceStore.ListTraceSpans(r.Context(), runID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if len(spans) == 0 {
		writeJSON(w, http.StatusOK, traceResponse{Available: false, Reason: "trace was not collected for this run", Spans: spans, Diagnostics: s.traceDiagnostics()})
		return
	}
	for i := range spans {
		if spans[i].Kind != runtime.TraceSpanRun {
			continue
		}
		switch meta.Status {
		case store.RunStatusCompleted:
			spans[i].Status = runtime.TraceStatusSucceeded
			if spans[i].CompletedAt.IsZero() {
				spans[i].CompletedAt = meta.UpdatedAt
			}
		case store.RunStatusFailed:
			spans[i].Status = runtime.TraceStatusFailed
			if spans[i].CompletedAt.IsZero() {
				spans[i].CompletedAt = meta.UpdatedAt
			}
			spans[i].Error = meta.Error
		case store.RunStatusAwaitingApproval:
			spans[i].Status = runtime.TraceStatusAwaitingApproval
		case store.RunStatusRunning:
			spans[i].Status = runtime.TraceStatusRunning
			spans[i].CompletedAt = time.Time{}
		}
	}
	total := len(spans)
	spans, filterActive := filterTraceSpans(spans, r)
	limit := traceLimit(r)
	hidden := 0
	if limit > 0 && len(spans) > limit {
		hidden = len(spans) - limit
		spans = spans[:limit]
	}
	writeJSON(w, http.StatusOK, traceResponse{Available: true, Spans: spans, Diagnostics: s.traceDiagnostics(), TotalSpans: total, HiddenSpans: hidden, FilterActive: filterActive})
}

func (s *Server) handleGetTraceSpan(w http.ResponseWriter, r *http.Request) {
	span, ok, err := s.findTraceSpan(r.Context(), r.PathValue("runID"), r.PathValue("spanID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "trace span not found"})
		return
	}
	writeJSON(w, http.StatusOK, traceSpanResponse{Span: span})
}

func (s *Server) handleCompareTrace(w http.ResponseWriter, r *http.Request) {
	current, err := s.traceComparison(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	other, err := s.traceComparison(r.Context(), r.PathValue("otherRunID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, traceCompareResponse{Current: current, Other: other})
}

func (s *Server) findTraceSpan(ctx context.Context, runID, spanID string) (runtime.TraceSpan, bool, error) {
	if s.traceStore == nil {
		return runtime.TraceSpan{}, false, nil
	}
	spans, err := s.traceStore.ListTraceSpans(ctx, runID)
	if err != nil {
		return runtime.TraceSpan{}, false, err
	}
	for _, span := range spans {
		if span.ID == spanID {
			return span, true, nil
		}
	}
	return runtime.TraceSpan{}, false, nil
}

func (s *Server) traceComparison(ctx context.Context, runID string) (traceComparison, error) {
	meta, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return traceComparison{}, err
	}
	comparison := traceComparison{RunID: runID, Status: meta.Status, TokenUsage: meta.TokenUsage, Kinds: map[string]int{}}
	if s.traceStore == nil {
		return comparison, nil
	}
	spans, err := s.traceStore.ListTraceSpans(ctx, runID)
	if err != nil {
		return traceComparison{}, err
	}
	comparison.TotalSpans = len(spans)
	for _, span := range spans {
		comparison.Kinds[string(span.Kind)]++
		if span.Status == runtime.TraceStatusFailed {
			comparison.FailedSpans++
		}
		if span.Kind == runtime.TraceSpanRun {
			comparison.DurationMS = span.DurationMS
			if comparison.DurationMS == 0 && !span.CompletedAt.IsZero() {
				comparison.DurationMS = span.CompletedAt.Sub(span.StartedAt).Milliseconds()
			}
		}
	}
	return comparison, nil
}

func (s *Server) traceDiagnostics() runtime.TraceDiagnostics {
	if provider, ok := s.traceObserver.(interface {
		Diagnostics() runtime.TraceDiagnostics
	}); ok {
		return provider.Diagnostics()
	}
	return runtime.TraceDiagnostics{Complete: true}
}

func filterTraceSpans(spans []runtime.TraceSpan, r *http.Request) ([]runtime.TraceSpan, bool) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if kind == "" && status == "" && query == "" {
		return spans, false
	}
	filtered := make([]runtime.TraceSpan, 0, len(spans))
	for _, span := range spans {
		if kind != "" && string(span.Kind) != kind && span.Component != kind {
			continue
		}
		if status != "" && string(span.Status) != status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(span.Name+" "+span.Component+" "+span.Error), query) {
			continue
		}
		filtered = append(filtered, span)
	}
	return filtered, true
}

func traceLimit(r *http.Request) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return 200
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func (s *Server) observeTrace(span runtime.TraceSpan) {
	if s.traceObserver != nil {
		attributes := make(map[string]any, len(span.Attributes)+1)
		for key, value := range span.Attributes {
			attributes[key] = value
		}
		if _, ok := attributes["runtime"]; !ok {
			attributes["runtime"] = map[string]any{
				"language":       "go",
				"component":      "gian",
				"model_provider": s.modelProvider,
			}
		}
		span.Attributes = attributes
		s.traceObserver.Observe(context.Background(), span)
	}
}

// observeRunSpan only measures one active execution slice. A chat Run can sit
// idle between turns, and that user wait time must not be reported as agent
// latency. Approval wait time is represented by its own approval span.
func (s *Server) observeRunSpan(runID string, status runtime.TraceSpanStatus, startedAt, completedAt time.Time, traceError string) {
	span := runtime.TraceSpan{
		ID:          traceRunSpanID(runID),
		RunID:       runID,
		Kind:        runtime.TraceSpanRun,
		Component:   "run",
		Name:        "Run",
		Status:      status,
		Revision:    traceRevision(),
		Sequence:    1,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		Error:       traceError,
	}
	if !completedAt.IsZero() {
		span.DurationMS = completedAt.Sub(startedAt).Milliseconds()
	}
	s.observeTrace(span)
}

// observeTurnDetails makes the Run view useful without duplicating the model
// spans. The first turn includes its system prompt; subsequent turns only need
// the new user prompt and the turn output.
func (s *Server) observeTurnDetails(runID, turnID string, history []runtime.Message, message string, attachments []runtime.Attachment, out agent.TurnOutput, status runtime.TraceSpanStatus, revision int, completedAt time.Time) {
	userPrompt := runtime.Message{
		Role:        runtime.RoleUser,
		Content:     message,
		Attachments: append([]runtime.Attachment(nil), attachments...),
		CreatedAt:   time.Now().UTC(),
	}
	if observed, ok := firstUserMessage(out.NewMessages); ok {
		userPrompt = observed
	}
	userMessages := userMessageCount(history)
	isFirstTurn := userMessages == 0
	if message == "" && len(attachments) == 0 {
		isFirstTurn = userMessages <= 1
		if previousUser, ok := latestUserMessage(history); ok {
			userPrompt = previousUser
		}
	}

	input := map[string]any{"user_prompt": userPrompt}
	if isFirstTurn && len(out.ModelLogs) > 0 {
		input["system_prompt"] = out.ModelLogs[0].Request.SystemPrompt
	}
	output := turnOutputMessages(out.NewMessages)
	inputJSON, _ := json.Marshal(input)
	outputJSON, _ := json.Marshal(output)

	startedAt := time.Now().UTC()
	if len(out.ModelLogs) > 0 && !out.ModelLogs[0].StartedAt.IsZero() {
		startedAt = out.ModelLogs[0].StartedAt
	}
	span := runtime.TraceSpan{
		ID:        traceTurnSpanID(turnID),
		ParentID:  traceRunSpanID(runID),
		RunID:     runID,
		TurnID:    turnID,
		Kind:      runtime.TraceSpanTurn,
		Component: "graph",
		Name:      "Turn",
		Status:    status,
		Revision:  revision,
		Sequence:  2,
		StartedAt: startedAt,
		Input:     inputJSON,
		Output:    outputJSON,
	}
	if !completedAt.IsZero() {
		span.CompletedAt = completedAt
		span.DurationMS = completedAt.Sub(startedAt).Milliseconds()
	}
	s.observeTrace(span)
}

func userMessageCount(messages []runtime.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == runtime.RoleUser {
			count++
		}
	}
	return count
}

func latestUserMessage(messages []runtime.Message) (runtime.Message, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == runtime.RoleUser {
			return messages[index], true
		}
	}
	return runtime.Message{}, false
}

func firstUserMessage(messages []runtime.Message) (runtime.Message, bool) {
	for _, message := range messages {
		if message.Role == runtime.RoleUser {
			return message, true
		}
	}
	return runtime.Message{}, false
}

func turnOutputMessages(messages []runtime.Message) []runtime.Message {
	output := make([]runtime.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role != runtime.RoleUser {
			output = append(output, message)
		}
	}
	return output
}

func (s *Server) observeEvent(event runtime.Event) {
	if s.traceObserver == nil || event.RunID == "" {
		return
	}
	now := event.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	span := runtime.TraceSpan{RunID: event.RunID, TurnID: event.TurnID, StartedAt: now, Sequence: traceSequence(event), Revision: 1, Status: runtime.TraceStatusRunning}
	switch event.Type {
	case "turn_started":
		span.ID, span.ParentID, span.Kind, span.Name = traceTurnSpanID(event.TurnID), traceRunSpanID(event.RunID), runtime.TraceSpanTurn, "Turn"
	case "turn_completed":
		span.ID, span.ParentID, span.Kind, span.Name, span.Status, span.Revision, span.CompletedAt = traceTurnSpanID(event.TurnID), traceRunSpanID(event.RunID), runtime.TraceSpanTurn, "Turn", runtime.TraceStatusSucceeded, 2, now
	case "turn_failed":
		span.ID, span.ParentID, span.Kind, span.Name, span.Status, span.Revision, span.CompletedAt, span.Error = traceTurnSpanID(event.TurnID), traceRunSpanID(event.RunID), runtime.TraceSpanTurn, "Turn", runtime.TraceStatusFailed, 2, now, event.Message
	default:
		return
	}
	s.observeTrace(span)
}

func (s *Server) observeModelLog(runID string, modelLog runtime.ModelLog) {
	if modelLog.CallID == "" {
		return
	}
	input, _ := json.Marshal(modelLog.Request)
	output, _ := json.Marshal(modelLog.Response)
	turnID := traceTurnIDFromModelCall(modelLog.CallID)
	span := runtime.TraceSpan{ID: traceModelSpanID(modelLog.CallID), ParentID: traceTurnSpanID(turnID), RunID: runID, TurnID: turnID, Kind: runtime.TraceSpanModel, Name: "Model", Status: runtime.TraceStatusSucceeded, Revision: 3, Sequence: modelLog.Index * 10, StartedAt: modelLog.StartedAt, CompletedAt: modelLog.CompletedAt, Input: input, Output: output, Error: modelLog.Error, Usage: modelLog.Response.Usage}
	if modelLog.Error != "" {
		span.Status = runtime.TraceStatusFailed
	}
	s.observeTrace(span)
}

func (s *Server) observeToolMessages(runID, turnID string, messages []runtime.Message) {
	for _, message := range messages {
		if message.Role == runtime.RoleAssistant {
			for _, call := range message.ToolCalls {
				s.observeTrace(runtime.TraceSpan{ID: traceToolSpanID(call.ID), ParentID: traceTurnSpanID(turnID), RunID: runID, TurnID: turnID, Kind: runtime.TraceSpanTool, Name: call.Name, Status: runtime.TraceStatusRunning, Revision: 1, Sequence: 20, StartedAt: message.CreatedAt, Input: call.Arguments})
			}
		}
		if message.Role != runtime.RoleTool || message.ToolCallID == "" {
			continue
		}
		output, _ := json.Marshal(message)
		s.observeTrace(runtime.TraceSpan{ID: traceToolSpanID(message.ToolCallID), ParentID: traceTurnSpanID(turnID), RunID: runID, TurnID: turnID, Kind: runtime.TraceSpanTool, Name: message.Name, Status: runtime.TraceStatusSucceeded, Revision: 3, Sequence: 90, StartedAt: message.CreatedAt, CompletedAt: message.CreatedAt, Output: output})
	}
}

func traceRunSpanID(runID string) string       { return "trace_run_" + runID }
func traceTurnSpanID(turnID string) string     { return "trace_turn_" + turnID }
func traceModelSpanID(callID string) string    { return "trace_model_" + callID }
func traceToolSpanID(callID string) string     { return "trace_tool_" + callID }
func traceApprovalSpanID(callID string) string { return "trace_approval_" + callID }

func traceRevision() int { return int(time.Now().UTC().UnixNano()) }

func traceTurnIDFromModelCall(callID string) string {
	if index := strings.LastIndex(callID, "_model_"); index > 0 {
		return callID[:index]
	}
	return ""
}

func traceSequence(event runtime.Event) int {
	switch event.Type {
	case "turn_started", "turn_completed", "turn_failed":
		return 2
	case "model_started", "model_completed":
		return 10
	case "tool_started", "tool_completed", "approval_required", "approval_approved", "approval_denied", "approval_cancelled":
		return 20
	default:
		return 99
	}
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.store.GetPlan(r.Context(), r.PathValue("runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleSetPlanMode(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if meta, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeStoreError(w, err)
		return
	} else if err := rejectUnavailableRun(meta); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	var req setPlanModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON request body"})
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if req.Enabled {
		plan.Mode = runtime.PlanModePlanning
	} else {
		plan.Mode = runtime.PlanModeOff
	}
	meta, err := s.store.SavePlan(r.Context(), runID, plan)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	eventType := "plan_mode_disabled"
	message := "Plan mode disabled"
	if req.Enabled {
		eventType = "plan_mode_enabled"
		message = "Plan mode enabled"
	}
	if err := s.appendEvent(r.Context(), runID, runtime.NewEvent(eventType, message)); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Run  store.RunMetadata `json:"run"`
		Plan runtime.PlanState `json:"plan"`
	}{Run: meta, Plan: plan})
}

func (s *Server) handleExecutePlan(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if meta, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeStoreError(w, err)
		return
	} else if err := rejectUnavailableRun(meta); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(plan.Steps) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "plan has no steps to execute"})
		return
	}
	plan.Mode = runtime.PlanModeExecuting
	meta, err := s.store.SavePlan(r.Context(), runID, plan)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.appendEvent(r.Context(), runID, runtime.NewEvent("plan_execution_started", "Plan execution started")); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Run  store.RunMetadata `json:"run"`
		Plan runtime.PlanState `json:"plan"`
	}{Run: meta, Plan: plan})
}

func (s *Server) handleResetPlan(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if meta, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeStoreError(w, err)
		return
	} else if err := rejectUnavailableRun(meta); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	meta, err := s.store.SavePlan(r.Context(), runID, runtime.EmptyPlanState())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	plan, err := s.store.GetPlan(r.Context(), runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.appendEvent(r.Context(), runID, runtime.NewEvent("plan_reset", "Plan reset")); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Run  store.RunMetadata `json:"run"`
		Plan runtime.PlanState `json:"plan"`
	}{Run: meta, Plan: plan})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrRunNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "run not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
}

func toMessageOutput(messages []runtime.Message) []messageOutput {
	output := make([]messageOutput, 0, len(messages))
	for _, msg := range messages {
		output = append(output, messageOutput{
			Role:        string(msg.Role),
			Content:     msg.Content,
			ToolCallID:  msg.ToolCallID,
			Name:        msg.Name,
			ToolCalls:   msg.ToolCalls,
			Attachments: msg.Attachments,
			CreatedAt:   msg.CreatedAt,
			Usage:       msg.Usage,
		})
	}
	return output
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type sseStream struct {
	writer  *bufio.Writer
	flusher http.Flusher
}

func newSSEStream(w http.ResponseWriter) (*sseStream, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	return &sseStream{
		writer:  bufio.NewWriter(w),
		flusher: flusher,
	}, true
}

func (s *sseStream) write(event runtime.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.ID != "" {
		if _, err := fmt.Fprintf(s.writer, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
		return err
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
