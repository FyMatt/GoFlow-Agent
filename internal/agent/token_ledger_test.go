package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestClassifyLLMOutput(t *testing.T) {
	kind, tools := classifyLLMOutput(schema.ChatResponse{
		Message:    schema.Message{Content: "hello"},
		StopReason: schema.StopReasonStop,
	})
	if kind != schema.LLMOutputKindText || len(tools) != 0 {
		t.Fatalf("expected text output, got %s %#v", kind, tools)
	}

	kind, tools = classifyLLMOutput(schema.ChatResponse{
		ToolCalls:  []schema.ToolCall{{Name: "read_file"}, {Name: "read_file"}, {Name: "write_file"}},
		StopReason: schema.StopReasonToolCalls,
	})
	if kind != schema.LLMOutputKindToolCalls || strings.Join(tools, ",") != "read_file,write_file" {
		t.Fatalf("expected tool_calls, got %s %#v", kind, tools)
	}

	kind, tools = classifyLLMOutput(schema.ChatResponse{
		Message:   schema.Message{Content: "calling tools"},
		ToolCalls: []schema.ToolCall{{Name: "search_files"}},
	})
	if kind != schema.LLMOutputKindMixed || strings.Join(tools, ",") != "search_files" {
		t.Fatalf("expected mixed, got %s %#v", kind, tools)
	}
}

func TestCacheHitRate(t *testing.T) {
	if got := cacheHitRate(schema.TokenUsage{PromptTokens: 100, CachedTokens: 25}); got != 0.25 {
		t.Fatalf("expected 0.25, got %v", got)
	}
	if got := cacheHitRate(schema.TokenUsage{PromptTokens: 0, CachedTokens: 10}); got != 0 {
		t.Fatalf("expected 0 without prompt tokens, got %v", got)
	}
}

func TestEmitTokenUsageRecordsOutputKindAndCacheHit(t *testing.T) {
	state := session.New(4)
	var events []schema.StreamEvent
	emitTokenUsage(func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	}, state, "planner", "plan", schema.ChatResponse{
		Message:    schema.Message{Content: "plan ready"},
		StopReason: schema.StopReasonStop,
		Usage:      schema.TokenUsage{PromptTokens: 80, OutputTokens: 20, CachedTokens: 40},
	})
	if len(events) != 1 {
		t.Fatalf("expected one token usage event, got %#v", events)
	}
	event := events[0]
	if event.Type != schema.StreamEventTokenUsage || event.OutputKind != schema.LLMOutputKindText || event.CacheHitRate != 0.5 || event.StopReason != schema.StopReasonStop {
		t.Fatalf("unexpected token usage event: %#v", event)
	}
	snapshot := state.Snapshot()
	if len(snapshot.TokenUsages) != 1 {
		t.Fatalf("expected session sample, got %#v", snapshot.TokenUsages)
	}
	sample := snapshot.TokenUsages[0]
	if sample.CallIndex != 1 || sample.OutputKind != schema.LLMOutputKindText || sample.CacheHitRate != 0.5 || sample.PromptTokens != 80 {
		t.Fatalf("unexpected session sample: %#v", sample)
	}

	emitTokenUsage(nil, state, "fixer", "fix", schema.ChatResponse{
		StopReason: schema.StopReasonToolCalls,
		ToolCalls:  []schema.ToolCall{{Name: "read_file"}},
		Usage:      schema.TokenUsage{PromptTokens: 10, OutputTokens: 2, CachedTokens: 1},
	})
	snapshot = state.Snapshot()
	if len(snapshot.TokenUsages) != 2 {
		t.Fatalf("expected second session sample, got %#v", snapshot.TokenUsages)
	}
	sample = snapshot.TokenUsages[1]
	if sample.CallIndex != 2 || sample.OutputKind != schema.LLMOutputKindToolCalls || strings.Join(sample.ToolNames, ",") != "read_file" {
		t.Fatalf("expected tool_calls sample, got %#v", sample)
	}
}
