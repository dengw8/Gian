package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dengw8/Gian/internal/runtime"
	"mvdan.cc/sh/v3/syntax"
)

type ShellTool struct {
	workspace string
}

type ShellInput struct {
	Command string `json:"command"`
}

type ShellRiskAnalysis struct {
	RequiresApproval bool
	Reasons          []string
}

func NewShellTool(workspace string) *ShellTool {
	return &ShellTool{workspace: workspace}
}

func (t *ShellTool) Name() string {
	return "shell"
}

func (t *ShellTool) Description() string {
	return "Execute an approved POSIX shell script under the configured workspace."
}

func (t *ShellTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "A complete POSIX shell script. Scripts that are not demonstrably read-only require user approval.",
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
	_, err := parseShellScript(command)
	return err
}

func ValidateReadOnlyShellCommand(command string) error {
	analysis, err := AnalyzeShellScript(command)
	if err != nil {
		return err
	}
	if analysis.RequiresApproval {
		return fmt.Errorf("shell script rejected in plan mode: %s", strings.Join(analysis.Reasons, "; "))
	}
	return nil
}

func AnalyzeShellScript(command string) (ShellRiskAnalysis, error) {
	file, err := parseShellScript(command)
	if err != nil {
		return ShellRiskAnalysis{}, err
	}
	if len(file.Stmts) != 1 {
		return approvalRequired("multiple shell statements are not provably read-only"), nil
	}
	stmt := file.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Semicolon.IsValid() {
		return approvalRequired("shell control operators are not provably read-only"), nil
	}
	if len(stmt.Redirs) > 0 {
		return approvalRequired("shell redirection requires approval"), nil
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return approvalRequired("compound shell syntax requires approval"), nil
	}
	if len(call.Assigns) > 0 {
		return approvalRequired("shell variable assignment requires approval"), nil
	}
	words, ok := literalWords(call.Args)
	if !ok || len(words) == 0 {
		return approvalRequired("shell expansion or dynamic command requires approval"), nil
	}
	base := commandBase(words[0])
	if !isReadOnlyCommand(base, words[1:]) {
		return approvalRequired(fmt.Sprintf("command %q is not provably read-only", base)), nil
	}
	return ShellRiskAnalysis{}, nil
}

func parseShellScript(command string) (*syntax.File, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("shell command is required")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("parse shell script: %w", err)
	}
	return file, nil
}

func approvalRequired(reason string) ShellRiskAnalysis {
	return ShellRiskAnalysis{RequiresApproval: true, Reasons: []string{reason}}
}

func literalWords(words []*syntax.Word) ([]string, bool) {
	values := make([]string, 0, len(words))
	for _, word := range words {
		value := word.Lit()
		if value == "" {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func commandBase(command string) string {
	if idx := strings.LastIndex(command, "/"); idx >= 0 {
		return command[idx+1:]
	}
	return command
}

func isReadOnlyCommand(command string, args []string) bool {
	switch command {
	case "cat", "head", "tail", "less", "more", "grep", "ls", "pwd", "echo", "printf", "wc", "uniq", "diff", "file", "stat", "du", "df", "tree", "which", "whereis", "type", "printenv", "uname", "whoami", "id", "date", "ps", "rg", "fd", "jq":
		return true
	case "git":
		if len(args) == 0 {
			return false
		}
		switch args[0] {
		case "status", "log", "branch", "remote":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
