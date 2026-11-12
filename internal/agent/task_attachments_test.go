package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type attachmentCapabilityClient struct {
	capabilities []string
}

func (c attachmentCapabilityClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, nil
}

func (c attachmentCapabilityClient) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, nil
}

func (c attachmentCapabilityClient) Capabilities() []string {
	return c.capabilities
}

func TestRequireVisionCapabilityRejectsUnsupportedProvider(t *testing.T) {
	messages := []schema.Message{{Role: "user", ContentParts: []schema.MessageContentPart{{Type: "image"}}}}
	err := requireVisionCapability(attachmentCapabilityClient{capabilities: []string{"chat"}}, messages)
	if err == nil || !strings.Contains(err.Error(), "不支持图片识别") {
		t.Fatalf("expected explicit vision error, got %v", err)
	}
	if err := requireVisionCapability(attachmentCapabilityClient{capabilities: []string{"chat", "vision"}}, messages); err != nil {
		t.Fatalf("vision provider rejected: %v", err)
	}
}
