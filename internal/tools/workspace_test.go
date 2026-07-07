package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileToolReadsWorkspaceFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)
	args, err := json.Marshal(map[string]string{"path": "note.txt"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello from file" {
		t.Fatalf("content = %q, want file content", result.Content)
	}
}

func TestListFilesToolListsDirectChildren(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewListFilesTool(dir)
	args, err := json.Marshal(map[string]string{"path": "."})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	want := "a.txt\nb.txt\nsubdir/"
	if result.Content != want {
		t.Fatalf("content = %q, want %q", result.Content, want)
	}
}

func TestWorkspaceToolsRejectPathEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)
	args, err := json.Marshal(map[string]string{"path": "../outside.txt"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("err = %v, want path escape error", err)
	}
}
