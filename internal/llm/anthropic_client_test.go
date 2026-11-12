package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type anthropicRequestPayload struct {
	Model      string               `json:"model"`
	System     string               `json:"system,omitempty"`
	MaxTokens  int                  `json:"max_tokens,omitempty"`
	Messages   []anthropicMessage   `json:"messages"`
	Tools      []anthropicTool      `json:"tools,omitempty"`
	ToolChoice *anthropicToolChoice `json:"tool_choice,omitempty"`
}

func TestAnthropicClientChatUsesAnthropicMessagesAPI(t *testing.T) {
	var capturedPath string
	var capturedAPIKey string
	var capturedVersion string
	var capturedContentType string
	var payload anthropicRequestPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAPIKey = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		capturedContentType = r.Header.Get("Content-Type")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type": "text", "text": "hello back"}],
			"stop_reason": "end_turn",
			"usage": {
				"input_tokens": 100,
				"output_tokens": 20,
				"cache_creation_input_tokens": 5,
				"cache_read_input_tokens": 15
			}
		}`))
	}))
	defer server.Close()

	client := newTestAnthropicClient(server.URL)
	capabilities := strings.Join(client.Capabilities(), ",")
	if capabilities != "chat,tool_calling,vision" {
		t.Fatalf("unexpected implemented Anthropic capabilities: %q", capabilities)
	}

	resp, err := client.Chat(context.Background(), schema.ChatRequest{
		Model:     "claude-opus-4-6",
		System:    "You are helpful.",
		MaxTokens: 128,
		Messages:  []schema.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedPath != "/messages" || capturedAPIKey != "test-key" || capturedVersion == "" || capturedContentType != "application/json" {
		t.Fatalf("unexpected request metadata: path=%q key=%q version=%q content-type=%q", capturedPath, capturedAPIKey, capturedVersion, capturedContentType)
	}
	if payload.Model != "claude-opus-4-6" || payload.System != "You are helpful." || payload.MaxTokens != 128 {
		t.Fatalf("unexpected request controls: %#v", payload)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != "hello" {
		t.Fatalf("plain text compatibility was lost: %#v", payload.Messages)
	}
	if resp.Message.Content != "hello back" || resp.StopReason != schema.StopReasonStop {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if resp.Usage.PromptTokens != 120 || resp.Usage.OutputTokens != 20 || resp.Usage.CachedTokens != 15 {
		t.Fatalf("unexpected token usage: %#v", resp.Usage)
	}
}

func TestAnthropicClientConvertsVisionAndToolRoundTrip(t *testing.T) {
	var payload anthropicRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [
				{"type":"text","text":"checking"},
				{"type":"tool_use","id":"toolu_response","name":"read_file","input":{"path":"port.go"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":12,"output_tokens":8}
		}`))
	}))
	defer server.Close()

	resp, err := newTestAnthropicClient(server.URL).Chat(context.Background(), schema.ChatRequest{
		Model:      "claude-opus-4-6",
		MaxTokens:  256,
		ToolChoice: "required",
		Tools: []schema.Tool{{
			Name:        "read_file",
			Description: "Read one workspace file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		}},
		Messages: []schema.Message{
			{
				Role:    "user",
				Content: "Inspect the screenshot.",
				ContentParts: []schema.MessageContentPart{
					{Type: "text", Text: "Use its exact requirements."},
					{
						Type:    "image",
						DataURL: "data:image/png;base64,aW1hZ2U=",
						Attachment: &schema.AttachmentRef{
							Ref: "sha256:private-object-reference", Hash: "private-object-reference", Name: "private-screenshot-name.png", MIME: "image/png",
						},
					},
				},
			},
			{
				Role: "assistant",
				ToolCalls: []schema.ToolCall{{
					ID: "toolu_existing", Name: "read_file", Arguments: json.RawMessage(`{"path":"old.go"}`),
				}},
			},
			{Role: "tool", ToolCallID: "toolu_existing", Content: "old contents"},
			{Role: "tool", ToolCallID: "toolu_second", Content: "second result"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "read_file" || string(payload.Tools[0].InputSchema) == "" {
		t.Fatalf("tool schema was not converted: %#v", payload.Tools)
	}
	if payload.ToolChoice == nil || payload.ToolChoice.Type != "any" {
		t.Fatalf("required tool choice was not converted: %#v", payload.ToolChoice)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("expected user, assistant, merged tool-result messages, got %#v", payload.Messages)
	}
	userBlocks := requireAnthropicBlocks(t, payload.Messages[0].Content)
	if len(userBlocks) != 3 || userBlocks[0].Type != "text" || userBlocks[0].Text != "Inspect the screenshot." || userBlocks[1].Text != "Use its exact requirements." {
		t.Fatalf("text/image ordering changed: %#v", userBlocks)
	}
	if userBlocks[2].Type != "image" || userBlocks[2].Source == nil || userBlocks[2].Source.Type != "base64" || userBlocks[2].Source.MediaType != "image/png" || userBlocks[2].Source.Data != "aW1hZ2U=" {
		t.Fatalf("image source was not converted exactly: %#v", userBlocks[2])
	}
	capturedJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal captured request: %v", err)
	}
	if strings.Contains(string(capturedJSON), "private-object-reference") || strings.Contains(string(capturedJSON), "private-screenshot-name") {
		t.Fatalf("attachment storage metadata leaked into Provider request: %s", capturedJSON)
	}
	assistantBlocks := requireAnthropicBlocks(t, payload.Messages[1].Content)
	if len(assistantBlocks) != 1 || assistantBlocks[0].Type != "tool_use" || assistantBlocks[0].ID != "toolu_existing" || assistantBlocks[0].Name != "read_file" || string(assistantBlocks[0].Input) != `{"path":"old.go"}` {
		t.Fatalf("assistant tool_use was not preserved: %#v", assistantBlocks)
	}
	resultBlocks := requireAnthropicBlocks(t, payload.Messages[2].Content)
	if len(resultBlocks) != 2 || resultBlocks[0].Type != "tool_result" || resultBlocks[0].ToolUseID != "toolu_existing" || resultBlocks[0].Content != "old contents" || resultBlocks[1].ToolUseID != "toolu_second" {
		t.Fatalf("tool results were not merged as one user turn: %#v", resultBlocks)
	}
	if resp.Message.Content != "checking" || resp.StopReason != schema.StopReasonToolCalls || len(resp.ToolCalls) != 1 || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("response tool call was not normalized: %#v", resp)
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_response" || call.Name != "read_file" || string(call.Arguments) != `{"path":"port.go"}` {
		t.Fatalf("unexpected normalized tool call: %#v", call)
	}
}

func TestAnthropicClientSupportsSpecificToolChoice(t *testing.T) {
	tools, choice, err := toAnthropicTools([]schema.Tool{
		{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "write_file", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}, "tool:write_file")
	if err != nil || len(tools) != 2 || choice == nil || choice.Type != "tool" || choice.Name != "write_file" {
		t.Fatalf("specific tool choice conversion failed: tools=%#v choice=%#v err=%v", tools, choice, err)
	}
}

func TestAnthropicClientRejectsInvalidInputsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := newTestAnthropicClient(server.URL)

	tests := []struct {
		name string
		req  schema.ChatRequest
		want string
	}{
		{
			name: "remote image URL",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "user", ContentParts: []schema.MessageContentPart{{Type: "image", DataURL: "https://example.invalid/image.png"}}}}},
			want: "inline base64 data URL",
		},
		{
			name: "invalid image base64",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "user", ContentParts: []schema.MessageContentPart{{Type: "image", DataURL: "data:image/png;base64,not!base64"}}}}},
			want: "invalid or empty base64 data",
		},
		{
			name: "unsupported image media type",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "user", ContentParts: []schema.MessageContentPart{{Type: "image", DataURL: "data:image/svg+xml;base64,PHN2Zz4="}}}}},
			want: "unsupported Anthropic image media type",
		},
		{
			name: "invalid tool schema",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "user", Content: "hello"}}, Tools: []schema.Tool{{Name: "bad", InputSchema: json.RawMessage(`[]`)}}},
			want: "input_schema must be a valid JSON object",
		},
		{
			name: "invalid historical tool arguments",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "id", Name: "bad", Arguments: json.RawMessage(`null`)}}}}},
			want: "tool call arguments must be a valid JSON object",
		},
		{
			name: "tool result without call ID",
			req:  schema.ChatRequest{Model: "model", Messages: []schema.Message{{Role: "tool", Content: "result"}}},
			want: "missing tool_call_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.Chat(context.Background(), test.req)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid inputs reached the Provider %d times", got)
	}
}

func TestNormalizeAnthropicResponseRejectsInvalidToolInput(t *testing.T) {
	_, err := normalizeAnthropicResponse(anthropicResponse{Content: []anthropicContentBlock{{
		Type: "tool_use", ID: "toolu_bad", Name: "read_file", Input: json.RawMessage(`"not-an-object"`),
	}}})
	if err == nil || !strings.Contains(err.Error(), "tool input must be a valid JSON object") {
		t.Fatalf("expected invalid response input error, got %v", err)
	}
}

func newTestAnthropicClient(baseURL string) *AnthropicClient {
	return NewAnthropicClient(config.LLMConfig{
		Provider: "anthropic",
		BaseURL:  baseURL,
		APIKey:   "test-key",
		Timeout:  5 * time.Second,
	})
}

func requireAnthropicBlocks(t *testing.T, content any) []anthropicContentBlock {
	t.Helper()
	payload, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal Anthropic content blocks: %v", err)
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(payload, &blocks); err != nil {
		t.Fatalf("expected Anthropic content blocks, got %T (%#v): %v", content, content, err)
	}
	return blocks
}
