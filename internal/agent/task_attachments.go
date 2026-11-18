package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type taskAttachmentsContextKey struct{}

// WithTaskAttachments associates already validated attachment references with
// a task. Binary content stays in the workspace attachment store.
func WithTaskAttachments(ctx context.Context, attachments []schema.AttachmentRef) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(attachments) == 0 {
		return ctx
	}
	copied := append([]schema.AttachmentRef(nil), attachments...)
	return context.WithValue(ctx, taskAttachmentsContextKey{}, copied)
}

func requireVisionCapability(client interfaces.LLMClient, messages []schema.Message) error {
	needsVision := false
	for _, message := range messages {
		for _, part := range message.ContentParts {
			if strings.EqualFold(strings.TrimSpace(part.Type), "image") || strings.EqualFold(strings.TrimSpace(part.Type), "image_url") {
				needsVision = true
				break
			}
		}
	}
	if !needsVision {
		return nil
	}
	if client == nil {
		return fmt.Errorf("当前模型连接不可用，无法识别图片")
	}
	for _, capability := range client.Capabilities() {
		if strings.EqualFold(strings.TrimSpace(capability), "vision") {
			return nil
		}
	}
	return fmt.Errorf("当前模型连接不支持图片识别，请更换支持视觉输入的模型后重试")
}

func taskAttachmentsFromContext(ctx context.Context) []schema.AttachmentRef {
	if ctx == nil {
		return nil
	}
	attachments, _ := ctx.Value(taskAttachmentsContextKey{}).([]schema.AttachmentRef)
	return append([]schema.AttachmentRef(nil), attachments...)
}

func taskInputMessage(input string, attachments []schema.AttachmentRef) schema.Message {
	message := schema.Message{Role: "user", Content: input}
	for i := range attachments {
		attachment := attachments[i]
		if !strings.EqualFold(strings.TrimSpace(attachment.Kind), "image") {
			continue
		}
		message.ContentParts = append(message.ContentParts, schema.MessageContentPart{
			Type:       "image",
			Attachment: &attachment,
		})
	}
	return message
}
