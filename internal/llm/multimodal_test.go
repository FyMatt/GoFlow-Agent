package llm

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestOpenAICompatibleVisionCapabilityRequiresExplicitOptIn(t *testing.T) {
	withoutVision := NewClient(config.LLMConfig{})
	if slices.Contains(withoutVision.Capabilities(), "vision") {
		t.Fatal("OpenAI-compatible clients must not advertise unverified vision support")
	}
	withVision := NewClient(config.LLMConfig{Vision: true})
	if !slices.Contains(withVision.Capabilities(), "vision") {
		t.Fatal("explicitly verified vision providers must advertise vision support")
	}
}

func TestOpenAIRequestUsesTextAndImageBlocks(t *testing.T) {
	request := newOpenAIRequest(schema.ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []schema.Message{{
			Role:    "user",
			Content: "请识别截图中的错误",
			ContentParts: []schema.MessageContentPart{{
				Type:    "image",
				DataURL: "data:image/png;base64,abc",
			}},
		}},
	}, nil)
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded struct {
		Messages []struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL *struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	parts := decoded.Messages[0].Content
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text == "" || parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,abc" {
		t.Fatalf("unexpected content blocks: %#v", parts)
	}
}

func TestSchemaMessageDoesNotPersistDataURL(t *testing.T) {
	message := schema.Message{Role: "user", Content: "截图", ContentParts: []schema.MessageContentPart{{Type: "image", DataURL: "data:image/png;base64,secret", Attachment: &schema.AttachmentRef{Ref: "sha256:abc", Hash: "abc", Name: "screen.png", MIME: "image/png"}}}}
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	if strings.Contains(string(payload), "data:image") || strings.Contains(string(payload), "secret") {
		t.Fatalf("message persisted inline data: %s", payload)
	}
	if !strings.Contains(string(payload), "sha256:abc") {
		t.Fatalf("attachment ref missing: %s", payload)
	}
}
