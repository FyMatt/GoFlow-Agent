package llm

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestSanitizeToolMessagesRewritesOrphanToolObservation(t *testing.T) {
	messages := []schema.Message{
		{Role: "user", Content: "生成 Office 交付物"},
		{Role: "assistant", Content: "", ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "office_tools/create_docx"}}},
		{Role: "tool", ToolCallID: "call-1", Name: "office_tools/create_docx", Content: "created"},
		{Role: "tool", ToolCallID: "call-orphan", Name: "office_tools/create_xlsx", Content: "orphan evidence"},
		{Role: "user", Content: "继续"},
	}
	sanitized, repairs := SanitizeToolMessages(messages)
	if repairs != 1 {
		t.Fatalf("expected one repair, got %d", repairs)
	}
	if len(sanitized) != len(messages) {
		t.Fatalf("sanitizing must not drop observations, got %d messages", len(sanitized))
	}
	if sanitized[3].Role != "user" || sanitized[3].ToolCallID != "" {
		t.Fatalf("orphan tool message must become a user note: %#v", sanitized[3])
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call-1" {
		t.Fatalf("a valid tool response must stay intact: %#v", sanitized[2])
	}
}

func TestSanitizeToolMessagesDropsToolAfterPlainTurn(t *testing.T) {
	messages := []schema.Message{
		{Role: "assistant", Content: "已生成文档"},
		{Role: "tool", ToolCallID: "call-9", Content: "late observation"},
	}
	sanitized, repairs := SanitizeToolMessages(messages)
	if repairs != 1 || sanitized[1].Role != "user" {
		t.Fatalf("tool messages without a preceding assistant call must be rewritten: %#v", sanitized)
	}
}

func TestSanitizeToolMessagesFillsUnansweredToolCalls(t *testing.T) {
	messages := []schema.Message{
		{Role: "user", Content: "生成文档"},
		{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "office_tools/create_docx"}}},
		{Role: "user", Content: "继续"},
	}
	sanitized, repairs := SanitizeToolMessages(messages)
	if repairs != 1 || len(sanitized) != 4 {
		t.Fatalf("an unanswered tool call must get a placeholder response: repairs=%d len=%d", repairs, len(sanitized))
	}
	if sanitized[2].Role != "tool" || sanitized[2].ToolCallID != "call-1" {
		t.Fatalf("placeholder must answer the missing call: %#v", sanitized[2])
	}
}

func TestSanitizeToolMessagesLeavesValidChainsUntouched(t *testing.T) {
	messages := []schema.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []schema.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a", Content: "1"},
		{Role: "tool", ToolCallID: "b", Content: "2"},
	}
	sanitized, repairs := SanitizeToolMessages(messages)
	if repairs != 0 {
		t.Fatalf("valid chain must not be repaired, got %d", repairs)
	}
	for index := range messages {
		if sanitized[index].Role != messages[index].Role {
			t.Fatalf("message %d role changed: %#v", index, sanitized[index])
		}
	}
}
