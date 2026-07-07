package server

import (
	"bufio"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/dengw8/harness-practice/internal/agent"
	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
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
	mux.HandleFunc("GET /v1/runs/{runID}/plan", s.handleGetPlan)
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

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, nil, plan, nil)
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
	if meta.Status == store.RunStatusRunning {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "run is already running"})
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

	out, meta, err := s.executeTurn(r, meta, message, req.Attachments, history, plan, nil)
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
	if meta.Status == store.RunStatusRunning {
		stream.write(runtime.NewEvent("turn_failed", "run is already running"))
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

	_, _, err = s.executeTurn(r, meta, message, req.Attachments, history, plan, stream.write)
	if err != nil {
		return
	}
}

func (s *Server) executeTurn(r *http.Request, meta store.RunMetadata, message string, attachments []runtime.Attachment, history []runtime.Message, plan runtime.PlanState, sink agent.EventSink) (agent.TurnOutput, store.RunMetadata, error) {
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
		Events: func(event runtime.Event) error {
			return s.emitAndPersist(r, meta.ID, event, sink)
		},
	})
	if err != nil {
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
	if err := s.emitAndPersist(r, meta.ID, runtime.NewEvent("turn_completed", "Turn completed"), sink); err != nil {
		return agent.TurnOutput{}, meta, err
	}
	meta, err = s.store.UpdateRun(r.Context(), meta.ID, store.UpdateRunInput{Status: store.RunStatusCompleted})
	if err != nil {
		return agent.TurnOutput{}, meta, err
	}
	return out, meta, nil
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
