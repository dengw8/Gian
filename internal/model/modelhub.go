package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
)

type ModelHubConfig struct {
	Endpoint  string
	Model     string
	MaxTokens int
	AK        string
	LogID     string
	Client    *http.Client
}

type ModelHubProvider struct {
	endpoint  string
	model     string
	maxTokens int
	ak        string
	logID     string
	client    *http.Client
}

func NewModelHubProvider(cfg ModelHubConfig) (*ModelHubProvider, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(cfg.AK) == "" {
		return nil, fmt.Errorf("ak is required")
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &ModelHubProvider{
		endpoint:  endpoint,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		ak:        cfg.AK,
		logID:     strings.TrimSpace(cfg.LogID),
		client:    client,
	}, nil
}

func (p *ModelHubProvider) Generate(ctx context.Context, req Request) (Response, error) {
	payload := modelHubRequest{
		Stream:    false,
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Messages:  toModelHubMessages(req),
		Tools:     toOpenAITools(req.Tools),
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = 0
	}
	if len(payload.Tools) == 0 {
		payload.Tools = nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpointWithAK(), bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-tt-logid", p.requestLogID())

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
		return Response{}, fmt.Errorf("modelhub provider returned %s: %s", httpResp.Status, strings.TrimSpace(string(respBody)))
	}

	msg, usage, err := parseModelHubResponse(respBody)
	if err != nil {
		return Response{}, err
	}
	return Response{Message: msg, Usage: usage}, nil
}

func (p *ModelHubProvider) endpointWithAK() string {
	parsed, err := url.Parse(p.endpoint)
	if err != nil {
		return p.endpoint
	}
	query := parsed.Query()
	query.Set("ak", p.ak)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (p *ModelHubProvider) requestLogID() string {
	if p.logID != "" {
		return p.logID
	}
	return fmt.Sprintf("agent-service-%d", time.Now().UTC().UnixNano())
}

type modelHubRequest struct {
	Stream    bool              `json:"stream"`
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens,omitempty"`
	Messages  []modelHubMessage `json:"messages"`
	Tools     []openAITool      `json:"tools,omitempty"`
}

type modelHubMessage struct {
	Role       string                `json:"role"`
	Content    []modelHubContentPart `json:"content,omitempty"`
	ToolCallID string                `json:"tool_call_id,omitempty"`
	Name       string                `json:"name,omitempty"`
	ToolCalls  []openAIToolCall      `json:"tool_calls,omitempty"`
}

type modelHubContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *modelHubImageURL `json:"image_url,omitempty"`
}

type modelHubImageURL struct {
	URL string `json:"url"`
}

func toModelHubMessages(req Request) []modelHubMessage {
	messages := make([]modelHubMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, modelHubMessage{
			Role:    "system",
			Content: modelHubTextContent(req.SystemPrompt),
		})
	}
	for _, msg := range req.Messages {
		messages = append(messages, modelHubMessage{
			Role:       string(msg.Role),
			Content:    modelHubContent(msg.Content, msg.Attachments),
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
		})
	}
	return messages
}

func modelHubTextContent(text string) []modelHubContentPart {
	return modelHubContent(text, nil)
}

func modelHubContent(text string, attachments []runtime.Attachment) []modelHubContentPart {
	parts := make([]modelHubContentPart, 0, 1+len(attachments))
	if text == "" {
		parts = nil
	} else {
		parts = append(parts, modelHubContentPart{Type: "text", Text: text})
	}
	for _, attachment := range attachments {
		if attachment.Type != "image" || attachment.Data == "" || attachment.MediaType == "" {
			continue
		}
		parts = append(parts, modelHubContentPart{
			Type: "image_url",
			ImageURL: &modelHubImageURL{
				URL: "data:" + attachment.MediaType + ";base64," + attachment.Data,
			},
		})
	}
	return parts
}

type modelHubResponse struct {
	Choices []modelHubChoice `json:"choices"`
	Usage   openAIUsage      `json:"usage,omitempty"`
}

type modelHubChoice struct {
	Message modelHubResponseMessage `json:"message"`
}

type modelHubResponseMessage struct {
	Role      string           `json:"role"`
	Content   json.RawMessage  `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

func parseModelHubResponse(data []byte) (runtime.Message, *runtime.TokenUsage, error) {
	var parsed modelHubResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return runtime.Message{}, nil, err
	}
	if len(parsed.Choices) == 0 {
		return runtime.Message{}, nil, fmt.Errorf("modelhub provider returned no choices")
	}
	message := parsed.Choices[0].Message
	content, err := parseModelHubContent(message.Content)
	if err != nil {
		return runtime.Message{}, nil, err
	}
	return runtime.Message{
		Role:      runtime.RoleAssistant,
		Content:   content,
		ToolCalls: fromOpenAIToolCalls(message.ToolCalls),
	}, parsed.Usage.toTokenUsage(), nil
}

func parseModelHubContent(data json.RawMessage) (string, error) {
	if len(data) == 0 || string(data) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}
	var parts []modelHubContentPart
	if err := json.Unmarshal(data, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if part.Type == "text" {
				builder.WriteString(part.Text)
			}
		}
		return builder.String(), nil
	}
	return "", fmt.Errorf("modelhub provider returned unsupported message content: %s", string(data))
}
