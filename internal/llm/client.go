package llm

import (
	"bufio"
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

// Client implements an OpenAI-compatible chat client.
type Client struct {
	baseURL               string
	apiKey                string
	httpClient            *http.Client
	retryCount            int
	retryBackoff          time.Duration
	providerMessageFields map[string]struct{}
}

// NewClient constructs a new OpenAI-compatible LLM client.
func NewClient(cfg config.LLMConfig) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	return &Client{
		baseURL:               strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:                cfg.APIKey,
		httpClient:            &http.Client{Timeout: timeout},
		retryCount:            cfg.RetryCount,
		retryBackoff:          backoff,
		providerMessageFields: providerMessageFieldSet(cfg.ProviderMessageFields),
	}
}

// Chat sends a provider-neutral request to an OpenAI-compatible backend.
func (c *Client) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	if err := validateOpenAICompatibleSetup(c, req); err != nil {
		return schema.ChatResponse{}, err
	}
	payload := c.newOpenAIRequest(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		resp, err := c.doJSON(ctx, body, false)
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

// StreamChat streams a provider response when supported and returns the assembled final response.
func (c *Client) StreamChat(ctx context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	if err := validateOpenAICompatibleSetup(c, req); err != nil {
		return schema.ChatResponse{}, err
	}
	payload := c.newOpenAIRequest(req)
	payload.Stream = true
	payload.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		resp, err := c.doStream(ctx, body, req, handler)
		if err == nil {
			return resp, nil
		}
		if payload.StreamOptions != nil && isUnsupportedStreamOptionsError(err) {
			payload.StreamOptions = nil
			body, err = json.Marshal(payload)
			if err != nil {
				return schema.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
			}
			resp, err = c.doStream(ctx, body, req, handler)
			if err == nil {
				return resp, nil
			}
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

func (c *Client) Capabilities() []string {
	return []string{"chat", "stream"}
}

func validateOpenAICompatibleSetup(c *Client, req schema.ChatRequest) error {
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

func (c *Client) newOpenAIRequest(req schema.ChatRequest) openAIRequest {
	return newOpenAIRequest(req, c.providerMessageFields)
}

func (c *Client) doJSON(ctx context.Context, body []byte, stream bool) (schema.ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	c.applyHeaders(httpReq)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

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

	var raw openAIResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}
	normalizedResp, err := normalizeResponse(raw, c.providerMessageFields)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	return normalizedResp, nil
}

func (c *Client) doStream(ctx context.Context, body []byte, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	c.applyHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return schema.ChatResponse{}, fmt.Errorf("read error response: %w", readErr)
		}
		return schema.ChatResponse{}, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	reader := bufio.NewReader(resp.Body)
	assembler := newStreamAssembler(c.providerMessageFields)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return schema.ChatResponse{}, fmt.Errorf("read stream chunk: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return schema.ChatResponse{}, fmt.Errorf("decode stream chunk: %w", err)
		}
		events, _, err := assembler.ingest(chunk)
		if err != nil {
			return schema.ChatResponse{}, err
		}
		for _, event := range events {
			if handler != nil {
				if err := handler(event); err != nil {
					return schema.ChatResponse{}, err
				}
			}
		}
	}

	final := assembler.response()
	if err := validateToolCalls(final.ToolCalls); err != nil {
		return c.Chat(ctx, req)
	}
	if len(final.ToolCalls) == 0 && final.Message.Content == "" {
		return c.Chat(ctx, req)
	}
	return final, nil
}

func (c *Client) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504")
}

func isUnsupportedStreamOptionsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "stream_options") || strings.Contains(msg, "include_usage")
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
