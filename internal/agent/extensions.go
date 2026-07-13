package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dengw8/Gian/internal/model"
	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/tools"
)

type PlanModeMiddleware struct {
	NoopMiddleware
}

func (m PlanModeMiddleware) Name() string {
	return "plan_mode"
}

func (m PlanModeMiddleware) BeforeModel(ctx context.Context, req *model.Request, state *State) error {
	switch state.Plan.Mode {
	case runtime.PlanModePlanning:
		req.Tools = planningTools(req.Tools)
	case runtime.PlanModeExecuting:
		return nil
	}
	return nil
}

func (m PlanModeMiddleware) BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	if state.Plan.Mode != runtime.PlanModePlanning {
		return nil
	}
	if call.Name != "shell" {
		for _, definition := range state.ActiveTools {
			if definition.Name == call.Name && definition.ReadOnly && !definition.Destructive {
				return nil
			}
		}
		return fmt.Errorf("plan mode rejected tool %q: only read-only tools are allowed while planning", call.Name)
	}
	input, err := tools.DecodeShellInput(call.Arguments)
	if err != nil {
		return err
	}
	return tools.ValidateReadOnlyShellCommand(input.Command)
}

func planningTools(definitions []runtime.ToolDefinition) []runtime.ToolDefinition {
	filtered := make([]runtime.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "shell" || (definition.ReadOnly && !definition.Destructive) {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func remainingPlanSteps(plan runtime.PlanState) string {
	lines := []string{}
	for _, step := range plan.Steps {
		if step.Status == runtime.PlanStepCompleted {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", step.Index, step.Text))
	}
	if len(lines) == 0 {
		return "All steps are complete."
	}
	return strings.Join(lines, "\n")
}

type CompactionMiddleware struct {
	NoopMiddleware
	Enabled     bool
	MaxMessages int
	KeepRecent  int
}

func (m CompactionMiddleware) Name() string {
	return "compaction"
}

func (m CompactionMiddleware) BeforeModel(ctx context.Context, req *model.Request, state *State) error {
	if !m.Enabled {
		return nil
	}
	maxMessages := m.MaxMessages
	if maxMessages <= 1 {
		maxMessages = 20
	}
	keepRecent := m.KeepRecent
	if keepRecent <= 0 {
		keepRecent = 8
	}
	if keepRecent >= maxMessages {
		keepRecent = maxMessages - 1
	}
	if len(req.Messages) <= maxMessages {
		state.Compaction.Enabled = true
		return nil
	}

	split := adjustCompactionSplit(req.Messages, len(req.Messages)-keepRecent)
	if split <= 0 || split >= len(req.Messages) {
		state.Compaction.Enabled = true
		return nil
	}

	older := req.Messages[:split]
	recent := append([]runtime.Message(nil), req.Messages[split:]...)
	summary := buildDeterministicCompactionSummary(older, len(recent))
	req.Messages = append([]runtime.Message{compactionSummaryMessage(summary)}, recent...)
	state.Compaction = runtime.CompactionState{
		Enabled:           true,
		Summary:           summary,
		CompactedMessages: len(older),
		KeepRecent:        len(recent),
		UpdatedAt:         time.Now().UTC(),
	}
	return nil
}

func adjustCompactionSplit(messages []runtime.Message, split int) int {
	for split > 0 && split < len(messages) && messages[split].Role == runtime.RoleTool {
		split--
	}
	return split
}

func compactionSummaryMessage(summary string) runtime.Message {
	return runtime.Message{
		Role: runtime.RoleUser,
		Content: "# Compacted Conversation Context\n" +
			"The following is a deterministic summary of earlier conversation messages. Recent original messages are preserved separately.\n\n" +
			summary,
	}
}

func buildDeterministicCompactionSummary(messages []runtime.Message, keepRecent int) string {
	// TODO(compaction): Replace this deterministic preview with a PI-style summarizer
	// once the persisted state and request-shaping flow are stable.
	lines := []string{fmt.Sprintf("Compacted %d earlier message(s); keeping %d recent message(s).", len(messages), keepRecent)}
	if len(messages) == 0 {
		return lines[0]
	}
	lines = append(lines, "")
	for i, message := range messages {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, compactMessageLine(message)))
	}
	return strings.Join(lines, "\n")
}

func compactMessageLine(message runtime.Message) string {
	parts := []string{string(message.Role)}
	if message.Name != "" {
		parts = append(parts, message.Name)
	}
	if len(message.ToolCalls) > 0 {
		names := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			names = append(names, call.Name)
		}
		parts = append(parts, "tool_calls: "+strings.Join(names, ", "))
	}
	if len(message.Attachments) > 0 {
		attachments := make([]string, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			label := attachment.MediaType
			if attachment.Name != "" {
				label = attachment.Name + "(" + attachment.MediaType + ")"
			}
			attachments = append(attachments, label)
		}
		parts = append(parts, "attachments: "+strings.Join(attachments, ", "))
	}
	preview := compactPreview(message.Content, 160)
	if preview != "" {
		parts = append(parts, preview)
	}
	return strings.Join(parts, ": ")
}

func compactPreview(content string, maxRunes int) string {
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return "<empty>"
	}
	if utf8.RuneCountInString(content) <= maxRunes {
		return content
	}
	runes := []rune(content)
	return string(runes[:maxRunes]) + "..."
}

type SubAgentMiddleware struct {
	NoopMiddleware
}

func (m SubAgentMiddleware) Name() string {
	return "subagent"
}
