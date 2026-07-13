package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dengw8/Gian/internal/runtime"
)

type ReadFileTool struct {
	workspace string
}

type ListFilesTool struct {
	workspace string
}

func NewReadFileTool(workspace string) *ReadFileTool {
	return &ReadFileTool{workspace: workspace}
}

func NewListFilesTool(workspace string) *ListFilesTool {
	return &ListFilesTool{workspace: workspace}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read a UTF-8 text file under the configured workspace."
}

func (t *ReadFileTool) Parameters() map[string]any {
	return pathParameters()
}

func (t *ReadFileTool) ReadOnly() bool {
	return true
}

func (t *ReadFileTool) Destructive() bool {
	return false
}

func (t *ReadFileTool) Concurrent() bool {
	return true
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (runtime.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return runtime.ToolResult{}, err
	}
	var input pathInput
	if err := json.Unmarshal(args, &input); err != nil {
		return runtime.ToolResult{}, fmt.Errorf("decode read_file arguments: %w", err)
	}
	path, err := resolveWorkspacePath(t.workspace, input.Path)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	if info.IsDir() {
		return runtime.ToolResult{}, fmt.Errorf("%s is a directory", input.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	return runtime.ToolResult{Name: t.Name(), Content: string(data)}, nil
}

func (t *ListFilesTool) Name() string {
	return "list_files"
}

func (t *ListFilesTool) Description() string {
	return "List direct child files and directories under a workspace directory."
}

func (t *ListFilesTool) Parameters() map[string]any {
	return pathParameters()
}

func (t *ListFilesTool) ReadOnly() bool {
	return true
}

func (t *ListFilesTool) Destructive() bool {
	return false
}

func (t *ListFilesTool) Concurrent() bool {
	return true
}

func (t *ListFilesTool) Execute(ctx context.Context, args json.RawMessage) (runtime.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return runtime.ToolResult{}, err
	}
	var input pathInput
	if err := json.Unmarshal(args, &input); err != nil {
		return runtime.ToolResult{}, fmt.Errorf("decode list_files arguments: %w", err)
	}
	path, err := resolveWorkspacePath(t.workspace, input.Path)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return runtime.ToolResult{}, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return runtime.ToolResult{Name: t.Name(), Content: strings.Join(names, "\n")}, nil
}

type pathInput struct {
	Path string `json:"path"`
}

func pathParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Workspace-relative path.",
			},
		},
		"required": []string{"path"},
	}
}

func resolveWorkspacePath(workspace string, requested string) (string, error) {
	if requested == "" {
		requested = "."
	}
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("path must be relative to workspace")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.Clean(requested))
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return candidate, nil
}
