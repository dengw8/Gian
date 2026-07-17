package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
)

const runsDir = "runs"

type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) *Store {
	return &Store{root: root}
}

func (s *Store) CreateRun(ctx context.Context, input store.CreateRunInput) (store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return store.RunMetadata{}, err
	}

	id, err := newRunID()
	if err != nil {
		return store.RunMetadata{}, err
	}
	now := time.Now().UTC()
	meta := store.RunMetadata{
		ID:            id,
		Status:        store.RunStatusCreated,
		CreatedAt:     now,
		UpdatedAt:     now,
		ModelProvider: input.ModelProvider,
	}

	if err := os.MkdirAll(s.runDir(id), 0o755); err != nil {
		return store.RunMetadata{}, err
	}
	if err := s.writeMetadata(meta); err != nil {
		return store.RunMetadata{}, err
	}
	return meta, nil
}

func (s *Store) ListRuns(ctx context.Context) ([]store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(s.root, runsDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []store.RunMetadata{}, nil
		}
		return nil, err
	}

	runs := make([]store.RunMetadata, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.GetRun(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		runs = append(runs, meta)
	}

	sort.Slice(runs, func(i, j int) bool {
		if runs[i].UpdatedAt.Equal(runs[j].UpdatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
	return runs, nil
}

func (s *Store) GetRun(ctx context.Context, runID string) (store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return store.RunMetadata{}, err
	}
	if err := validateRunID(runID); err != nil {
		return store.RunMetadata{}, err
	}

	data, err := os.ReadFile(s.metadataPath(runID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store.RunMetadata{}, store.ErrRunNotFound
		}
		return store.RunMetadata{}, err
	}
	var meta store.RunMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return store.RunMetadata{}, err
	}
	return meta, nil
}

func (s *Store) UpdateRun(ctx context.Context, runID string, input store.UpdateRunInput) (store.RunMetadata, error) {
	meta, err := s.GetRun(ctx, runID)
	if err != nil {
		return store.RunMetadata{}, err
	}
	meta.Status = input.Status
	meta.Error = input.Error
	meta.TokenUsage = addUsage(meta.TokenUsage, input.AddUsage)
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetadata(meta); err != nil {
		return store.RunMetadata{}, err
	}
	return meta, nil
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

func (s *Store) GetPlan(ctx context.Context, runID string) (runtime.PlanState, error) {
	if err := ctx.Err(); err != nil {
		return runtime.PlanState{}, err
	}
	if err := validateRunID(runID); err != nil {
		return runtime.PlanState{}, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return runtime.PlanState{}, err
	}

	data, err := os.ReadFile(s.planPath(runID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtime.EmptyPlanState(), nil
		}
		return runtime.PlanState{}, err
	}
	var plan runtime.PlanState
	if err := json.Unmarshal(data, &plan); err != nil {
		return runtime.PlanState{}, err
	}
	if plan.Mode == "" {
		plan.Mode = runtime.PlanModeOff
	}
	return plan, nil
}

func (s *Store) SavePlan(ctx context.Context, runID string, plan runtime.PlanState) (store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return store.RunMetadata{}, err
	}
	if err := validateRunID(runID); err != nil {
		return store.RunMetadata{}, err
	}
	meta, err := s.GetRun(ctx, runID)
	if err != nil {
		return store.RunMetadata{}, err
	}
	if plan.Mode == "" {
		plan.Mode = runtime.PlanModeOff
	}
	now := time.Now().UTC()
	if plan.Mode != runtime.PlanModeOff && plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now

	if err := s.writePlan(runID, plan); err != nil {
		return store.RunMetadata{}, err
	}
	meta.PlanMode = string(plan.Mode)
	meta.PlanSteps = len(plan.Steps)
	meta.PlanCompleted = plan.CompletedCount()
	meta.UpdatedAt = now
	if err := s.writeMetadata(meta); err != nil {
		return store.RunMetadata{}, err
	}
	return meta, nil
}

func (s *Store) GetCompaction(ctx context.Context, runID string) (runtime.CompactionState, error) {
	if err := ctx.Err(); err != nil {
		return runtime.CompactionState{}, err
	}
	if err := validateRunID(runID); err != nil {
		return runtime.CompactionState{}, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return runtime.CompactionState{}, err
	}

	data, err := os.ReadFile(s.compactionPath(runID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtime.EmptyCompactionState(), nil
		}
		return runtime.CompactionState{}, err
	}
	var state runtime.CompactionState
	if err := json.Unmarshal(data, &state); err != nil {
		return runtime.CompactionState{}, err
	}
	return state, nil
}

func (s *Store) SaveCompaction(ctx context.Context, runID string, state runtime.CompactionState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	if !state.UpdatedAt.IsZero() {
		state.UpdatedAt = state.UpdatedAt.UTC()
	}
	return s.writeCompaction(runID, state)
}

func (s *Store) GetPendingShellApproval(ctx context.Context, runID string) (*runtime.PendingShellApproval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.pendingApprovalPath(runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var approval runtime.PendingShellApproval
	if err := json.Unmarshal(data, &approval); err != nil {
		return nil, err
	}
	return &approval, nil
}

func (s *Store) SavePendingShellApproval(ctx context.Context, runID string, approval runtime.PendingShellApproval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	if existing, err := s.readPendingShellApproval(runID); err != nil {
		return err
	} else if existing != nil {
		return store.ErrApprovalConflict
	}
	if approval.Status != runtime.ShellApprovalPending {
		return fmt.Errorf("new shell approval must be pending")
	}
	return s.writePendingShellApproval(runID, approval)
}

func (s *Store) ClaimPendingShellApproval(ctx context.Context, runID, approvalID string, version int) (runtime.PendingShellApproval, error) {
	if err := ctx.Err(); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	if err := validateRunID(runID); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetRun(ctx, runID); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	approval, err := s.readPendingShellApproval(runID)
	if err != nil {
		return runtime.PendingShellApproval{}, err
	}
	if approval == nil || approval.ID != approvalID || approval.Version != version || approval.Status != runtime.ShellApprovalPending {
		return runtime.PendingShellApproval{}, store.ErrApprovalConflict
	}
	approval.Status = runtime.ShellApprovalExecuting
	approval.Version++
	if err := s.writePendingShellApproval(runID, *approval); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	return *approval, nil
}

func (s *Store) ClearPendingShellApproval(ctx context.Context, runID, approvalID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	approval, err := s.readPendingShellApproval(runID)
	if err != nil {
		return err
	}
	if approval == nil || approval.ID != approvalID {
		return store.ErrApprovalConflict
	}
	if err := os.Remove(s.pendingApprovalPath(runID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) CompleteShellApproval(ctx context.Context, runID, executingApprovalID string, next *runtime.PendingShellApproval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	approval, err := s.readPendingShellApproval(runID)
	if err != nil {
		return err
	}
	if approval == nil || approval.ID != executingApprovalID || approval.Status != runtime.ShellApprovalExecuting {
		return store.ErrApprovalConflict
	}
	if next != nil {
		if next.Status != runtime.ShellApprovalPending {
			return fmt.Errorf("replacement shell approval must be pending")
		}
		return s.writePendingShellApproval(runID, *next)
	}
	if err := os.Remove(s.pendingApprovalPath(runID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) AppendMessages(ctx context.Context, runID string, messages []runtime.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	return appendJSONLines(s.messagesPath(runID), messages)
}

func (s *Store) ListMessages(ctx context.Context, runID string) ([]runtime.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	return readJSONLines[runtime.Message](s.messagesPath(runID))
}

func (s *Store) AppendEvents(ctx context.Context, runID string, events []runtime.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	return appendJSONLines(s.eventsPath(runID), events)
}

func (s *Store) ListEvents(ctx context.Context, runID string) ([]runtime.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	return readJSONLines[runtime.Event](s.eventsPath(runID))
}

func (s *Store) AppendExecutionEvent(ctx context.Context, event runtime.ExecutionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(event.RunID); err != nil {
		return err
	}
	if event.ID == "" || event.Sequence <= 0 {
		return errors.New("execution event id and sequence are required")
	}
	if _, err := s.GetRun(ctx, event.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := readJSONLines[runtime.ExecutionEvent](s.executionEventsPath(event.RunID))
	if err != nil {
		return err
	}
	for _, existing := range events {
		if existing.Sequence == event.Sequence {
			return nil
		}
	}
	return appendJSONLines(s.executionEventsPath(event.RunID), []runtime.ExecutionEvent{event})
}

func (s *Store) ListExecutionEvents(ctx context.Context, runID string, afterSequence int64, limit int) ([]runtime.ExecutionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	events, err := readJSONLines[runtime.ExecutionEvent](s.executionEventsPath(runID))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	filtered := make([]runtime.ExecutionEvent, 0, len(events))
	for _, event := range events {
		if event.Sequence > afterSequence {
			filtered = append(filtered, event)
			if limit > 0 && len(filtered) >= limit {
				break
			}
		}
	}
	return filtered, nil
}

func (s *Store) LastExecutionEventSequence(ctx context.Context, runID string) (int64, error) {
	events, err := s.ListExecutionEvents(ctx, runID, 0, 0)
	if err != nil {
		return 0, err
	}
	var sequence int64
	for _, event := range events {
		if event.Sequence > sequence {
			sequence = event.Sequence
		}
	}
	return sequence, nil
}

func (s *Store) AppendModelLogs(ctx context.Context, runID string, logs []runtime.ModelLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return err
	}
	return appendJSONLines(s.modelLogsPath(runID), logs)
}

func (s *Store) ListModelLogs(ctx context.Context, runID string) ([]runtime.ModelLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	return readJSONLines[runtime.ModelLog](s.modelLogsPath(runID))
}

func (s *Store) UpsertTraceSpan(ctx context.Context, span runtime.TraceSpan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(span.RunID); err != nil {
		return err
	}
	if span.ID == "" {
		return errors.New("trace span id is required")
	}
	if _, err := s.GetRun(ctx, span.RunID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendJSONLines(s.tracePath(span.RunID), []runtime.TraceSpan{span})
}

func (s *Store) ListTraceSpans(ctx context.Context, runID string) ([]runtime.TraceSpan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRunID(runID); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	values, err := readJSONLines[runtime.TraceSpan](s.tracePath(runID))
	if err != nil {
		return nil, err
	}
	latest := make(map[string]runtime.TraceSpan, len(values))
	for _, span := range values {
		previous, ok := latest[span.ID]
		if !ok || span.Revision >= previous.Revision {
			latest[span.ID] = span
		}
	}
	spans := make([]runtime.TraceSpan, 0, len(latest))
	for _, span := range latest {
		spans = append(spans, span)
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Sequence == spans[j].Sequence {
			return spans[i].StartedAt.Before(spans[j].StartedAt)
		}
		return spans[i].Sequence < spans[j].Sequence
	})
	return spans, nil
}

func (s *Store) runDir(runID string) string {
	return filepath.Join(s.root, runsDir, runID)
}

func (s *Store) metadataPath(runID string) string {
	return filepath.Join(s.runDir(runID), "metadata.json")
}

func (s *Store) messagesPath(runID string) string {
	return filepath.Join(s.runDir(runID), "messages.jsonl")
}

func (s *Store) eventsPath(runID string) string {
	return filepath.Join(s.runDir(runID), "events.jsonl")
}

func (s *Store) executionEventsPath(runID string) string {
	return filepath.Join(s.runDir(runID), "execution_events.jsonl")
}

func (s *Store) modelLogsPath(runID string) string {
	return filepath.Join(s.runDir(runID), "model_logs.jsonl")
}

func (s *Store) tracePath(runID string) string {
	return filepath.Join(s.runDir(runID), "trace.jsonl")
}

func (s *Store) planPath(runID string) string {
	return filepath.Join(s.runDir(runID), "plan.json")
}

func (s *Store) compactionPath(runID string) string {
	return filepath.Join(s.runDir(runID), "compaction.json")
}

func (s *Store) pendingApprovalPath(runID string) string {
	return filepath.Join(s.runDir(runID), "pending_approval.json")
}

func (s *Store) writeMetadata(meta store.RunMetadata) error {
	if err := os.MkdirAll(s.runDir(meta.ID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.metadataPath(meta.ID), data, 0o644)
}

func (s *Store) writePlan(runID string, plan runtime.PlanState) error {
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.planPath(runID), data, 0o644)
}

func (s *Store) writeCompaction(runID string, state runtime.CompactionState) error {
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.compactionPath(runID), data, 0o644)
}

func (s *Store) readPendingShellApproval(runID string) (*runtime.PendingShellApproval, error) {
	data, err := os.ReadFile(s.pendingApprovalPath(runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var approval runtime.PendingShellApproval
	if err := json.Unmarshal(data, &approval); err != nil {
		return nil, err
	}
	return &approval, nil
}

func (s *Store) writePendingShellApproval(runID string, approval runtime.PendingShellApproval) error {
	if err := os.MkdirAll(s.runDir(runID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(approval, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(s.runDir(runID), "pending_approval-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, s.pendingApprovalPath(runID))
}

func appendJSONLines[T any](path string, values []T) error {
	if len(values) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return nil
}

func readJSONLines[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []T{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var values []T
	decoder := json.NewDecoder(file)
	for {
		var value T
		if err := decoder.Decode(&value); err != nil {
			if errors.Is(err, io.EOF) {
				return values, nil
			}
			return nil, err
		}
		values = append(values, value)
	}
}

func newRunID() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(bytes[:]), nil
}

func validateRunID(runID string) error {
	if runID == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.Contains(runID, "/") || strings.Contains(runID, string(filepath.Separator)) || strings.Contains(runID, "..") {
		return fmt.Errorf("invalid run id")
	}
	return nil
}
