package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type AnthropicClient struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	retryCount   int
	retryBackoff time.Duration
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens,omitempty"`
		OutputTokens             int `json:"output_tokens,omitempty"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

func NewAnthropicClient(cfg config.LLMConfig) *AnthropicClient {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	return &AnthropicClient{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       cfg.APIKey,
		httpClient:   &http.Client{Timeout: timeout},
		retryCount:   cfg.RetryCount,
		retryBackoff: backoff,
	}
}

func (c *AnthropicClient) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	if err := validateAnthropicSetup(c, req); err != nil {
		return schema.ChatResponse{}, err
	}
	payload := anthropicRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  toAnthropicMessages(req.Messages),
		MaxTokens: req.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		resp, err := c.doJSON(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryableError(err) || attempt == c.retryCount {
			break
		}
		if err := sleepWithContext(ctx, c.retryBackoff*time.Duration(attempt+1)); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return schema.ChatResponse{}, lastErr
}

func (c *AnthropicClient) StreamChat(ctx context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	if handler != nil && resp.Message.Content != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return resp, nil
}

func (c *AnthropicClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func validateAnthropicSetup(c *AnthropicClient, req schema.ChatRequest) error {
	if c == nil {
		return fmt.Errorf("model provider setup required: provider client is not configured")
	}
	missing := make([]string, 0, 3)
	if strings.TrimSpace(c.baseURL) == "" {
		missing = append(missing, "base_url")
	}
	if strings.TrimSpace(req.Model) == "" {
		missing = append(missing, "model")
	}
	if strings.TrimSpace(c.apiKey) == "" {
		missing = append(missing, "api_key")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("model provider setup required: configure %s in Web Studio Settings/Resources or configs/providers/*.yaml", strings.Join(missing, ", "))
}

func toAnthropicMessages(messages []schema.Message) []anthropicMessage {
	converted := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		converted = append(converted, anthropicMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return converted
}

func (c *AnthropicClient) doJSON(ctx context.Context, body []byte) (schema.ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	c.applyHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return schema.ChatResponse{}, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var raw anthropicResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return normalizeAnthropicResponse(raw), nil
}

func normalizeAnthropicResponse(resp anthropicResponse) schema.ChatResponse {
	var content strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}
	return schema.ChatResponse{
		Message: schema.Message{
			Role:    "assistant",
			Content: content.String(),
		},
		StopReason: resp.StopReason,
		Usage: schema.TokenUsage{
			PromptTokens: resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CachedTokens: resp.Usage.CacheReadInputTokens,
		},
	}
}

func (c *AnthropicClient) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
}
