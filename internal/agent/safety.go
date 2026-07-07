package agent

import (
	"context"

	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/tools"
)

type SafetyMiddleware struct {
	NoopMiddleware
}

func (m SafetyMiddleware) Name() string {
	return "safety"
}

func (m SafetyMiddleware) BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	if call.Name != "shell" {
		return nil
	}
	input, err := tools.DecodeShellInput(call.Arguments)
	if err != nil {
		return err
	}
	return tools.ValidateShellCommand(input.Command)
}
