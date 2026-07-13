package agent

import (
	"fmt"
	"strings"

	"github.com/dengw8/Gian/internal/runtime"
)

const dynamicContextBoundary = "━━━ DYNAMIC CONTEXT BELOW ━━━"

type SystemPromptBuilder struct {
	StaticSections  []string
	DynamicSections []string
}

func NewSystemPromptBuilder() SystemPromptBuilder {
	return SystemPromptBuilder{
		StaticSections: []string{
			"# Role\nYou are an agent service runtime.",
			"# Safety Rules\nDo not perform destructive actions unless the available tool policy allows them.",
			"# Behavior Rules\nUse tools only when needed, then use tool results to answer.",
			"# Tool Use Policy\nPrefer registered tools over free-form assumptions. Respect tool safety attributes and runtime restrictions.",
			"# Output Policy\nAnswer with the useful result of the current turn. Keep tool-only implementation details out of the final answer unless they matter to the user.",
		},
	}
}

func (b SystemPromptBuilder) WithTools(tools []runtime.ToolDefinition) SystemPromptBuilder {
	if len(tools) == 0 {
		return b
	}
	lines := []string{"# Available Tools"}
	for _, tool := range tools {
		lines = append(lines, fmt.Sprintf("- %s: %s", tool.Name, tool.Description))
	}
	b.DynamicSections = append(b.DynamicSections, strings.Join(lines, "\n"))
	return b
}

func (b SystemPromptBuilder) WithRuntimeEnvironment(workspace string) SystemPromptBuilder {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return b
	}
	b.DynamicSections = append(b.DynamicSections, "# Runtime Environment\nWorkspace: "+workspace)
	return b
}

func (b SystemPromptBuilder) WithPlan(plan runtime.PlanState) SystemPromptBuilder {
	switch plan.Mode {
	case runtime.PlanModePlanning:
		b.DynamicSections = append(b.DynamicSections, "# Plan Mode Context\n[PLAN MODE ACTIVE]\nYou are in plan mode. Explore with read-only tools only and produce a numbered plan under a \"Plan:\" header. Do not make changes.")
	case runtime.PlanModeExecuting:
		if len(plan.Steps) == 0 {
			return b
		}
		b.DynamicSections = append(b.DynamicSections, "# Plan Mode Context\n[EXECUTING PLAN]\nExecute the persisted plan in order. After completing a step, include a [DONE:n] marker in your response.\n\nRemaining steps:\n"+remainingPlanSteps(plan))
	}
	return b
}

func (b SystemPromptBuilder) Build() string {
	sections := append([]string{}, b.StaticSections...)
	sections = append(sections, dynamicContextBoundary)
	sections = append(sections, b.DynamicSections...)
	return strings.Join(sections, "\n\n")
}
