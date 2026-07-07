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

func TestShellToolRejectsDestructiveCommand(t *testing.T) {
	tool := NewShellTool(t.TempDir())
	args, err := json.Marshal(map[string]string{"command": "rm file.txt"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "destructive command") {
		t.Fatalf("err = %v, want destructive command rejection", err)
	}
}

func TestShellToolRejectsChainedCommand(t *testing.T) {
	tool := NewShellTool(t.TempDir())
	args, err := json.Marshal(map[string]string{"command": "printf hello && rm file.txt"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "command chaining") {
		t.Fatalf("err = %v, want command chaining rejection", err)
	}
}
