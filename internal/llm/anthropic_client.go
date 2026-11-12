package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
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
	Model      string               `json:"model"`
	System     string               `json:"system,omitempty"`
	Messages   []anthropicMessage   `json:"messages"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
	MaxTokens  int                  `json:"max_tokens,omitempty"`
	Stream     bool                 `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     json.RawMessage       `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	IsError   bool                  `json:"is_error,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
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
	retryCount := cfg.RetryCount
	if retryCount <= 0 {
		retryCount = 2
	}
	return &AnthropicClient{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       cfg.APIKey,
		httpClient:   &http.Client{Timeout: timeout},
		retryCount:   retryCount,
		retryBackoff: backoff,
	}
}

func (c *AnthropicClient) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	if err := validateAnthropicSetup(c, req); err != nil {
		return schema.ChatResponse{}, err
	}
	messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	tools, toolChoice, err := toAnthropicTools(req.Tools, req.ToolChoice)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	payload := anthropicRequest{
		Model:      req.Model,
		System:     req.System,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: toolChoice,
		MaxTokens:  req.MaxTokens,
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
	return []string{"chat", "tool_calling", "vision"}
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

func toAnthropicMessages(messages []schema.Message) ([]anthropicMessage, error) {
	converted := make([]anthropicMessage, 0, len(messages))
	for index, message := range messages {
		item, ok, err := toAnthropicMessage(message, index)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		converted = appendAnthropicMessage(converted, item)
	}
	return converted, nil
}

func toAnthropicMessage(message schema.Message, index int) (anthropicMessage, bool, error) {
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role == "system" {
		return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d: system messages must use ChatRequest.System", index)
	}
	if role == "tool" {
		callID := strings.TrimSpace(message.ToolCallID)
		if callID == "" {
			return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d: tool result is missing tool_call_id", index)
		}
		block := anthropicContentBlock{Type: "tool_result", ToolUseID: callID, Content: message.Content}
		return blockAnthropicMessage("user", []anthropicContentBlock{block}), true, nil
	}
	if role != "user" && role != "assistant" {
		return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d: unsupported role %q", index, message.Role)
	}

	blocks := make([]anthropicContentBlock, 0, len(message.ContentParts)+len(message.ToolCalls)+1)
	if strings.TrimSpace(message.Content) != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: message.Content})
	}
	for partIndex, part := range message.ContentParts {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "text":
			if part.Text != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: part.Text})
			}
		case "image", "image_url":
			source, err := parseAnthropicImageDataURL(part.DataURL)
			if err != nil {
				return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d content part %d: %w", index, partIndex, err)
			}
			blocks = append(blocks, anthropicContentBlock{Type: "image", Source: source})
		default:
			return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d content part %d: unsupported type %q", index, partIndex, part.Type)
		}
	}
	if len(message.ToolCalls) > 0 && role != "assistant" {
		return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d: only assistant messages can contain tool calls", index)
	}
	for callIndex, call := range message.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d tool call %d: name is required", index, callIndex)
		}
		input, err := validJSONObject(call.Arguments, "tool call arguments")
		if err != nil {
			return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d tool call %d: %w", index, callIndex, err)
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID, err = newAnthropicToolUseID()
			if err != nil {
				return anthropicMessage{}, false, fmt.Errorf("convert Anthropic message %d tool call %d: %w", index, callIndex, err)
			}
		}
		blocks = append(blocks, anthropicContentBlock{Type: "tool_use", ID: callID, Name: name, Input: input})
	}
	if len(blocks) == 0 {
		return anthropicMessage{}, false, nil
	}
	if len(message.ContentParts) == 0 && len(message.ToolCalls) == 0 {
		return textAnthropicMessage(role, message.Content), true, nil
	}
	return blockAnthropicMessage(role, blocks), true, nil
}

func appendAnthropicMessage(messages []anthropicMessage, next anthropicMessage) []anthropicMessage {
	if len(messages) == 0 || messages[len(messages)-1].Role != next.Role {
		return append(messages, next)
	}
	last := &messages[len(messages)-1]
	blocks := anthropicMessageBlocks(*last)
	blocks = append(blocks, anthropicMessageBlocks(next)...)
	*last = blockAnthropicMessage(last.Role, blocks)
	return messages
}

func anthropicMessageBlocks(message anthropicMessage) []anthropicContentBlock {
	switch content := message.Content.(type) {
	case string:
		return []anthropicContentBlock{{Type: "text", Text: content}}
	case []anthropicContentBlock:
		return append([]anthropicContentBlock(nil), content...)
	default:
		return nil
	}
}

func textAnthropicMessage(role, content string) anthropicMessage {
	return anthropicMessage{Role: role, Content: content}
}

func blockAnthropicMessage(role string, blocks []anthropicContentBlock) anthropicMessage {
	return anthropicMessage{Role: role, Content: blocks}
}

func parseAnthropicImageDataURL(dataURL string) (*anthropicImageSource, error) {
	dataURL = strings.TrimSpace(dataURL)
	metadata, payload, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(metadata), "data:") {
		return nil, fmt.Errorf("image must be an inline base64 data URL")
	}
	mediaSpec := metadata[len("data:"):]
	const base64Suffix = ";base64"
	if !strings.HasSuffix(strings.ToLower(mediaSpec), base64Suffix) {
		return nil, fmt.Errorf("image data URL must use base64 encoding")
	}
	mediaType, parameters, err := mime.ParseMediaType(mediaSpec[:len(mediaSpec)-len(base64Suffix)])
	if err != nil || len(parameters) != 0 {
		return nil, fmt.Errorf("image must use a valid base64 data URL")
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, fmt.Errorf("unsupported Anthropic image media type %q", mediaType)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("image data URL contains invalid or empty base64 data")
	}
	return &anthropicImageSource{Type: "base64", MediaType: mediaType, Data: payload}, nil
}

func toAnthropicTools(tools []schema.Tool, choice string) ([]anthropicTool, *anthropicToolChoice, error) {
	choice = strings.TrimSpace(choice)
	if strings.EqualFold(choice, "none") {
		return nil, nil, nil
	}
	converted := make([]anthropicTool, 0, len(tools))
	for index, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, nil, fmt.Errorf("convert Anthropic tool %d: name is required", index)
		}
		schemaJSON, err := validJSONObject(tool.InputSchema, "input_schema")
		if err != nil {
			return nil, nil, fmt.Errorf("convert Anthropic tool %q: %w", name, err)
		}
		converted = append(converted, anthropicTool{Name: name, Description: tool.Description, InputSchema: schemaJSON})
	}
	if len(converted) == 0 {
		if choice != "" && !strings.EqualFold(choice, "auto") {
			return nil, nil, fmt.Errorf("convert Anthropic tool choice: %q requires at least one tool", choice)
		}
		return nil, nil, nil
	}
	if choice == "" {
		return converted, nil, nil
	}
	switch strings.ToLower(choice) {
	case "auto":
		return converted, &anthropicToolChoice{Type: "auto"}, nil
	case "required", "any":
		return converted, &anthropicToolChoice{Type: "any"}, nil
	default:
		name := strings.TrimSpace(strings.TrimPrefix(choice, "tool:"))
		for _, tool := range converted {
			if tool.Name == name {
				return converted, &anthropicToolChoice{Type: "tool", Name: name}, nil
			}
		}
		return nil, nil, fmt.Errorf("convert Anthropic tool choice: unknown choice or tool %q", choice)
	}
}

func validJSONObject(value json.RawMessage, label string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return nil, fmt.Errorf("%s is required", label)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(value, &decoded); err != nil || decoded == nil {
		return nil, fmt.Errorf("%s must be a valid JSON object", label)
	}
	return append(json.RawMessage(nil), value...), nil
}

func newAnthropicToolUseID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate tool-use ID: %w", err)
	}
	return "toolu_" + hex.EncodeToString(buffer), nil
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
	return normalizeAnthropicResponse(raw)
}

func normalizeAnthropicResponse(resp anthropicResponse) (schema.ChatResponse, error) {
	var content strings.Builder
	toolCalls := make([]schema.ToolCall, 0)
	for index, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			if strings.TrimSpace(block.Name) == "" {
				return schema.ChatResponse{}, fmt.Errorf("decode response tool_use block %d: name is required", index)
			}
			input, err := validJSONObject(block.Input, "tool input")
			if err != nil {
				return schema.ChatResponse{}, fmt.Errorf("decode response tool_use block %d: %w", index, err)
			}
			callID := strings.TrimSpace(block.ID)
			if callID == "" {
				callID, err = newAnthropicToolUseID()
				if err != nil {
					return schema.ChatResponse{}, fmt.Errorf("decode response tool_use block %d: %w", index, err)
				}
			}
			toolCalls = append(toolCalls, schema.ToolCall{ID: callID, Name: block.Name, Arguments: input})
		}
	}
	return schema.ChatResponse{
		Message: schema.Message{
			Role:      "assistant",
			Content:   content.String(),
			ToolCalls: toolCalls,
		},
		ToolCalls:  toolCalls,
		StopReason: normalizeAnthropicStopReason(resp.StopReason),
		Usage: schema.TokenUsage{
			PromptTokens: resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CachedTokens: resp.Usage.CacheReadInputTokens,
		},
	}, nil
}

func normalizeAnthropicStopReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "":
		return ""
	case "end_turn", "stop_sequence":
		return schema.StopReasonStop
	case "tool_use":
		return schema.StopReasonToolCalls
	case "max_tokens":
		return schema.StopReasonMaxTokens
	case "cancelled", "canceled":
		return schema.StopReasonCancelled
	default:
		return reason
	}
}

func (c *AnthropicClient) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
}
