package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// ipv4FirstDialContext prefers IPv4 for provider connections. Observed outages
// were always the IPv6 path: the TLS handshake completed and then the streamed
// body read failed or stalled for minutes. IPv6 stays as a fallback so hosts
// without IPv4 still work.
func ipv4FirstDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, "tcp4", address)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, "tcp6", address)
	}
}

// Client implements an OpenAI-compatible chat client.
type Client struct {
	baseURL               string
	apiKey                string
	wireAPI               string
	httpClient            *http.Client
	retryCount            int
	retryBackoff          time.Duration
	attemptTimeout        time.Duration
	providerMessageFields map[string]struct{}
	vision                bool
	reasoningEffort       string
	thinking              string
}

// ProviderRetryExhaustedError prevents callers from multiplying the transport's
// completed retry cycle by retrying the entire workflow stage automatically.
type ProviderRetryExhaustedError struct {
	Err error
}

func (e *ProviderRetryExhaustedError) Error() string                  { return e.Err.Error() }
func (e *ProviderRetryExhaustedError) Unwrap() error                  { return e.Err }
func (e *ProviderRetryExhaustedError) ProviderRetriesExhausted() bool { return true }

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
	retryCount := cfg.RetryCount
	if retryCount <= 0 {
		retryCount = 2
	}
	// The configured timeout bounds the whole call, not each attempt: a stalled
	// provider that never returns response headers would otherwise consume the
	// entire budget on the first attempt and leave the retry count unusable.
	// Streaming keeps the full timeout per attempt because a long stream is
	// progress, not a stall.
	attemptTimeout := timeout
	if retryCount > 0 {
		attemptTimeout = timeout / time.Duration(retryCount+1)
	}
	if attemptTimeout <= 0 {
		attemptTimeout = timeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = ipv4FirstDialContext(&net.Dialer{KeepAlive: 30 * time.Second})
	return &Client{
		baseURL:               strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:                cfg.APIKey,
		wireAPI:               normalizeWireAPI(cfg.WireAPI),
		httpClient:            &http.Client{Timeout: timeout, Transport: transport},
		retryCount:            retryCount,
		retryBackoff:          backoff,
		attemptTimeout:        attemptTimeout,
		providerMessageFields: providerMessageFieldSet(cfg.ProviderMessageFields),
		vision:                cfg.Vision,
		reasoningEffort:       config.NormalizeReasoningEffort(cfg.ReasoningEffort),
		thinking:              strings.ToLower(strings.TrimSpace(cfg.Thinking)),
	}
}

// Chat sends a provider-neutral request to an OpenAI-compatible backend.
func (c *Client) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	if err := validateOpenAICompatibleSetup(c, req); err != nil {
		return schema.ChatResponse{}, err
	}
	req.Messages, _ = SanitizeToolMessages(req.Messages)
	if c.wireAPI == "responses" {
		return c.chatResponses(ctx, req)
	}
	payload := c.newOpenAIRequest(req)
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, c.attemptTimeout)
		resp, err := c.doJSON(attemptCtx, body, false)
		cancel()
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
	req.Messages, _ = SanitizeToolMessages(req.Messages)
	if c.wireAPI == "responses" {
		return c.streamResponses(ctx, req, handler)
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
	capabilities := []string{"chat", "stream", "tool_calling", "structured_output"}
	if c != nil && c.vision {
		capabilities = append(capabilities, "vision")
	}
	return capabilities
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
	payload := newOpenAIRequest(req, c.providerMessageFields)
	payload.ReasoningEffort = config.NormalizeReasoningEffort(req.ReasoningEffort)
	if payload.ReasoningEffort == "" {
		payload.ReasoningEffort = c.reasoningEffort
	}
	if c.thinking != "" {
		payload.Thinking = &openAIThinking{Type: c.thinking}
	}
	return payload
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

	resp, err := doStreamingRequest(c.httpClient, httpReq)
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
		progress := len(events) > 0
		for _, choice := range chunk.Choices {
			progress = progress || len(choice.Delta.ToolCalls) > 0
		}
		if progress {
			recordStreamingProgress(resp.Body)
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
	if len(final.ToolCalls) == 0 && final.Message.Content == "" && len(final.Message.ProviderFields) == 0 {
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
	for _, marker := range []string{"access forbidden", "forbidden", "unauthorized", "invalid_api_key", "insufficient_user_quota", "model_not_found", "not supported by any configured account", "permission denied"} {
		if strings.Contains(msg, marker) {
			return false
		}
	}
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "wsarecv") ||
		strings.Contains(msg, "protocol_error") ||
		strings.Contains(msg, "http2: client connection lost") ||
		strings.Contains(msg, "stream error: stream id") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "temporary model failure") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "server misbehaving") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "524")
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
