package runtime

import (
	"encoding/json"
	"time"
)

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

type Message struct {
	Role        MessageRole  `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCallID  string       `json:"tool_call_id,omitempty"`
	Name        string       `json:"name,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
	Usage       *TokenUsage  `json:"usage,omitempty"`
}

type Attachment struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Name      string `json:"name,omitempty"`
}

type Event struct {
	ID          string         `json:"id,omitempty"`
	Type        string         `json:"type"`
	RunID       string         `json:"run_id,omitempty"`
	TurnID      string         `json:"turn_id,omitempty"`
	ModelCallID string         `json:"model_call_id,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Message     string         `json:"message,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	Usage       *TokenUsage    `json:"usage,omitempty"`
}

func NewEvent(eventType string, message string) Event {
	return Event{
		Type:      eventType,
		CreatedAt: time.Now().UTC(),
		Message:   message,
	}
}

type TokenUsage struct {
	PromptTokens     *int           `json:"prompt_tokens,omitempty"`
	CompletionTokens *int           `json:"completion_tokens,omitempty"`
	TotalTokens      *int           `json:"total_tokens,omitempty"`
	CachedTokens     *int           `json:"cached_tokens,omitempty"`
	ReasoningTokens  *int           `json:"reasoning_tokens,omitempty"`
	Raw              map[string]any `json:"raw,omitempty"`
}

type ModelRequestLog struct {
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Messages     []Message        `json:"messages,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

type ModelResponseLog struct {
	Message Message     `json:"message,omitempty"`
	Usage   *TokenUsage `json:"usage,omitempty"`
}

type ModelLog struct {
	Index       int              `json:"index"`
	CallID      string           `json:"call_id,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
	Request     ModelRequestLog  `json:"request"`
	Response    ModelResponseLog `json:"response,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type CompactionState struct {
	Enabled           bool      `json:"enabled"`
	Summary           string    `json:"summary,omitempty"`
	CompactedMessages int       `json:"compacted_messages,omitempty"`
	KeepRecent        int       `json:"keep_recent,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

func EmptyCompactionState() CompactionState {
	return CompactionState{}
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ShellApprovalStatus string

const (
	ShellApprovalPending   ShellApprovalStatus = "pending"
	ShellApprovalExecuting ShellApprovalStatus = "executing"
)

type ShellApprovalDecision string

const (
	ShellApprovalApprove ShellApprovalDecision = "approve"
	ShellApprovalDeny    ShellApprovalDecision = "deny"
	ShellApprovalCancel  ShellApprovalDecision = "cancel"
)

type PendingShellApproval struct {
	ID                 string              `json:"id"`
	Version            int                 `json:"version"`
	ToolCall           ToolCall            `json:"tool_call"`
	Workspace          string              `json:"workspace"`
	RiskReasons        []string            `json:"risk_reasons"`
	ScriptHash         string              `json:"script_hash"`
	RemainingToolCalls []ToolCall          `json:"remaining_tool_calls,omitempty"`
	TurnID             string              `json:"turn_id,omitempty"`
	Iteration          int                 `json:"iteration,omitempty"`
	RequestedAt        time.Time           `json:"requested_at"`
	Status             ShellApprovalStatus `json:"status"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	ReadOnly    bool           `json:"read_only"`
	Destructive bool           `json:"destructive"`
	Concurrent  bool           `json:"concurrent"`
}

type PlanMode string

const (
	PlanModeOff       PlanMode = "off"
	PlanModePlanning  PlanMode = "planning"
	PlanModeExecuting PlanMode = "executing"
)

type PlanStepStatus string

const (
	PlanStepPending   PlanStepStatus = "pending"
	PlanStepCompleted PlanStepStatus = "completed"
)

type PlanState struct {
	Mode      PlanMode   `json:"mode"`
	Steps     []PlanStep `json:"steps,omitempty"`
	CreatedAt time.Time  `json:"created_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
}

type PlanStep struct {
	Index  int            `json:"index"`
	Text   string         `json:"text"`
	Status PlanStepStatus `json:"status"`
}

func EmptyPlanState() PlanState {
	return PlanState{Mode: PlanModeOff}
}

func (p PlanState) IsActive() bool {
	return p.Mode == PlanModePlanning || p.Mode == PlanModeExecuting
}

func (p PlanState) CompletedCount() int {
	count := 0
	for _, step := range p.Steps {
		if step.Status == PlanStepCompleted {
			count++
		}
	}
	return count
}
