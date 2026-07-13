package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dengw8/Gian/internal/runtime"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	ReadOnly() bool
	Destructive() bool
	Concurrent() bool
	Execute(ctx context.Context, args json.RawMessage) (runtime.ToolResult, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(toolList ...Tool) (*Registry, error) {
	registry := &Registry{tools: map[string]Tool{}}
	for _, tool := range toolList {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.tools[name] = tool
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Definitions() []runtime.ToolDefinition {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]runtime.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		definitions = append(definitions, runtime.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
			ReadOnly:    tool.ReadOnly(),
			Destructive: tool.Destructive(),
			Concurrent:  tool.Concurrent(),
		})
	}
	return definitions
}
