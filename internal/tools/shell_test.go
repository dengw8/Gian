package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellToolExecutesApprovedCommandInWorkspace(t *testing.T) {
	tool := NewShellTool(t.TempDir())
	args, err := json.Marshal(map[string]string{"command": "printf hello"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello" {
		t.Fatalf("content = %q, want hello", result.Content)
	}
}

func TestShellToolExecutesValidScriptAfterApproval(t *testing.T) {
	tool := NewShellTool(t.TempDir())
	args, err := json.Marshal(map[string]string{"command": "printf hello && printf world"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "helloworld" {
		t.Fatalf("content = %q, want helloworld", result.Content)
	}
}

func TestAnalyzeShellScript(t *testing.T) {
	tests := []struct {
		name             string
		command          string
		requireApproval  bool
		wantParseFailure bool
	}{
		{name: "read only", command: "rg PlanMode", requireApproval: false},
		{name: "destructive", command: "rm file.txt", requireApproval: true},
		{name: "multiple commands", command: "pwd; touch file.txt", requireApproval: true},
		{name: "redirection", command: "printf hello > output.txt", requireApproval: true},
		{name: "substitution", command: "echo $(pwd)", requireApproval: true},
		{name: "invalid", command: "if", wantParseFailure: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis, err := AnalyzeShellScript(tt.command)
			if tt.wantParseFailure {
				if err == nil {
					t.Fatal("expected parse error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if analysis.RequiresApproval != tt.requireApproval {
				t.Fatalf("requires approval = %t, want %t (%#v)", analysis.RequiresApproval, tt.requireApproval, analysis)
			}
		})
	}
}

func TestValidateReadOnlyShellCommand(t *testing.T) {
	if err := ValidateReadOnlyShellCommand("rg PlanMode"); err != nil {
		t.Fatalf("read-only command rejected: %v", err)
	}
	if err := ValidateReadOnlyShellCommand("touch file.txt"); err == nil || !strings.Contains(err.Error(), "plan mode") {
		t.Fatalf("err = %v, want plan mode rejection", err)
	}
}
