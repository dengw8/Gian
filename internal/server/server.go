package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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
	ModelProvider string
}

type Server struct {
	runner        *agent.Runner
	store         store.RunStore
	modelProvider string
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
	Approval runtime.PendingShellApproval `json:"approval"`
}

type createRunResponse struct {
	Run      store.RunMetadata `json:"run"`
	Messages []messageOutput   `json:"messages"`
	Plan     runtime.PlanState `json:"plan"`
}

type listRunsResponse struct {
	Runs []store.RunMetadata `json:"runs"`
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
	mux.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.FS(appFS))))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/runs", s.handleListRuns)
	mux.HandleFunc("POST /v1/runs", s.handleCreateRun)
	mux.HandleFunc("GET /v1/runs/{runID}", s.handleGetRun)
	mux.HandleFunc("GET /v1/runs/{runID}/messages", s.handleListMessages)
	mux.HandleFunc("GET /v1/runs/{runID}/events", s.handleListEvents)
	mux.HandleFunc("GET /v1/runs/{runID}/model-logs", s.handleListModelLogs)
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
	if err := s.store.AppendEvents(r.Context(), meta.ID, []runtime.Event{runtime.NewEvent("run_created", "Run created")}); err != nil {
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
		if err := s.store.AppendEvents(r.Context(), meta.ID, []runtime.Event{runtime.NewEvent("plan_mode_enabled", "Plan mode enabled")}); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}
	}

	message := strings.TrimSpace(req.Message)
	if message == "" && len(req.Attachments) == 0 {
		writeJSON(w, http.StatusCreated, createRunResponse{Run: meta, Plan: plan})
		return
	}

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, nil, plan, runtime.EmptyCompactionState(), nil)
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

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, history, plan, compaction, nil)
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

	_, _, err = s.executeTurn(r, meta, message, req.Attachments, history, plan, compaction, stream.write)
	if err != nil {
		return
	}
}

func (s *Server) executeTurn(r *http.Request, meta store.RunMetadata, message string, attachments []runtime.Attachment, history []runtime.Message, plan runtime.PlanState, compaction runtime.CompactionState, sink agent.EventSink) (agent.TurnOutput, store.RunMetadata, error) {
	if s.runner == nil {
		return agent.TurnOutput{}, meta, errors.New("agent runner is not configured")
	}

	updated, err := s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusRunning})
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
	meta = updated

	if err := s.emitAndPersist(r, meta.ID, runtime.NewEvent("turn_started", "Turn started"), sink); err != nil {
		return agent.TurnOutput{}, meta, err
	}

	out, err := s.runner.RunTurn(r.Context(), agent.TurnInput{
		History:     history,
		Message:     message,
		Attachments: attachments,
		Plan:        plan,
		Compaction:  compaction,
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
		_ = s.emitAndPersist(r, meta.ID, runtime.NewEvent("turn_failed", err.Error()), sink)
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
		if err := s.store.SavePendingShellApproval(r.Context(), meta.ID, *out.PendingApproval); err != nil {
			return agent.TurnOutput{}, meta, err
		}
		meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusAwaitingApproval})
		if err != nil {
			return agent.TurnOutput{}, meta, err
		}
		return out, meta, nil
	}
	if err := s.emitAndPersist(r, meta.ID, runtime.NewEvent("turn_completed", "Turn completed"), sink); err != nil {
		return agent.TurnOutput{}, meta, err
	}
	meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusCompleted})
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
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
	if sink != nil {
		if err := sink(event); err != nil {
			return err
		}
	}
	return s.store.AppendEvents(r.Context(), runID, []runtime.Event{event})
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
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no pending shell approval"})
		return
	}
	if approval.Status == runtime.ShellApprovalExecuting {
		s.markExecutingApprovalFailed(r.Context(), runID, approval.ID)
		writeJSON(w, http.StatusConflict, errorResponse{Error: "shell approval execution was interrupted; manual recovery is required"})
		return
	}
	writeJSON(w, http.StatusOK, pendingApprovalResponse{Approval: *approval})
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
	meta, err = s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusRunning})
	if err != nil {
		writeStoreError(w, err)
		return
	}
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
	out, err := s.runner.ResumeTurn(r.Context(), agent.ResumeInput{
		History:    history,
		Plan:       plan,
		Compaction: compaction,
		Approval:   claimed,
		Decision:   req.Decision,
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
	if err := s.persistResumedOutput(r, runID, meta, claimed, out); err != nil {
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

func (s *Server) persistResumedOutput(r *http.Request, runID string, meta store.RunMetadata, claimed runtime.PendingShellApproval, out agent.TurnOutput) error {
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
	if err := s.store.CompleteShellApproval(r.Context(), runID, claimed.ID, out.PendingApproval); err != nil {
		return err
	}
	if out.PendingApproval != nil {
		_, err := s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusAwaitingApproval})
		return err
	}
	if err := s.store.AppendEvents(r.Context(), runID, []runtime.Event{runtime.NewEvent("turn_completed", "Turn completed")}); err != nil {
		return err
	}
	_, err := s.store.UpdateRun(r.Context(), runID, store.UpdateRunInput{Status: store.RunStatusCompleted})
	return err
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
	_ = s.store.AppendEvents(ctx, runID, []runtime.Event{runtime.NewEvent("approval_recovery_required", message)})
	_ = s.store.ClearPendingShellApproval(ctx, runID, approvalID)
}

func (s *Server) failResumedTurn(ctx context.Context, runID string, cause error) {
	_, _ = s.store.UpdateRun(ctx, runID, store.UpdateRunInput{Status: store.RunStatusFailed, Error: cause.Error()})
	_ = s.store.AppendEvents(ctx, runID, []runtime.Event{runtime.NewEvent("turn_failed", cause.Error())})
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
	if err := s.store.AppendEvents(r.Context(), runID, []runtime.Event{runtime.NewEvent(eventType, message)}); err != nil {
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
	if err := s.store.AppendEvents(r.Context(), runID, []runtime.Event{runtime.NewEvent("plan_execution_started", "Plan execution started")}); err != nil {
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
	if err := s.store.AppendEvents(r.Context(), runID, []runtime.Event{runtime.NewEvent("plan_reset", "Plan reset")}); err != nil {
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
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
		return err
	}
	if err := s.writer.Flush(); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
