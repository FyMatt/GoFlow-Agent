package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func guardTestCall(id, name string, args map[string]any) schema.ToolCall {
	encoded, _ := json.Marshal(args)
	return schema.ToolCall{ID: id, Name: name, Arguments: encoded}
}

func TestBlockRepeatedFailingCallsBlocksIdenticalRepeat(t *testing.T) {
	call := guardTestCall("c1", "file_tools/read_file", map[string]any{"path": "missing.go"})
	failures := map[string]int{failedCallSignature(call): repeatedFailureLimit}

	allowed, blocked := blockRepeatedFailingCalls([]schema.ToolCall{call}, failures)
	if len(allowed) != 0 {
		t.Fatalf("expected the repeated failing call to be blocked, got %d allowed", len(allowed))
	}
	result, ok := blocked["c1"]
	if !ok || !result.IsError {
		t.Fatalf("expected an error result for the blocked call, got %#v", blocked)
	}
	if !strings.Contains(result.Content, "cannot succeed again") {
		t.Fatalf("blocked result must explain why, got %q", result.Content)
	}
}

func TestBlockRepeatedFailingCallsAllowsDifferentArguments(t *testing.T) {
	failed := guardTestCall("c1", "file_tools/read_file", map[string]any{"path": "missing.go"})
	fresh := guardTestCall("c2", "file_tools/read_file", map[string]any{"path": "present.go"})
	failures := map[string]int{failedCallSignature(failed): repeatedFailureLimit}

	allowed, blocked := blockRepeatedFailingCalls([]schema.ToolCall{fresh}, failures)
	if len(allowed) != 1 || len(blocked) != 0 {
		t.Fatalf("a different argument set must be allowed, got allowed=%d blocked=%d", len(allowed), len(blocked))
	}
}

func TestRecordFailedCallSignaturesCountsOnlyErrors(t *testing.T) {
	call := guardTestCall("c1", "file_tools/read_file", map[string]any{"path": "missing.go"})
	other := guardTestCall("c2", "file_tools/read_file", map[string]any{"path": "present.go"})
	failures := map[string]int{}
	results := []schema.ToolResult{
		{CallID: "c1", ToolName: "file_tools/read_file", IsError: true},
		{CallID: "c2", ToolName: "file_tools/read_file", IsError: false},
	}
	recordFailedCallSignatures(results, []schema.ToolCall{call, other}, failures)
	if failures[failedCallSignature(call)] != 1 {
		t.Fatalf("expected one recorded failure, got %#v", failures)
	}
	if failures[failedCallSignature(other)] != 0 {
		t.Fatalf("successful calls must not count as failures, got %#v", failures)
	}
}

func TestRepeatedFailureNudgeNamesBlockedTools(t *testing.T) {
	blocked := map[string]schema.ToolResult{
		"c1": {ToolName: "file_tools/read_file", IsError: true},
	}
	nudge := repeatedFailureNudge(blocked)
	if !strings.Contains(nudge, "file_tools/read_file") {
		t.Fatalf("nudge must name the blocked tool, got %q", nudge)
	}
}
