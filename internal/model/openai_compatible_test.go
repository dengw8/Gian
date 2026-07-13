package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dengw8/Gian/internal/runtime"
)

func TestOpenAICompatibleProviderMapsTextResponse(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeOpenAITestResponse(t, w, openAIChatResponse{
			Choices: []openAIChoice{{
				Message: openAIMessage{Role: "assistant", Content: "hello"},
			}},
			Usage: openAIUsage{
				PromptTokens:     intPtr(16),
				CompletionTokens: intPtr(5),
				TotalTokens:      intPtr(21),
				PromptTokensDetails: openAIPromptTokenDetails{
					CachedTokens: intPtr(8),
				},
				CompletionTokensDetails: openAICompletionDetails{
					ReasoningTokens: intPtr(2),
				},
			},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL + "/v1",
		Model:   "test-model",
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := provider.Generate(context.Background(), Request{
		SystemPrompt: "system prompt",
		Messages: []runtime.Message{
			{Role: runtime.RoleUser, Content: "hi"},
		},
		Tools: []runtime.ToolDefinition{{
			Name:        "read_file",
			Description: "Read file",
			Parameters:  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Role != runtime.RoleAssistant || resp.Message.Content != "hello" {
		t.Fatalf("message = %#v", resp.Message)
	}
	if captured.Model != "test-model" {
		t.Fatalf("model = %q, want test-model", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content != "system prompt" {
		t.Fatalf("system message = %#v", captured.Messages[0])
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "read_file" {
		t.Fatalf("tools = %#v", captured.Tools)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if *resp.Usage.PromptTokens != 16 || *resp.Usage.CompletionTokens != 5 || *resp.Usage.TotalTokens != 21 || *resp.Usage.CachedTokens != 8 || *resp.Usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
}

func TestOpenAICompatibleProviderMapsToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOpenAITestResponse(t, w, openAIChatResponse{
			Choices: []openAIChoice{{
				Message: openAIMessage{
					Role: "assistant",
					ToolCalls: []openAIToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: openAIToolFunction{
							Name:      "read_file",
							Arguments: json.RawMessage(`{"path":"note.txt"}`),
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := provider.Generate(context.Background(), Request{
		Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" || string(call.Arguments) != `{"path":"note.txt"}` {
		t.Fatalf("tool call = %#v", call)
	}
}

func TestOpenAICompatibleProviderMapsToolResultMessage(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeOpenAITestResponse(t, w, openAIChatResponse{
			Choices: []openAIChoice{{
				Message: openAIMessage{Role: "assistant", Content: "done"},
			}},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.Generate(context.Background(), Request{
		Messages: []runtime.Message{{
			Role:       runtime.RoleTool,
			ToolCallID: "call_1",
			Name:       "read_file",
			Content:    "file content",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(captured.Messages))
	}
	msg := captured.Messages[0]
	if msg.Role != "tool" || msg.ToolCallID != "call_1" || msg.Content != "file content" {
		t.Fatalf("tool result message = %#v", msg)
	}
}

func writeOpenAITestResponse(t *testing.T, w http.ResponseWriter, resp openAIChatResponse) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Fatal(err)
	}
}

func intPtr(value int) *int {
	return &value
}
