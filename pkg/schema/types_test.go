package schema

import "testing"

func TestWithStreamEventCopiesToolDurationFromJSONContent(t *testing.T) {
	result := ToolResult{Content: `{"program":"go","args":["test","./..."],"exit_code":0,"duration_ms":27}`}
	event := result.WithStreamEvent(StreamEvent{Type: StreamEventToolResult, ToolName: "run_command"})
	if event.DurationMs != 27 {
		t.Fatalf("expected duration from tool JSON, got %#v", event)
	}
}

func TestParseToolResultDurationMsRejectsMissingAndNegativeValues(t *testing.T) {
	if got := ParseToolResultDurationMs(`{"exit_code":0}`); got != 0 {
		t.Fatalf("missing duration must be absent, got %d", got)
	}
	if got := ParseToolResultDurationMs(`{"duration_ms":-1}`); got != 0 {
		t.Fatalf("negative duration must be absent, got %d", got)
	}
	if got := ParseToolResultDurationMs(`{"duration_ms":0}`); got != 0 {
		t.Fatalf("zero duration is a valid observation, got %d", got)
	}
}
