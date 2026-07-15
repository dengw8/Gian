package mysql

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	logs "code.byted.org/gopkg/logs/v2"
	"code.byted.org/gorm/bytedgorm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/store"
)

const (
	maxIdleConnections = 5
	maxOpenConnections = 10
)

var requiredTables = []string{
	"agent_runs",
	"agent_run_messages",
	"agent_run_events",
	"agent_model_logs",
	"agent_run_states",
}

type Config struct {
	PSM      string
	Host     string
	Port     int
	Database string
	Username string
	Password string
}

type Store struct {
	db *gorm.DB
}

func New(ctx context.Context, cfg Config) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	// bytedgorm logs its complete DSN at debug level during initialization.
	// The DSN includes the configured password, so suppress dependency logs below
	// fatal before opening the connection.
	logs.SetLevel(logs.FatalLevel)

	dialector := bytedgorm.MySQL(cfg.PSM, cfg.Database).With(func(dbConfig *bytedgorm.DBConfig) {
		dbConfig.User = cfg.Username
		dbConfig.Password = cfg.Password
		dbConfig.DBHostname = cfg.Host
		dbConfig.DBPort = strconv.Itoa(cfg.Port)
		dbConfig.Loc = "UTC"
		dbConfig.DSNParams.Set("charset", "utf8mb4")
	})
	db, err := gorm.Open(dialector,
		bytedgorm.ConnPool{
			ConnMaxIdleTime: 5 * time.Minute,
			ConnMaxLifetime: 5 * time.Minute,
			MaxIdleConns:    maxIdleConnections,
			MaxOpenConns:    maxOpenConnections,
		},
		bytedgorm.Logger{
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		},
	)
	if err != nil {
		return nil, errors.New("open mysql database")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get mysql database handle: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping mysql database: %w", err)
	}

	s := &Store{db: db}
	if err := s.verifySchema(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return s, nil
}

func (c Config) validate() error {
	for name, value := range map[string]string{
		"psm":      c.PSM,
		"host":     c.Host,
		"database": c.Database,
		"username": c.Username,
		"password": c.Password,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("mysql %s is required", name)
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("mysql port must be between 1 and 65535")
	}
	return nil
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("get mysql database handle: %w", err)
	}
	return sqlDB.Close()
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
	record, err := runRecordFromMetadata(meta)
	if err != nil {
		return store.RunMetadata{}, err
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return store.RunMetadata{}, err
	}
	return meta, nil
}

func (s *Store) ListRuns(ctx context.Context) ([]store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var records []runRecord
	if err := s.db.WithContext(ctx).Order("updated_at DESC, id DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	runs := make([]store.RunMetadata, 0, len(records))
	for _, record := range records {
		meta, err := metadataFromRunRecord(record)
		if err != nil {
			return nil, err
		}
		runs = append(runs, meta)
	}
	return runs, nil
}

func (s *Store) GetRun(ctx context.Context, runID string) (store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return store.RunMetadata{}, err
	}
	if err := validateRunID(runID); err != nil {
		return store.RunMetadata{}, err
	}
	record, err := s.findRun(ctx, runID)
	if err != nil {
		return store.RunMetadata{}, err
	}
	return metadataFromRunRecord(record)
}

func (s *Store) UpdateRun(ctx context.Context, runID string, input store.UpdateRunInput) (store.RunMetadata, error) {
	if err := ctx.Err(); err != nil {
		return store.RunMetadata{}, err
	}
	if err := validateRunID(runID); err != nil {
		return store.RunMetadata{}, err
	}

	var updated store.RunMetadata
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockRun(tx, runID)
		if err != nil {
			return err
		}
		meta, err := metadataFromRunRecord(record)
		if err != nil {
			return err
		}
		meta.Status = input.Status
		meta.Error = input.Error
		meta.TokenUsage = addUsage(meta.TokenUsage, input.AddUsage)
		meta.UpdatedAt = time.Now().UTC()

		record, err = runRecordFromMetadata(meta)
		if err != nil {
			return err
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		updated = meta
		return nil
	})
	if err != nil {
		return store.RunMetadata{}, err
	}
	return updated, nil
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

	state, err := s.findState(ctx, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runtime.EmptyPlanState(), nil
	}
	if err != nil {
		return runtime.PlanState{}, err
	}
	if state.PlanPayload == nil {
		return runtime.EmptyPlanState(), nil
	}
	var plan runtime.PlanState
	if err := json.Unmarshal([]byte(*state.PlanPayload), &plan); err != nil {
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
	if plan.Mode == "" {
		plan.Mode = runtime.PlanModeOff
	}
	now := time.Now().UTC()
	if plan.Mode != runtime.PlanModeOff && plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now
	payload, err := marshalValue(plan)
	if err != nil {
		return store.RunMetadata{}, err
	}

	var updated store.RunMetadata
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockRun(tx, runID)
		if err != nil {
			return err
		}
		if err := upsertPlan(tx, runID, payload); err != nil {
			return err
		}
		record.PlanMode = string(plan.Mode)
		record.PlanSteps = len(plan.Steps)
		record.PlanCompleted = plan.CompletedCount()
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		updated, err = metadataFromRunRecord(record)
		return err
	})
	if err != nil {
		return store.RunMetadata{}, err
	}
	return updated, nil
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

	state, err := s.findState(ctx, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runtime.EmptyCompactionState(), nil
	}
	if err != nil {
		return runtime.CompactionState{}, err
	}
	if state.CompactionPayload == nil {
		return runtime.EmptyCompactionState(), nil
	}
	var compaction runtime.CompactionState
	if err := json.Unmarshal([]byte(*state.CompactionPayload), &compaction); err != nil {
		return runtime.CompactionState{}, err
	}
	return compaction, nil
}

func (s *Store) SaveCompaction(ctx context.Context, runID string, state runtime.CompactionState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	if !state.UpdatedAt.IsZero() {
		state.UpdatedAt = state.UpdatedAt.UTC()
	}
	payload, err := marshalValue(state)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRun(tx, runID); err != nil {
			return err
		}
		return upsertCompaction(tx, runID, payload)
	})
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
	state, err := s.findState(ctx, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) || state.PendingApprovalPayload == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var approval runtime.PendingShellApproval
	if err := json.Unmarshal([]byte(*state.PendingApprovalPayload), &approval); err != nil {
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
	if approval.Status != runtime.ShellApprovalPending {
		return fmt.Errorf("new shell approval must be pending")
	}
	payload, err := marshalValue(approval)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRun(tx, runID); err != nil {
			return err
		}
		state, err := findStateForUpdate(tx, runID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && state.PendingApprovalPayload != nil {
			return store.ErrApprovalConflict
		}
		return upsertPendingApproval(tx, runID, &payload)
	})
}

func (s *Store) ClaimPendingShellApproval(ctx context.Context, runID, approvalID string, version int) (runtime.PendingShellApproval, error) {
	if err := ctx.Err(); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	if err := validateRunID(runID); err != nil {
		return runtime.PendingShellApproval{}, err
	}
	var claimed runtime.PendingShellApproval
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRun(tx, runID); err != nil {
			return err
		}
		state, err := findStateForUpdate(tx, runID)
		if errors.Is(err, gorm.ErrRecordNotFound) || state.PendingApprovalPayload == nil {
			return store.ErrApprovalConflict
		}
		if err != nil {
			return err
		}
		var approval runtime.PendingShellApproval
		if err := json.Unmarshal([]byte(*state.PendingApprovalPayload), &approval); err != nil {
			return err
		}
		if approval.ID != approvalID || approval.Version != version || approval.Status != runtime.ShellApprovalPending {
			return store.ErrApprovalConflict
		}
		approval.Status = runtime.ShellApprovalExecuting
		approval.Version++
		payload, err := marshalValue(approval)
		if err != nil {
			return err
		}
		if err := upsertPendingApproval(tx, runID, &payload); err != nil {
			return err
		}
		claimed = approval
		return nil
	})
	if err != nil {
		return runtime.PendingShellApproval{}, err
	}
	return claimed, nil
}

func (s *Store) ClearPendingShellApproval(ctx context.Context, runID, approvalID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRun(tx, runID); err != nil {
			return err
		}
		state, err := findStateForUpdate(tx, runID)
		if errors.Is(err, gorm.ErrRecordNotFound) || state.PendingApprovalPayload == nil {
			return store.ErrApprovalConflict
		}
		if err != nil {
			return err
		}
		var approval runtime.PendingShellApproval
		if err := json.Unmarshal([]byte(*state.PendingApprovalPayload), &approval); err != nil {
			return err
		}
		if approval.ID != approvalID {
			return store.ErrApprovalConflict
		}
		return upsertPendingApproval(tx, runID, nil)
	})
}

func (s *Store) CompleteShellApproval(ctx context.Context, runID, executingApprovalID string, next *runtime.PendingShellApproval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRun(tx, runID); err != nil {
			return err
		}
		state, err := findStateForUpdate(tx, runID)
		if errors.Is(err, gorm.ErrRecordNotFound) || state.PendingApprovalPayload == nil {
			return store.ErrApprovalConflict
		}
		if err != nil {
			return err
		}
		var approval runtime.PendingShellApproval
		if err := json.Unmarshal([]byte(*state.PendingApprovalPayload), &approval); err != nil {
			return err
		}
		if approval.ID != executingApprovalID || approval.Status != runtime.ShellApprovalExecuting {
			return store.ErrApprovalConflict
		}
		var payload *string
		if next != nil {
			if next.Status != runtime.ShellApprovalPending {
				return fmt.Errorf("replacement shell approval must be pending")
			}
			encoded, err := marshalValue(*next)
			if err != nil {
				return err
			}
			payload = &encoded
		}
		return upsertPendingApproval(tx, runID, payload)
	})
}

func (s *Store) AppendMessages(ctx context.Context, runID string, messages []runtime.Message) error {
	if err := s.requireRun(ctx, runID); err != nil {
		return err
	}
	records := make([]messageRecord, 0, len(messages))
	for _, message := range messages {
		payload, err := marshalValue(message)
		if err != nil {
			return err
		}
		records = append(records, messageRecord{RunID: runID, Payload: payload})
	}
	if len(records) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&records).Error
}

func (s *Store) ListMessages(ctx context.Context, runID string) ([]runtime.Message, error) {
	if err := s.requireRun(ctx, runID); err != nil {
		return nil, err
	}
	var records []messageRecord
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	messages := make([]runtime.Message, 0, len(records))
	for _, record := range records {
		var message runtime.Message
		if err := json.Unmarshal([]byte(record.Payload), &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (s *Store) AppendEvents(ctx context.Context, runID string, events []runtime.Event) error {
	if err := s.requireRun(ctx, runID); err != nil {
		return err
	}
	records := make([]eventRecord, 0, len(events))
	for _, event := range events {
		payload, err := marshalValue(event)
		if err != nil {
			return err
		}
		records = append(records, eventRecord{RunID: runID, Payload: payload})
	}
	if len(records) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&records).Error
}

func (s *Store) ListEvents(ctx context.Context, runID string) ([]runtime.Event, error) {
	if err := s.requireRun(ctx, runID); err != nil {
		return nil, err
	}
	var records []eventRecord
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	events := make([]runtime.Event, 0, len(records))
	for _, record := range records {
		var event runtime.Event
		if err := json.Unmarshal([]byte(record.Payload), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (s *Store) AppendModelLogs(ctx context.Context, runID string, logs []runtime.ModelLog) error {
	if err := s.requireRun(ctx, runID); err != nil {
		return err
	}
	records := make([]modelLogRecord, 0, len(logs))
	for _, modelLog := range logs {
		payload, err := marshalValue(modelLog)
		if err != nil {
			return err
		}
		records = append(records, modelLogRecord{RunID: runID, Payload: payload})
	}
	if len(records) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&records).Error
}

func (s *Store) ListModelLogs(ctx context.Context, runID string) ([]runtime.ModelLog, error) {
	if err := s.requireRun(ctx, runID); err != nil {
		return nil, err
	}
	var records []modelLogRecord
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).Order("id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	logs := make([]runtime.ModelLog, 0, len(records))
	for _, record := range records {
		var modelLog runtime.ModelLog
		if err := json.Unmarshal([]byte(record.Payload), &modelLog); err != nil {
			return nil, err
		}
		logs = append(logs, modelLog)
	}
	return logs, nil
}

func (s *Store) verifySchema(ctx context.Context) error {
	for _, table := range requiredTables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.db.WithContext(ctx).Migrator().HasTable(table) {
			return fmt.Errorf("required mysql table %q does not exist; apply migrations/mysql/001_create_agent_store.sql before starting the service", table)
		}
	}
	return nil
}

func (s *Store) requireRun(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	_, err := s.GetRun(ctx, runID)
	return err
}

func (s *Store) findRun(ctx context.Context, runID string) (runRecord, error) {
	var record runRecord
	err := s.db.WithContext(ctx).First(&record, "id = ?", runID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runRecord{}, store.ErrRunNotFound
	}
	return record, err
}

func (s *Store) findState(ctx context.Context, runID string) (stateRecord, error) {
	var state stateRecord
	err := s.db.WithContext(ctx).First(&state, "run_id = ?", runID).Error
	return state, err
}

func findStateForUpdate(tx *gorm.DB, runID string) (stateRecord, error) {
	var state stateRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "run_id = ?", runID).Error
	return state, err
}

func lockRun(tx *gorm.DB, runID string) (runRecord, error) {
	var record runRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", runID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runRecord{}, store.ErrRunNotFound
	}
	return record, err
}

func upsertPlan(tx *gorm.DB, runID string, payload string) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "run_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"plan_payload"}),
	}).Create(&stateRecord{RunID: runID, PlanPayload: &payload}).Error
}

func upsertCompaction(tx *gorm.DB, runID string, payload string) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "run_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"compaction_payload"}),
	}).Create(&stateRecord{RunID: runID, CompactionPayload: &payload}).Error
}

func upsertPendingApproval(tx *gorm.DB, runID string, payload *string) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "run_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"pending_approval_payload"}),
	}).Create(&stateRecord{RunID: runID, PendingApprovalPayload: payload}).Error
}

func runRecordFromMetadata(meta store.RunMetadata) (runRecord, error) {
	record := runRecord{
		ID:            meta.ID,
		Status:        string(meta.Status),
		CreatedAt:     meta.CreatedAt.UTC(),
		UpdatedAt:     meta.UpdatedAt.UTC(),
		ModelProvider: meta.ModelProvider,
		ErrorMessage:  meta.Error,
		PlanMode:      meta.PlanMode,
		PlanSteps:     meta.PlanSteps,
		PlanCompleted: meta.PlanCompleted,
	}
	if meta.TokenUsage == nil {
		return record, nil
	}
	record.PromptTokens = meta.TokenUsage.PromptTokens
	record.CompletionTokens = meta.TokenUsage.CompletionTokens
	record.TotalTokens = meta.TokenUsage.TotalTokens
	record.CachedTokens = meta.TokenUsage.CachedTokens
	record.ReasoningTokens = meta.TokenUsage.ReasoningTokens
	if meta.TokenUsage.Raw != nil {
		payload, err := marshalValue(meta.TokenUsage.Raw)
		if err != nil {
			return runRecord{}, err
		}
		record.TokenUsageRaw = &payload
	}
	return record, nil
}

func metadataFromRunRecord(record runRecord) (store.RunMetadata, error) {
	meta := store.RunMetadata{
		ID:            record.ID,
		Status:        store.RunStatus(record.Status),
		CreatedAt:     record.CreatedAt.UTC(),
		UpdatedAt:     record.UpdatedAt.UTC(),
		ModelProvider: record.ModelProvider,
		Error:         record.ErrorMessage,
		PlanMode:      record.PlanMode,
		PlanSteps:     record.PlanSteps,
		PlanCompleted: record.PlanCompleted,
	}
	if record.PromptTokens == nil && record.CompletionTokens == nil && record.TotalTokens == nil && record.CachedTokens == nil && record.ReasoningTokens == nil && record.TokenUsageRaw == nil {
		return meta, nil
	}
	usage := &runtime.TokenUsage{
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		TotalTokens:      record.TotalTokens,
		CachedTokens:     record.CachedTokens,
		ReasoningTokens:  record.ReasoningTokens,
	}
	if record.TokenUsageRaw != nil {
		if err := json.Unmarshal([]byte(*record.TokenUsageRaw), &usage.Raw); err != nil {
			return store.RunMetadata{}, err
		}
	}
	meta.TokenUsage = usage
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

func marshalValue(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
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
