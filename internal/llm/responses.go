package llm

// OpenAI Responses API support lives next to the Chat Completions adapter so
// providers can select the wire protocol without changing the agent contract.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type responsesRequest struct {
	Model           string               `json:"model"`
	Instructions    string               `json:"instructions,omitempty"`
	Input           []any                `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
	ToolChoice      string               `json:"tool_choice,omitempty"`
	Text            *responsesTextConfig `json:"text,omitempty"`
	Temperature     float64              `json:"temperature,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesReasoning  `json:"reasoning,omitempty"`
	Stream          bool                 `json:"stream,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesTextConfig struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type string `json:"type"`
}

type responsesResponse struct {
	Status string                `json:"status"`
	Output []responsesOutputItem `json:"output"`
	Usage  responsesUsage        `json:"usage,omitempty"`
	Error  responsesAPIError     `json:"error,omitempty"`
}

type responsesAPIError struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
}

type responsesOutputItem struct {
	Type      string                 `json:"type"`
	Role      string                 `json:"role,omitempty"`
	ID        string                 `json:"id,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments json.RawMessage        `json:"arguments,omitempty"`
	Content   []responsesContentItem `json:"content,omitempty"`
}

type responsesContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	Details      struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
}

func normalizeWireAPI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "responses", "response":
		return "responses"
	default:
		return "chat_completions"
	}
}

func (c *Client) chatResponses(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	payload, err := c.responsesPayload(req)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal Responses request: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, c.attemptTimeout)
		response, err := c.doResponsesJSON(attemptCtx, body)
		cancel()
		if err == nil {
			return response, nil
		}
		if responsesRequiresStreaming(err) {
			return c.streamResponses(ctx, req, nil)
		}
		lastErr = err
		if !isRetryableError(err) || attempt == c.retryCount {
			break
		}
		if err := sleepWithContext(ctx, c.retryBackoff*time.Duration(attempt+1)); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	if ctx.Err() == nil && isRetryableError(lastErr) {
		lastErr = &ProviderRetryExhaustedError{Err: lastErr}
	}
	return schema.ChatResponse{}, lastErr
}

func responsesRequiresStreaming(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "only accepts streaming conversation requests")
}

func (c *Client) doResponsesJSON(ctx context.Context, body []byte) (schema.ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("build Responses request: %w", err)
	}
	c.applyHeaders(httpReq)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("send Responses request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("read Responses response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return schema.ChatResponse{}, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var raw responsesResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("decode Responses response: %w", err)
	}
	return normalizeResponsesResponse(raw)
}

func (c *Client) streamResponses(ctx context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	payload, err := c.responsesPayload(req)
	if err != nil {
		return schema.ChatResponse{}, err
	}
	payload.Stream = true
	body, err := json.Marshal(payload)
	if err != nil {
		return schema.ChatResponse{}, fmt.Errorf("marshal Responses request: %w", err)
	}
	var lastErr error
	maxAttempts := c.retryCount + 1
	requestClient := c
	for attempt := 0; attempt <= c.retryCount; attempt++ {
		attemptNumber := attempt + 1
		if err := emitResponsesProviderStatus(handler, "provider_request_started", fmt.Sprintf("provider request attempt %d/%d started; waiting up to %s for a response", attemptNumber, maxAttempts, c.httpClient.Timeout), "info"); err != nil {
			return schema.ChatResponse{}, err
		}
		response, sawOutput, err := requestClient.doResponsesStream(ctx, body, handler, attemptNumber, maxAttempts)
		if err == nil {
			return response, nil
		}
		lastErr = err
		// Lifecycle and reasoning events have no external side effects. A retry is
		// safe until assistant text or a function call has actually been emitted.
		if sawOutput || (!isRetryableError(err) && !errors.Is(err, io.ErrUnexpectedEOF)) || attempt == c.retryCount {
			break
		}
		if requestClient == c && needsHTTP1Retry(err) {
			if client, closeIdle := http1FallbackClient(c.httpClient); client != nil {
				defer closeIdle()
				fallback := *c
				fallback.httpClient = client
				requestClient = &fallback
				if statusErr := emitResponsesProviderStatus(handler, "provider_request_retry", "模型流式连接停滞或中断，下一次重试尝试 HTTP/1.1。", "warning"); statusErr != nil {
					return schema.ChatResponse{}, statusErr
				}
			}
		}
		delay := c.retryBackoff * time.Duration(attempt+1)
		if statusErr := emitResponsesProviderStatus(handler, "provider_request_retry", fmt.Sprintf("provider request attempt %d/%d failed before assistant output; retrying attempt %d/%d in %s: %s", attemptNumber, maxAttempts, attemptNumber+1, maxAttempts, delay, compactResponsesProviderError(err)), "warning"); statusErr != nil {
			return schema.ChatResponse{}, statusErr
		}
		if err := sleepWithContext(ctx, delay); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	if ctx.Err() == nil && (isRetryableError(lastErr) || errors.Is(lastErr, io.ErrUnexpectedEOF)) {
		lastErr = &ProviderRetryExhaustedError{Err: lastErr}
	}
	return schema.ChatResponse{}, lastErr
}

func (c *Client) doResponsesStream(ctx context.Context, body []byte, handler interfaces.StreamHandler, attempt, maxAttempts int) (schema.ChatResponse, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return schema.ChatResponse{}, false, fmt.Errorf("build Responses stream request: %w", err)
	}
	c.applyHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := doStreamingRequest(c.httpClient, httpReq)
	if err != nil {
		return schema.ChatResponse{}, false, fmt.Errorf("send Responses stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return schema.ChatResponse{}, false, fmt.Errorf("read Responses stream error: %w", readErr)
		}
		return schema.ChatResponse{}, false, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	if err := emitResponsesProviderStatus(handler, "provider_response_connected", fmt.Sprintf("provider request attempt %d/%d connected; waiting for streamed model output", attempt, maxAttempts), "info"); err != nil {
		return schema.ChatResponse{}, false, err
	}
	assembler := newResponsesStreamAssembler()
	reader := bufio.NewReader(resp.Body)
	var eventName string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			eventName = ""
			return nil
		}
		before := assembler.progress
		if err := assembler.ingest(eventName, []byte(data.String()), handler); err != nil {
			return err
		}
		if assembler.progress != before {
			recordStreamingProgress(resp.Body)
		}
		eventName = ""
		data.Reset()
		return nil
	}
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if line == "" {
			if err := flush(); err != nil {
				return schema.ChatResponse{}, assembler.seenOutput, err
			}
			if assembler.terminal {
				break
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if err := flush(); err != nil {
					return schema.ChatResponse{}, assembler.seenOutput, err
				}
				break
			}
			return schema.ChatResponse{}, assembler.seenOutput, fmt.Errorf("read Responses stream: %w", readErr)
		}
	}
	if !assembler.seenEvent {
		return schema.ChatResponse{}, false, fmt.Errorf("Responses stream returned no events")
	}
	if !assembler.terminal {
		return schema.ChatResponse{}, assembler.seenOutput, fmt.Errorf("read Responses stream: %w before terminal response event", io.ErrUnexpectedEOF)
	}
	final := assembler.response()
	if err := validateToolCalls(final.ToolCalls); err != nil {
		return schema.ChatResponse{}, assembler.seenOutput, fmt.Errorf("invalid Responses stream tool arguments: %w", err)
	}
	if err := responsesProviderFailure(assembler.status, assembler.errMsg, assembler.errCode); err != nil && !responsesOutputUsable(final) {
		return schema.ChatResponse{}, assembler.seenOutput, err
	}
	if strings.EqualFold(strings.TrimSpace(assembler.status), "completed") && !responsesOutputUsable(final) {
		return schema.ChatResponse{}, assembler.seenOutput, fmt.Errorf("temporary model failure: responses status completed: empty provider output")
	}
	return final, true, nil
}

func emitResponsesProviderStatus(handler interfaces.StreamHandler, reason, content, severity string) error {
	if handler == nil {
		return nil
	}
	return handler(schema.StreamEvent{Type: schema.StreamEventStatus, Reason: reason, Content: content, Severity: severity})
}

func compactResponsesProviderError(err error) string {
	if err == nil {
		return "unknown provider error"
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 180 {
		return text[:180] + "..."
	}
	return text
}

func (c *Client) responsesPayload(req schema.ChatRequest) (responsesRequest, error) {
	payload, err := newResponsesRequest(req)
	if err != nil {
		return payload, err
	}
	effort := config.NormalizeReasoningEffort(req.ReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(c.reasoningEffort)
	}
	if effort != "" {
		payload.Reasoning = &responsesReasoning{Effort: effort}
	}
	return payload, nil
}

func newResponsesRequest(req schema.ChatRequest) (responsesRequest, error) {
	payload := responsesRequest{
		Model:           req.Model,
		Instructions:    req.System,
		Input:           make([]any, 0, len(req.Messages)),
		ToolChoice:      req.ToolChoice,
		Temperature:     req.Temperature,
		MaxOutputTokens: req.MaxTokens,
	}
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system":
			if strings.TrimSpace(message.Content) != "" {
				if payload.Instructions != "" {
					payload.Instructions += "\n\n"
				}
				payload.Instructions += message.Content
			}
		case "tool":
			callID := strings.TrimSpace(message.ToolCallID)
			if callID == "" {
				return responsesRequest{}, fmt.Errorf("Responses tool result is missing tool_call_id")
			}
			payload.Input = append(payload.Input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  message.Content,
			})
		default:
			if len(message.ToolCalls) > 0 || message.Role == "assistant" {
				if strings.TrimSpace(message.Content) != "" {
					payload.Input = append(payload.Input, responsesMessageItem(message.Role, message.Content, message.ContentParts))
				}
				for _, call := range message.ToolCalls {
					callID := strings.TrimSpace(call.ID)
					if callID == "" {
						callID = "goflow-call-unknown"
					}
					payload.Input = append(payload.Input, map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      call.Name,
						"arguments": string(call.Arguments),
					})
				}
				continue
			}
			payload.Input = append(payload.Input, responsesMessageItem(message.Role, message.Content, message.ContentParts))
		}
	}
	for _, tool := range req.Tools {
		payload.Tools = append(payload.Tools, responsesTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
		})
	}
	if req.ResponseFormat != nil {
		payload.Text = &responsesTextConfig{Format: responsesTextFormat{Type: req.ResponseFormat.Type}}
	}
	return payload, nil
}

func responsesMessageItem(role, content string, parts []schema.MessageContentPart) map[string]any {
	if len(parts) == 0 {
		return map[string]any{"type": "message", "role": role, "content": content}
	}
	contentParts := make([]any, 0, len(parts)+1)
	if strings.TrimSpace(content) != "" {
		contentParts = append(contentParts, map[string]any{"type": "input_text", "text": content})
	}
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "text":
			if part.Text != "" {
				contentParts = append(contentParts, map[string]any{"type": "input_text", "text": part.Text})
			}
		case "image", "image_url":
			if strings.TrimSpace(part.DataURL) != "" {
				contentParts = append(contentParts, map[string]any{"type": "input_image", "image_url": part.DataURL})
			}
		}
	}
	return map[string]any{"type": "message", "role": role, "content": contentParts}
}

func normalizeResponsesResponse(resp responsesResponse) (schema.ChatResponse, error) {
	content := strings.Builder{}
	toolCalls := make([]schema.ToolCall, 0)
	for _, item := range resp.Output {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "function_call":
			args := strings.TrimSpace(rawJSONText(item.Arguments))
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, schema.ToolCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(args)})
		case "message", "output_text":
			if item.Type == "output_text" && len(item.Content) == 0 {
				content.WriteString(itemText(item))
			}
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" || part.Type == "refusal" {
					content.WriteString(part.Text)
				}
			}
		}
	}
	ensureToolCallIDs(toolCalls)
	if err := validateToolCalls(toolCalls); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("invalid Responses tool arguments: %w", err)
	}
	contentText := content.String()
	if len(toolCalls) == 0 {
		if parsed, cleaned, ok := parseDSMLToolCalls(contentText); ok {
			toolCalls = parsed
			contentText = cleaned
		}
	}
	ensureToolCallIDs(toolCalls)
	if err := validateToolCalls(toolCalls); err != nil {
		return schema.ChatResponse{}, fmt.Errorf("invalid Responses tool arguments: %w", err)
	}
	normalized := schema.ChatResponse{
		Message:   schema.Message{Role: "assistant", Content: contentText, ToolCalls: toolCalls},
		ToolCalls: toolCalls,
		Usage:     schema.TokenUsage{PromptTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.Details.CachedTokens},
	}
	if err := responsesProviderFailure(resp.Status, resp.Error.Message, resp.Error.Code); err != nil && !responsesOutputUsable(normalized) {
		return schema.ChatResponse{}, err
	}
	if strings.EqualFold(strings.TrimSpace(resp.Status), "completed") && !responsesOutputUsable(normalized) {
		return schema.ChatResponse{}, fmt.Errorf("temporary model failure: responses status completed: empty provider output")
	}
	stopReason := responsesStopReason(resp.Status, toolCalls)
	if stopReason == "" && len(resp.Output) > 0 {
		stopReason = schema.StopReasonStop
	}
	normalized.StopReason = stopReason
	return normalized, nil
}

func responsesOutputUsable(resp schema.ChatResponse) bool {
	return strings.TrimSpace(resp.Message.Content) != "" || len(resp.ToolCalls) > 0
}

func responsesProviderFailure(status, message, code string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled", "canceled":
		detail := strings.TrimSpace(message)
		if detail == "" {
			detail = strings.TrimSpace(code)
		}
		if detail == "" {
			detail = "empty provider output"
		}
		prefix := "temporary model failure"
		failureText := strings.ToLower(strings.TrimSpace(message + " " + code))
		for _, marker := range []string{"access forbidden", "forbidden", "unauthorized", "invalid_api_key", "insufficient_user_quota", "model_not_found", "not supported by any configured account", "permission denied"} {
			if strings.Contains(failureText, marker) {
				prefix = "provider request failed"
				break
			}
		}
		return fmt.Errorf("%s: responses status %s: %s", prefix, strings.ToLower(strings.TrimSpace(status)), detail)
	default:
		return nil
	}
}

func responsesStopReason(status string, toolCalls []schema.ToolCall) string {
	if len(toolCalls) > 0 {
		return schema.StopReasonToolCalls
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return schema.StopReasonStop
	case "incomplete":
		return schema.StopReasonMaxTokens
	case "failed", "cancelled", "canceled":
		return schema.StopReasonError
	default:
		return normalizeOpenAIStopReason(status)
	}
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func itemText(item responsesOutputItem) string {
	if len(item.Content) == 0 {
		return ""
	}
	return item.Content[0].Text
}

type responsesStreamCall struct {
	ID        string
	CallID    string
	Name      string
	Arguments strings.Builder
	Announced bool
}

type responsesStreamAssembler struct {
	content    strings.Builder
	calls      []*responsesStreamCall
	byItemID   map[string]*responsesStreamCall
	usage      responsesUsage
	status     string
	errMsg     string
	errCode    string
	seenEvent  bool
	seenOutput bool
	terminal   bool
	progress   int
}

func newResponsesStreamAssembler() *responsesStreamAssembler {
	return &responsesStreamAssembler{byItemID: make(map[string]*responsesStreamCall)}
}

func (a *responsesStreamAssembler) ingest(event string, data []byte, handler interfaces.StreamHandler) error {
	if string(data) == "[DONE]" {
		return nil
	}
	var envelope struct {
		Type     string              `json:"type"`
		Delta    string              `json:"delta,omitempty"`
		ItemID   string              `json:"item_id,omitempty"`
		Item     responsesOutputItem `json:"item,omitempty"`
		Response responsesResponse   `json:"response,omitempty"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode Responses stream event: %w", err)
	}
	a.seenEvent = true
	eventType := strings.TrimSpace(envelope.Type)
	if eventType == "" {
		eventType = strings.TrimSpace(event)
	}
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning.delta", "response.thinking.delta", "response.function_call_arguments.delta":
		if envelope.Delta != "" {
			a.progress++
		}
	case "response.output_item.added", "response.output_item.done":
		if envelope.Item.Type != "" {
			a.progress++
		}
	}
	switch eventType {
	case "response.output_text.delta", "response.refusal.delta":
		if envelope.Delta != "" {
			a.seenOutput = true
			a.content.WriteString(envelope.Delta)
			if handler != nil {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: envelope.Delta}); err != nil {
					return err
				}
			}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning.delta", "response.thinking.delta":
		if envelope.Delta != "" && handler != nil {
			if err := handler(schema.StreamEvent{Type: schema.StreamEventReasoning, Content: envelope.Delta}); err != nil {
				return err
			}
		}
	case "response.output_item.added":
		a.addCall(envelope.Item, handler)
	case "response.function_call_arguments.delta":
		call := a.byItemID[envelope.ItemID]
		if call == nil && len(a.calls) > 0 {
			call = a.calls[len(a.calls)-1]
		}
		if call != nil {
			call.Arguments.WriteString(envelope.Delta)
		}
	case "response.output_item.done":
		call := a.byItemID[envelope.Item.ID]
		if call == nil && strings.EqualFold(envelope.Item.Type, "function_call") {
			call = a.addCall(envelope.Item, handler)
		}
		if call != nil {
			if call.CallID == "" {
				call.CallID = envelope.Item.CallID
			}
			if call.Name == "" {
				call.Name = envelope.Item.Name
			}
			if args := rawJSONText(envelope.Item.Arguments); args != "" {
				call.Arguments.Reset()
				call.Arguments.WriteString(args)
			}
		}
	case "response.completed", "response.incomplete", "response.failed":
		a.terminal = true
		a.status = envelope.Response.Status
		a.usage = envelope.Response.Usage
		if message := strings.TrimSpace(envelope.Response.Error.Message); message != "" {
			a.errMsg = message
		}
		if code := strings.TrimSpace(envelope.Response.Error.Code); code != "" {
			a.errCode = code
		}
	}
	return nil
}

func (a *responsesStreamAssembler) addCall(item responsesOutputItem, handler interfaces.StreamHandler) *responsesStreamCall {
	if !strings.EqualFold(item.Type, "function_call") {
		return nil
	}
	a.seenOutput = true
	call := a.byItemID[item.ID]
	if call == nil {
		call = &responsesStreamCall{ID: item.ID, CallID: item.CallID, Name: item.Name}
		a.calls = append(a.calls, call)
		if item.ID != "" {
			a.byItemID[item.ID] = call
		}
	}
	if !call.Announced && handler != nil {
		call.Announced = true
		if err := handler(schema.StreamEvent{Type: schema.StreamEventToolCall, ToolName: call.Name, ToolCallID: call.CallID}); err != nil {
			return call
		}
	}
	return call
}

func (a *responsesStreamAssembler) response() schema.ChatResponse {
	toolCalls := make([]schema.ToolCall, 0, len(a.calls))
	for _, call := range a.calls {
		args := strings.TrimSpace(call.Arguments.String())
		if args == "" {
			args = "{}"
		}
		toolCalls = append(toolCalls, schema.ToolCall{ID: call.CallID, Name: call.Name, Arguments: json.RawMessage(args)})
	}
	ensureToolCallIDs(toolCalls)
	content := a.content.String()
	if len(toolCalls) == 0 {
		if parsed, cleaned, ok := parseDSMLToolCalls(content); ok {
			toolCalls = parsed
			content = cleaned
		}
	}
	return schema.ChatResponse{
		Message:    schema.Message{Role: "assistant", Content: content, ToolCalls: toolCalls},
		StopReason: responsesStopReason(a.status, toolCalls),
		ToolCalls:  toolCalls,
		Usage:      schema.TokenUsage{PromptTokens: a.usage.InputTokens, OutputTokens: a.usage.OutputTokens, CachedTokens: a.usage.Details.CachedTokens},
	}
}
