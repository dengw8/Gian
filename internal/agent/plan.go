package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/dengw8/harness-practice/internal/runtime"
)

var (
	planHeaderPattern = regexp.MustCompile(`(?im)^\s*Plan:\s*$`)
	planStepPattern   = regexp.MustCompile(`(?m)^\s*(\d+)[.)]\s+(.+?)\s*$`)
	doneStepPattern   = regexp.MustCompile(`(?i)\[DONE:(\d+)\]`)
)

func extractPlanSteps(content string) []runtime.PlanStep {
	header := planHeaderPattern.FindStringIndex(content)
	if header == nil {
		return nil
	}
	section := content[header[1]:]
	matches := planStepPattern.FindAllStringSubmatch(section, -1)
	steps := make([]runtime.PlanStep, 0, len(matches))
	for _, match := range matches {
		text := cleanPlanStepText(match[2])
		if text == "" {
			continue
		}
		steps = append(steps, runtime.PlanStep{
			Index:  len(steps) + 1,
			Text:   text,
			Status: runtime.PlanStepPending,
		})
	}
	return steps
}

func markCompletedSteps(plan runtime.PlanState, content string) (runtime.PlanState, []runtime.PlanStep) {
	if len(plan.Steps) == 0 {
		return plan, nil
	}
	completed := []runtime.PlanStep{}
	for _, match := range doneStepPattern.FindAllStringSubmatch(content, -1) {
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		for i := range plan.Steps {
			if plan.Steps[i].Index != index || plan.Steps[i].Status == runtime.PlanStepCompleted {
				continue
			}
			plan.Steps[i].Status = runtime.PlanStepCompleted
			completed = append(completed, plan.Steps[i])
		}
	}
	return plan, completed
}

func cleanPlanStepText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "*")
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "`") || strings.HasPrefix(text, "-") {
		return ""
	}
	return text
}
