package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dengw8/harness-practice/internal/runtime"
)

type Provider interface {
	Generate(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	SystemPrompt string                   `json:"system_prompt"`
	Messages     []runtime.Message        `json:"messages"`
	Tools        []runtime.ToolDefinition `json:"tools"`
}

type Response struct {
	Message runtime.Message `json:"message"`
}

type FakeProvider struct{}

func NewFakeProvider() *FakeProvider {
	return &FakeProvider{}
}

func (p *FakeProvider) Generate(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	if strings.Contains(req.SystemPrompt, "[PLAN MODE ACTIVE]") {
		return Response{
			Message: runtime.Message{
				Role: runtime.RoleAssistant,
				Content: "Plan:\n" +
					"1. Inspect the current implementation.\n" +
					"2. Apply the requested change.\n" +
					"3. Verify the result with focused tests.",
			},
		}, nil
	}
	if strings.Contains(req.SystemPrompt, "[EXECUTING PLAN]") {
		return Response{
			Message: runtime.Message{
				Role:    runtime.RoleAssistant,
				Content: "Executed the next planned step. [DONE:1]",
			},
		}, nil
	}

	if result, ok := lastToolMessage(req.Messages); ok {
		content := result.Content
		if content == "" {
			content = "tool returned no content"
		}
		return Response{
			Message: runtime.Message{
				Role:    runtime.RoleAssistant,
				Content: fmt.Sprintf("fake tool response from %s: %s", result.Name, content),
			},
		}, nil
	}

	last := ""
	lastAttachmentCount := 0
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == runtime.RoleUser {
			last = req.Messages[i].Content
			lastAttachmentCount = len(req.Messages[i].Attachments)
			break
		}
	}
	if last == "" {
		if lastAttachmentCount > 0 {
			return Response{
				Message: runtime.Message{
					Role:    runtime.RoleAssistant,
					Content: fmt.Sprintf("fake response: received %d image(s)", lastAttachmentCount),
				},
			}, nil
		}
		last = "empty request"
	}
	if call, ok, err := fakeToolCall(last, req.Tools); ok || err != nil {
		if err != nil {
			return Response{}, err
		}
		return Response{
			Message: runtime.Message{
				Role:      runtime.RoleAssistant,
				ToolCalls: []runtime.ToolCall{call},
			},
		}, nil
	}

	return Response{
		Message: runtime.Message{
			Role:    runtime.RoleAssistant,
			Content: fakeTextResponse(last, lastAttachmentCount),
		},
	}, nil
}

func fakeTextResponse(text string, attachmentCount int) string {
	if attachmentCount == 0 {
		return fmt.Sprintf("fake response: %s", text)
	}
	return fmt.Sprintf("fake response: %s (received %d image(s))", text, attachmentCount)
}

func lastToolMessage(messages []runtime.Message) (runtime.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == runtime.RoleTool {
			return messages[i], true
		}
		if messages[i].Role == runtime.RoleUser {
			return runtime.Message{}, false
		}
	}
	return runtime.Message{}, false
}

func fakeToolCall(input string, definitions []runtime.ToolDefinition) (runtime.ToolCall, bool, error) {
	for _, prefix := range []struct {
		command string
		tool    string
		argName string
	}{
		{command: "read_file:", tool: "read_file", argName: "path"},
		{command: "list_files:", tool: "list_files", argName: "path"},
		{command: "shell:", tool: "shell", argName: "command"},
	} {
		if !strings.HasPrefix(input, prefix.command) {
			continue
		}
		if !toolAvailable(prefix.tool, definitions) {
			return runtime.ToolCall{}, false, nil
		}
		value := strings.TrimSpace(strings.TrimPrefix(input, prefix.command))
		args, err := json.Marshal(map[string]string{prefix.argName: value})
		if err != nil {
			return runtime.ToolCall{}, false, err
		}
		return runtime.ToolCall{
			ID:        "fake_call_1",
			Name:      prefix.tool,
			Arguments: args,
		}, true, nil
	}
	return runtime.ToolCall{}, false, nil
}

func toolAvailable(name string, definitions []runtime.ToolDefinition) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}
