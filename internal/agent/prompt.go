package agent

import (
	"fmt"
	"strings"

	"github.com/dengw8/harness-practice/internal/runtime"
)

type SystemPromptBuilder struct {
	StaticSections  []string
	DynamicSections []string
}

func NewSystemPromptBuilder() SystemPromptBuilder {
	return SystemPromptBuilder{
		StaticSections: []string{
			"You are an agent service runtime.",
			"Use tools only when needed, then use tool results to answer.",
		},
	}
}

func (b SystemPromptBuilder) WithTools(tools []runtime.ToolDefinition) SystemPromptBuilder {
	if len(tools) == 0 {
		return b
	}
	lines := []string{"Available tools:"}
	for _, tool := range tools {
		lines = append(lines, fmt.Sprintf("- %s: %s", tool.Name, tool.Description))
	}
	b.DynamicSections = append(b.DynamicSections, strings.Join(lines, "\n"))
	return b
}

func (b SystemPromptBuilder) Build() string {
	sections := append([]string{}, b.StaticSections...)
	sections = append(sections, "--- dynamic context ---")
	sections = append(sections, b.DynamicSections...)
	return strings.Join(sections, "\n\n")
}
