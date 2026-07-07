package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dengw8/harness-practice/internal/runtime"
)

func TestModelHubProviderMapsTextResponse(t *testing.T) {
	var captured modelHubRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/modelhub/online/v2/crawl" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("ak"); got != "test-ak" {
			t.Fatalf("ak = %q", got)
		}
		if got := r.Header.Get("x-tt-logid"); got != "test-logid" {
			t.Fatalf("x-tt-logid = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeModelHubTestResponse(t, w, modelHubResponse{
			Choices: []modelHubChoice{{
				Message: modelHubResponseMessage{
					Role:    "assistant",
					Content: json.RawMessage(`"2"`),
				},
			}},
			Usage: openAIUsage{
				PromptTokens:     intPtr(16),
				CompletionTokens: intPtr(5),
				TotalTokens:      intPtr(21),
				PromptTokensDetails: openAIPromptTokenDetails{
					CachedTokens: intPtr(7),
				},
			},
		})
	}))
	defer server.Close()

	provider, err := NewModelHubProvider(ModelHubConfig{
		Endpoint:  server.URL + "/api/modelhub/online/v2/crawl",
		Model:     "gpt-5.4-2026-03-05",
		MaxTokens: 500,
		AK:        "test-ak",
		LogID:     "test-logid",
		Client:    server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := provider.Generate(context.Background(), Request{
		SystemPrompt: "system prompt",
		Messages: []runtime.Message{
			{Role: runtime.RoleUser, Content: "What is the result of 1+1?"},
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
	if resp.Message.Role != runtime.RoleAssistant || resp.Message.Content != "2" {
		t.Fatalf("message = %#v", resp.Message)
	}
	if captured.Stream {
		t.Fatal("stream = true, want false")
	}
	if captured.Model != "gpt-5.4-2026-03-05" {
		t.Fatalf("model = %q", captured.Model)
	}
	if captured.MaxTokens != 500 {
		t.Fatalf("max_tokens = %d, want 500", captured.MaxTokens)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content[0].Text != "system prompt" {
		t.Fatalf("system message = %#v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || captured.Messages[1].Content[0].Type != "text" || captured.Messages[1].Content[0].Text != "What is the result of 1+1?" {
		t.Fatalf("user message = %#v", captured.Messages[1])
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "read_file" {
		t.Fatalf("tools = %#v", captured.Tools)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if *resp.Usage.PromptTokens != 16 || *resp.Usage.CachedTokens != 7 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
}

func TestModelHubProviderMapsContentArrayAndToolCallResponse(t *testing.T) {
	var capturedLogID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLogID = r.Header.Get("x-tt-logid")
		writeModelHubTestResponse(t, w, modelHubResponse{
			Choices: []modelHubChoice{{
				Message: modelHubResponseMessage{
					Role:    "assistant",
					Content: json.RawMessage(`[{"type":"text","text":"read it"}]`),
					ToolCalls: []openAIToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: openAIToolFunction{
							Name:      "read_file",
							Arguments: json.RawMessage(`{"path":"README.md"}`),
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	provider, err := NewModelHubProvider(ModelHubConfig{
		Endpoint: server.URL,
		Model:    "test-model",
		AK:       "test-ak",
		Client:   server.Client(),
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
	if capturedLogID == "" {
		t.Fatal("expected generated x-tt-logid")
	}
	if resp.Message.Content != "read it" {
		t.Fatalf("content = %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" || string(call.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("tool call = %#v", call)
	}
}

func TestModelHubProviderMapsToolResultMessage(t *testing.T) {
	var captured modelHubRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeModelHubTestResponse(t, w, modelHubResponse{
			Choices: []modelHubChoice{{
				Message: modelHubResponseMessage{
					Role:    "assistant",
					Content: json.RawMessage(`"done"`),
				},
			}},
		})
	}))
	defer server.Close()

	provider, err := NewModelHubProvider(ModelHubConfig{
		Endpoint: server.URL,
		Model:    "test-model",
		AK:       "test-ak",
		Client:   server.Client(),
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
	if msg.Role != "tool" || msg.ToolCallID != "call_1" || msg.Content[0].Text != "file content" {
		t.Fatalf("tool result message = %#v", msg)
	}
}

func TestModelHubProviderMapsImageAttachments(t *testing.T) {
	var captured modelHubRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writeModelHubTestResponse(t, w, modelHubResponse{
			Choices: []modelHubChoice{{
				Message: modelHubResponseMessage{
					Role:    "assistant",
					Content: json.RawMessage(`"done"`),
				},
			}},
		})
	}))
	defer server.Close()

	provider, err := NewModelHubProvider(ModelHubConfig{
		Endpoint: server.URL,
		Model:    "test-model",
		AK:       "test-ak",
		Client:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.Generate(context.Background(), Request{
		Messages: []runtime.Message{{
			Role:    runtime.RoleUser,
			Content: "describe",
			Attachments: []runtime.Attachment{{
				Type:      "image",
				MediaType: "image/png",
				Data:      "aW1hZ2U=",
				Name:      "image.png",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(captured.Messages))
	}
	content := captured.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("content parts = %#v, want text and image", content)
	}
	if content[0].Type != "text" || content[0].Text != "describe" {
		t.Fatalf("text part = %#v", content[0])
	}
	if content[1].Type != "image_url" || content[1].ImageURL == nil || content[1].ImageURL.URL != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image part = %#v", content[1])
	}
}

func writeModelHubTestResponse(t *testing.T, w http.ResponseWriter, resp modelHubResponse) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Fatal(err)
	}
}
