package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/tools"
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
		req.SystemPrompt = req.SystemPrompt + "\n\n[PLAN MODE ACTIVE]\nYou are in plan mode. Explore with read-only tools only and produce a numbered plan under a \"Plan:\" header. Do not make changes."
		req.Tools = planningTools(req.Tools)
	case runtime.PlanModeExecuting:
		if len(state.Plan.Steps) == 0 {
			return nil
		}
		req.SystemPrompt = req.SystemPrompt + "\n\n[EXECUTING PLAN]\nExecute the persisted plan in order. After completing a step, include a [DONE:n] marker in your response.\n\nRemaining steps:\n" + remainingPlanSteps(state.Plan)
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
}

func (m CompactionMiddleware) Name() string {
	return "compaction"
}

type SubAgentMiddleware struct {
	NoopMiddleware
}

func (m SubAgentMiddleware) Name() string {
	return "subagent"
}
