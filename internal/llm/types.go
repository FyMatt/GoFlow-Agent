package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

var fallbackToolCallSequence atomic.Uint64

type openAIRequest struct {
	Model           string                `json:"model"`
	Messages        []openAIMessage       `json:"messages"`
	Tools           []openAITool          `json:"tools,omitempty"`
	ToolChoice      string                `json:"tool_choice,omitempty"`
	ResponseFormat  *openAIResponseFormat `json:"response_format,omitempty"`
	Temperature     float64               `json:"temperature,omitempty"`
	MaxTokens       int                   `json:"max_tokens,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	Thinking        *openAIThinking       `json:"thinking,omitempty"`
	Stream          bool                  `json:"stream,omitempty"`
	StreamOptions   *openAIStreamOptions  `json:"stream_options,omitempty"`
}

type openAIThinking struct {
	Type string `json:"type"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIMessage struct {
	Role           string              `json:"role"`
	Content        string              `json:"-"`
	ContentParts   []openAIContentPart `json:"-"`
	Name           string              `json:"name,omitempty"`
	ToolCalls      []openAIToolCall    `json:"tool_calls,omitempty"`
	ToolCallID     string              `json:"tool_call_id,omitempty"`
	ProviderFields providerFields      `json:"-"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

func (m openAIMessage) MarshalJSON() ([]byte, error) {
	payload := make(map[string]any)
	payload["role"] = m.Role
	if len(m.ContentParts) > 0 {
		payload["content"] = m.ContentParts
	} else if m.Content != "" {
		payload["content"] = m.Content
	}
	if m.Name != "" {
		payload["name"] = m.Name
	}
	if len(m.ToolCalls) > 0 {
		payload["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		payload["tool_call_id"] = m.ToolCallID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return mergeProviderFields(data, m.ProviderFields)
}

func (m *openAIMessage) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Role       string           `json:"role"`
		Content    json.RawMessage  `json:"content"`
		Name       string           `json:"name,omitempty"`
		ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	m.Role = decoded.Role
	m.Name = decoded.Name
	m.ToolCalls = decoded.ToolCalls
	m.ToolCallID = decoded.ToolCallID
	if len(decoded.Content) > 0 && string(decoded.Content) != "null" {
		if err := json.Unmarshal(decoded.Content, &m.Content); err != nil {
			var parts []openAIContentPart
			if partsErr := json.Unmarshal(decoded.Content, &parts); partsErr != nil {
				return err
			}
			m.ContentParts = parts
			for _, part := range parts {
				if part.Type == "text" {
					m.Content += part.Text
				}
			}
		}
	}
	m.ProviderFields = extractProviderFields(data, nil)
	return nil
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
			Role           string                 `json:"role"`
			Content        string                 `json:"content"`
			ToolCalls      []openAIStreamToolCall `json:"tool_calls"`
			ProviderFields providerFields         `json:"-"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage,omitempty"`
}

func (c *openAIStreamChunk) UnmarshalJSON(data []byte) error {
	var raw struct {
		Choices []struct {
			Delta        json.RawMessage `json:"delta"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage openAIUsage `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Usage = raw.Usage
	c.Choices = make([]struct {
		Delta struct {
			Role           string                 `json:"role"`
			Content        string                 `json:"content"`
			ToolCalls      []openAIStreamToolCall `json:"tool_calls"`
			ProviderFields providerFields         `json:"-"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	}, len(raw.Choices))
	for i, choice := range raw.Choices {
		c.Choices[i].FinishReason = choice.FinishReason
		if len(choice.Delta) == 0 {
			continue
		}
		var delta struct {
			Role      string                 `json:"role"`
			Content   string                 `json:"content"`
			ToolCalls []openAIStreamToolCall `json:"tool_calls"`
		}
		if err := json.Unmarshal(choice.Delta, &delta); err != nil {
			return err
		}
		c.Choices[i].Delta.Role = delta.Role
		c.Choices[i].Delta.Content = delta.Content
		c.Choices[i].Delta.ToolCalls = delta.ToolCalls
		c.Choices[i].Delta.ProviderFields = extractProviderFields(choice.Delta, nil)
	}
	return nil
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
	role                  string
	content               strings.Builder
	toolCalls             []partialToolCall
	providerFields        map[string]*strings.Builder
	allowedProviderFields map[string]struct{}
	finishReason          string
	usage                 openAIUsage
}

type providerFields map[string]json.RawMessage

var reservedOpenAIMessageFields = map[string]struct{}{
	"role":         {},
	"content":      {},
	"name":         {},
	"tool_call_id": {},
	"tool_call":    {},
	"tool_calls":   {},
}

func providerMessageFieldSet(fields []string) map[string]struct{} {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !isProviderMessageFieldName(field) {
			continue
		}
		out[field] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func newOpenAIRequest(req schema.ChatRequest, allowedProviderFields map[string]struct{}) openAIRequest {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.System})
	}
	for _, msg := range req.Messages {
		contentParts := toOpenAIContentParts(msg)
		messages = append(messages, openAIMessage{
			Role:           msg.Role,
			Content:        msg.Content,
			ContentParts:   contentParts,
			Name:           msg.Name,
			ToolCallID:     msg.ToolCallID,
			ToolCalls:      toOpenAIToolCalls(msg.ToolCalls),
			ProviderFields: providerFieldsForRequest(msg.ProviderFields, allowedProviderFields),
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

	var responseFormat *openAIResponseFormat
	if req.ResponseFormat != nil {
		responseFormat = &openAIResponseFormat{Type: req.ResponseFormat.Type}
	}
	return openAIRequest{
		Model:          req.Model,
		Messages:       messages,
		Tools:          tools,
		ToolChoice:     req.ToolChoice,
		ResponseFormat: responseFormat,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
	}
}

func toOpenAIContentParts(message schema.Message) []openAIContentPart {
	if len(message.ContentParts) == 0 {
		return nil
	}
	parts := make([]openAIContentPart, 0, len(message.ContentParts)+1)
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, openAIContentPart{Type: "text", Text: message.Content})
	}
	for _, part := range message.ContentParts {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "text":
			if part.Text != "" {
				parts = append(parts, openAIContentPart{Type: "text", Text: part.Text})
			}
		case "image", "image_url":
			if strings.TrimSpace(part.DataURL) != "" {
				parts = append(parts, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: part.DataURL}})
			}
		}
	}
	return parts
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

func normalizeResponse(resp openAIResponse, allowedProviderFields map[string]struct{}) (schema.ChatResponse, error) {
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
	content := choice.Message.Content
	if len(toolCalls) == 0 {
		if parsed, cleaned, ok := parseDSMLToolCalls(content); ok {
			toolCalls = parsed
			content = cleaned
		}
	}
	ensureToolCallIDs(toolCalls)
	if err := validateToolCalls(toolCalls); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return schema.ChatResponse{
		Message: schema.Message{
			Role:           choice.Message.Role,
			Content:        content,
			ToolCalls:      toolCalls,
			ProviderFields: providerFieldsForSchema(choice.Message.ProviderFields, allowedProviderFields),
		},
		StopReason: normalizedStopReason(choice.FinishReason, toolCalls),
		ToolCalls:  toolCalls,
		Usage:      resp.Usage.tokenUsage(),
	}, nil
}

func normalizeOpenAIStopReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "":
		return ""
	case "stop":
		return schema.StopReasonStop
	case "tool_calls", "function_call":
		return schema.StopReasonToolCalls
	case "length", "max_tokens":
		return schema.StopReasonMaxTokens
	case "cancelled", "canceled":
		return schema.StopReasonCancelled
	default:
		return reason
	}
}

func normalizedStopReason(reason string, toolCalls []schema.ToolCall) string {
	if len(toolCalls) > 0 {
		return schema.StopReasonToolCalls
	}
	return normalizeOpenAIStopReason(reason)
}

func newStreamAssembler(allowedProviderFields map[string]struct{}) *streamAssembler {
	return &streamAssembler{allowedProviderFields: allowedProviderFields}
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
	if delta := reasoningDeltaFromFields(choice.Delta.ProviderFields); delta != "" {
		events = append(events, schema.StreamEvent{Type: schema.StreamEventReasoning, Content: delta})
	}
	a.appendProviderFields(choice.Delta.ProviderFields)
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

func (a *streamAssembler) appendProviderFields(fields providerFields) {
	if len(fields) == 0 || len(a.allowedProviderFields) == 0 {
		return
	}
	for key, raw := range fields {
		if _, ok := a.allowedProviderFields[key]; !ok {
			continue
		}
		if len(raw) == 0 {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			continue
		}
		if a.providerFields == nil {
			a.providerFields = make(map[string]*strings.Builder)
		}
		builder := a.providerFields[key]
		if builder == nil {
			builder = &strings.Builder{}
			a.providerFields[key] = builder
		}
		builder.WriteString(value)
	}
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
	content := a.content.String()
	if len(toolCalls) == 0 {
		if parsed, cleaned, ok := parseDSMLToolCalls(content); ok {
			toolCalls = parsed
			content = cleaned
		}
	}
	ensureToolCallIDs(toolCalls)
	return schema.ChatResponse{
		Message: schema.Message{
			Role:           defaultString(a.role, "assistant"),
			Content:        content,
			ToolCalls:      toolCalls,
			ProviderFields: streamProviderFieldsForSchema(a.providerFields, a.allowedProviderFields),
		},
		StopReason: normalizedStopReason(a.finishReason, toolCalls),
		ToolCalls:  toolCalls,
		Usage:      a.usage.tokenUsage(),
	}
}

func ensureToolCallIDs(calls []schema.ToolCall) {
	for index := range calls {
		if strings.TrimSpace(calls[index].ID) != "" {
			continue
		}
		calls[index].ID = fmt.Sprintf("goflow-call-%d", fallbackToolCallSequence.Add(1))
	}
}

func providerFieldsForRequest(fields map[string]json.RawMessage, allowed map[string]struct{}) providerFields {
	if len(fields) == 0 || len(allowed) == 0 {
		return nil
	}
	out := make(providerFields, len(fields))
	for key, value := range fields {
		if _, ok := allowed[key]; !ok {
			continue
		}
		if !isProviderMessageFieldName(key) {
			continue
		}
		if len(value) == 0 {
			continue
		}
		out[key] = append(json.RawMessage(nil), value...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerFieldsForSchema(fields providerFields, allowed map[string]struct{}) map[string]json.RawMessage {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		if len(allowed) == 0 {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		if !isProviderMessageFieldName(key) {
			continue
		}
		if len(value) == 0 {
			continue
		}
		out[key] = append(json.RawMessage(nil), value...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func streamProviderFieldsForSchema(fields map[string]*strings.Builder, allowed map[string]struct{}) map[string]json.RawMessage {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(fields))
	for key, builder := range fields {
		if builder == nil || builder.Len() == 0 {
			continue
		}
		if len(allowed) == 0 {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		if !isProviderMessageFieldName(key) {
			continue
		}
		raw, err := json.Marshal(builder.String())
		if err != nil {
			continue
		}
		out[key] = raw
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reasoningDeltaFromFields(fields providerFields) string {
	if len(fields) == 0 {
		return ""
	}
	for _, key := range []string{"reasoning_content", "reasoning", "thinking", "reasoning_text"} {
		raw, ok := fields[key]
		if !ok || len(raw) == 0 {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil && value != "" {
			return value
		}
		var object struct {
			Content string `json:"content"`
			Text    string `json:"text"`
		}
		if err := json.Unmarshal(raw, &object); err == nil {
			if object.Content != "" {
				return object.Content
			}
			if object.Text != "" {
				return object.Text
			}
		}
	}
	return ""
}

func extractProviderFields(data []byte, allowed map[string]struct{}) providerFields {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	fields := make(providerFields)
	for key, value := range raw {
		if len(allowed) > 0 {
			if _, ok := allowed[key]; !ok {
				continue
			}
		}
		if !isProviderMessageFieldName(key) {
			continue
		}
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		fields[key] = append(json.RawMessage(nil), value...)
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func mergeProviderFields(data []byte, fields providerFields) ([]byte, error) {
	if len(fields) == 0 {
		return data, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	for key, value := range fields {
		if !isProviderMessageFieldName(key) {
			continue
		}
		if len(value) == 0 {
			continue
		}
		payload[key] = append(json.RawMessage(nil), value...)
	}
	return json.Marshal(payload)
}

func isProviderMessageFieldName(field string) bool {
	field = strings.TrimSpace(field)
	if field == "" || len(field) > 128 {
		return false
	}
	if _, reserved := reservedOpenAIMessageFields[field]; reserved {
		return false
	}
	hasLetter := false
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z':
			hasLetter = true
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return hasLetter
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
