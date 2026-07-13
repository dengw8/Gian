package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dengw8/Gian/internal/runtime"
)

type OpenAICompatibleConfig struct {
	BaseURL string
	Model   string
	APIKey  string
	Client  *http.Client
}

type OpenAICompatibleProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func NewOpenAICompatibleProvider(cfg OpenAICompatibleConfig) (*OpenAICompatibleProvider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base url is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("api key is required")
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICompatibleProvider{
		baseURL: baseURL,
		model:   cfg.Model,
		apiKey:  cfg.APIKey,
		client:  client,
	}, nil
}

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, req Request) (Response, error) {
	payload := openAIChatRequest{
		Model:    p.model,
		Messages: toOpenAIMessages(req),
		Tools:    toOpenAITools(req.Tools),
	}
	if len(payload.Tools) == 0 {
		payload.Tools = nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("content-type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("openai-compatible provider returned %s: %s", httpResp.Status, strings.TrimSpace(string(respBody)))
	}

	var parsed openAIChatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Response{}, err
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai-compatible provider returned no choices")
	}
	return Response{
		Message: fromOpenAIMessage(parsed.Choices[0].Message),
		Usage:   parsed.Usage.toTokenUsage(),
	}, nil
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type openAIChatResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage,omitempty"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

type openAIUsage struct {
	PromptTokens            *int                     `json:"prompt_tokens,omitempty"`
	CompletionTokens        *int                     `json:"completion_tokens,omitempty"`
	TotalTokens             *int                     `json:"total_tokens,omitempty"`
	PromptTokensDetails     openAIPromptTokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails openAICompletionDetails  `json:"completion_tokens_details,omitempty"`
	Raw                     map[string]any           `json:"-"`
}

type openAIPromptTokenDetails struct {
	CachedTokens *int `json:"cached_tokens,omitempty"`
	AudioTokens  *int `json:"audio_tokens,omitempty"`
}

type openAICompletionDetails struct {
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
	AudioTokens     *int `json:"audio_tokens,omitempty"`
}

func (u *openAIUsage) UnmarshalJSON(data []byte) error {
	type alias openAIUsage
	var parsed alias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		parsed.Raw = raw
	}
	*u = openAIUsage(parsed)
	return nil
}

func (u openAIUsage) toTokenUsage() *runtime.TokenUsage {
	if u.PromptTokens == nil && u.CompletionTokens == nil && u.TotalTokens == nil && u.PromptTokensDetails.CachedTokens == nil && u.CompletionTokensDetails.ReasoningTokens == nil && len(u.Raw) == 0 {
		return nil
	}
	return &runtime.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.PromptTokensDetails.CachedTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
		Raw:              u.Raw,
	}
}

func toOpenAIMessages(req Request) []openAIMessage {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		openAI := openAIMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
		}
		messages = append(messages, openAI)
	}
	return messages
}

func toOpenAITools(definitions []runtime.ToolDefinition) []openAITool {
	tools := make([]openAITool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        definition.Name,
				Description: definition.Description,
				Parameters:  definition.Parameters,
			},
		})
	}
	return tools
}

func toOpenAIToolCalls(calls []runtime.ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}
	toolCalls := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, openAIToolCall{
			ID:   call.ID,
			Type: "function",
			Function: openAIToolFunction{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		})
	}
	return toolCalls
}

func fromOpenAIMessage(msg openAIMessage) runtime.Message {
	return runtime.Message{
		Role:      runtime.RoleAssistant,
		Content:   msg.Content,
		ToolCalls: fromOpenAIToolCalls(msg.ToolCalls),
	}
}

func fromOpenAIToolCalls(calls []openAIToolCall) []runtime.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	toolCalls := make([]runtime.ToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, runtime.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return toolCalls
}
