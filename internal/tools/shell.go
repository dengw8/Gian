package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dengw8/harness-practice/internal/runtime"
)

type ShellTool struct {
	workspace string
}

type ShellInput struct {
	Command string `json:"command"`
}

func NewShellTool(workspace string) *ShellTool {
	return &ShellTool{workspace: workspace}
}

func (t *ShellTool) Name() string {
	return "shell"
}

func (t *ShellTool) Description() string {
	return "Execute an approved shell command under the configured workspace."
}

func (t *ShellTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "A single approved shell command.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *ShellTool) ReadOnly() bool {
	return false
}

func (t *ShellTool) Destructive() bool {
	return true
}

func (t *ShellTool) Concurrent() bool {
	return false
}

func (t *ShellTool) Execute(ctx context.Context, args json.RawMessage) (runtime.ToolResult, error) {
	input, err := DecodeShellInput(args)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	if err := ValidateShellCommand(input.Command); err != nil {
		return runtime.ToolResult{}, err
	}

	workspace, err := resolveWorkspacePath(t.workspace, ".")
	if err != nil {
		return runtime.ToolResult{}, err
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", input.Command)
	cmd.Dir = workspace
	output, err := cmd.CombinedOutput()
	if err != nil {
		return runtime.ToolResult{}, fmt.Errorf("shell command failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return runtime.ToolResult{Name: t.Name(), Content: string(output)}, nil
}

func DecodeShellInput(args json.RawMessage) (ShellInput, error) {
	var input ShellInput
	if err := json.Unmarshal(args, &input); err != nil {
		return ShellInput{}, fmt.Errorf("decode shell arguments: %w", err)
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return ShellInput{}, fmt.Errorf("shell command is required")
	}
	return input, nil
}

func ValidateShellCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("shell command is required")
	}
	for _, token := range []string{"&&", "||", ";", "|", "`", "$(", "\n", "\r", ">", "<", "&"} {
		if strings.Contains(command, token) {
			return fmt.Errorf("shell command rejected: command chaining or shell control operator %q is not allowed", token)
		}
	}

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("shell command is required")
	}
	if isDestructiveCommand(fields[0]) {
		return fmt.Errorf("shell command rejected: destructive command %q is not allowed", fields[0])
	}
	return nil
}

func isDestructiveCommand(command string) bool {
	base := command
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	switch base {
	case "rm", "rmdir", "mv", "chmod", "chown", "sudo", "dd", "mkfs", "shutdown", "reboot", "kill", "pkill":
		return true
	default:
		return false
	}
}
