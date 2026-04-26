package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Temperature   float64              `json:"temperature,omitempty"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResponse struct {
	Choices []struct {
		FinishReason string        `json:"finish_reason"`
		Message      openAIMessage `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage,omitempty"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string                 `json:"role"`
			Content   string                 `json:"content"`
			ToolCalls []openAIStreamToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage,omitempty"`
}

type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens,omitempty"`
	CompletionTokens    int `json:"completion_tokens,omitempty"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

type openAIStreamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type partialToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
	Announced bool
}

type streamAssembler struct {
	role         string
	content      strings.Builder
	toolCalls    []partialToolCall
	finishReason string
	usage        openAIUsage
}

func newOpenAIRequest(req schema.ChatRequest) openAIRequest {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.System})
	}
	for _, msg := range req.Messages {
		messages = append(messages, openAIMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
		})
	}

	tools := make([]openAITool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	return openAIRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
}

func toOpenAIToolCalls(calls []schema.ToolCall) []openAIToolCall {
	converted := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		converted = append(converted, openAIToolCall{
			ID:   call.ID,
			Type: "function",
			Function: openAIToolFunction{
				Name:      call.Name,
				Arguments: string(call.Arguments),
			},
		})
	}
	return converted
}

func normalizeResponse(resp openAIResponse) (schema.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return schema.ChatResponse{}, nil
	}
	choice := resp.Choices[0]
	toolCalls := make([]schema.ToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, schema.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	if err := validateToolCalls(toolCalls); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return schema.ChatResponse{
		Message: schema.Message{
			Role:      choice.Message.Role,
			Content:   choice.Message.Content,
			ToolCalls: toolCalls,
		},
		StopReason: choice.FinishReason,
		ToolCalls:  toolCalls,
		Usage:      resp.Usage.tokenUsage(),
	}, nil
}

func newStreamAssembler() *streamAssembler {
	return &streamAssembler{}
}

func (a *streamAssembler) ingest(chunk openAIStreamChunk) ([]schema.StreamEvent, bool, error) {
	if chunk.Usage.hasUsage() {
		a.usage = chunk.Usage
	}
	if len(chunk.Choices) == 0 {
		return nil, false, nil
	}
	choice := chunk.Choices[0]
	events := make([]schema.StreamEvent, 0)
	if choice.Delta.Role != "" {
		a.role = choice.Delta.Role
	}
	if choice.Delta.Content != "" {
		a.content.WriteString(choice.Delta.Content)
		events = append(events, schema.StreamEvent{Type: schema.StreamEventText, Content: choice.Delta.Content})
	}
	for _, call := range choice.Delta.ToolCalls {
		for len(a.toolCalls) <= call.Index {
			a.toolCalls = append(a.toolCalls, partialToolCall{})
		}
		current := &a.toolCalls[call.Index]
		if call.ID != "" {
			current.ID = call.ID
		}
		if call.Function.Name != "" {
			current.Name = call.Function.Name
		}
		if call.Function.Arguments != "" {
			current.Arguments.WriteString(call.Function.Arguments)
		}
		if current.Name != "" && !current.Announced {
			current.Announced = true
			events = append(events, schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: current.Name, ToolCallID: current.ID})
		}
	}
	if choice.FinishReason != "" {
		a.finishReason = choice.FinishReason
		return events, true, nil
	}
	return events, false, nil
}

func (a *streamAssembler) response() schema.ChatResponse {
	toolCalls := make([]schema.ToolCall, 0, len(a.toolCalls))
	for _, call := range a.toolCalls {
		args := strings.TrimSpace(call.Arguments.String())
		if args == "" {
			args = "{}"
		}
		toolCalls = append(toolCalls, schema.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: json.RawMessage(args),
		})
	}
	return schema.ChatResponse{
		Message: schema.Message{
			Role:      defaultString(a.role, "assistant"),
			Content:   a.content.String(),
			ToolCalls: toolCalls,
		},
		StopReason: a.finishReason,
		ToolCalls:  toolCalls,
		Usage:      a.usage.tokenUsage(),
	}
}

func (u openAIUsage) hasUsage() bool {
	return u.PromptTokens != 0 || u.CompletionTokens != 0 || u.PromptTokensDetails.CachedTokens != 0
}

func (u openAIUsage) tokenUsage() schema.TokenUsage {
	return schema.TokenUsage{
		PromptTokens: u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		CachedTokens: u.PromptTokensDetails.CachedTokens,
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func validateToolCalls(calls []schema.ToolCall) error {
	for _, call := range calls {
		if call.Name == "" {
			return fmt.Errorf("streamed tool call missing function name")
		}
		if len(call.Arguments) == 0 {
			call.Arguments = json.RawMessage("{}")
		}
		var args map[string]any
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return fmt.Errorf("tool call has invalid arguments: %w", err)
		}
	}
	return nil
}
