package mysql

import "time"

type runRecord struct {
	ID               string    `gorm:"primaryKey;size:64"`
	Status           string    `gorm:"size:32;not null"`
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
	ModelProvider    string    `gorm:"size:128;not null"`
	ErrorMessage     string    `gorm:"type:longtext"`
	PlanMode         string    `gorm:"size:32;not null"`
	PlanSteps        int       `gorm:"not null"`
	PlanCompleted    int       `gorm:"not null"`
	PromptTokens     *int
	CompletionTokens *int
	TotalTokens      *int
	CachedTokens     *int
	ReasoningTokens  *int
	TokenUsageRaw    *string `gorm:"type:longtext"`
}

func (runRecord) TableName() string {
	return "agent_runs"
}

type messageRecord struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement"`
	RunID   string `gorm:"size:64;not null;index"`
	Payload string `gorm:"type:longtext;not null"`
}

func (messageRecord) TableName() string {
	return "agent_run_messages"
}

type eventRecord struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement"`
	RunID   string `gorm:"size:64;not null;index"`
	Payload string `gorm:"type:longtext;not null"`
}

func (eventRecord) TableName() string {
	return "agent_run_events"
}

type modelLogRecord struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement"`
	RunID   string `gorm:"size:64;not null;index"`
	Payload string `gorm:"type:longtext;not null"`
}

func (modelLogRecord) TableName() string {
	return "agent_model_logs"
}

type stateRecord struct {
	RunID             string  `gorm:"primaryKey;size:64"`
	PlanPayload       *string `gorm:"type:longtext"`
	CompactionPayload *string `gorm:"type:longtext"`
}

func (stateRecord) TableName() string {
	return "agent_run_states"
}
