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
}

type Attachment struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Name      string `json:"name,omitempty"`
}

type Event struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Message   string    `json:"message,omitempty"`
}

func NewEvent(eventType string, message string) Event {
	return Event{
		Type:      eventType,
		CreatedAt: time.Now().UTC(),
		Message:   message,
	}
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
