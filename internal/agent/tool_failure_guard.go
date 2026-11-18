package agent

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// repeatedFailureLimit is how many times one identical call may fail before the
// runtime refuses to execute it again. The same arguments on the same failing
// tool cannot produce a different outcome, so every retry is a wasted turn.
const repeatedFailureLimit = 2

// failedCallSignature identifies one tool call by name and arguments.
func failedCallSignature(call schema.ToolCall) string {
	return strings.ToLower(strings.TrimSpace(call.Name)) + "\x00" + toolArgumentsHash(call.Arguments)
}

// blockRepeatedFailingCalls filters calls whose identical signature already
// failed repeatedFailureLimit times. Blocked calls get an explicit result that
// tells the model to change the arguments or move on, instead of paying for
// another identical failure.
func blockRepeatedFailingCalls(calls []schema.ToolCall, failures map[string]int) ([]schema.ToolCall, map[string]schema.ToolResult) {
	if len(calls) == 0 || len(failures) == 0 {
		return calls, nil
	}
	allowed := make([]schema.ToolCall, 0, len(calls))
	blocked := make(map[string]schema.ToolResult)
	for _, call := range calls {
		signature := failedCallSignature(call)
		seen := failures[signature]
		if seen < repeatedFailureLimit {
			allowed = append(allowed, call)
			continue
		}
		blocked[call.ID] = schema.ToolResult{
			CallID:   call.ID,
			ToolName: call.Name,
			IsError:  true,
			Content: fmt.Sprintf(
				"identical call already failed %d times with the same arguments; it cannot succeed again. Change the arguments, use a different tool, or produce the stage report.",
				seen,
			),
			Summary: "repeated failing call blocked",
		}
	}
	return allowed, blocked
}

// recordFailedCallSignatures counts failing results by signature.
func recordFailedCallSignatures(results []schema.ToolResult, calls []schema.ToolCall, failures map[string]int) {
	if len(results) == 0 || failures == nil {
		return
	}
	for _, result := range results {
		if !result.IsError || result.Suspended {
			continue
		}
		call, ok := toolCallByID(calls, result.CallID)
		if !ok {
			continue
		}
		failures[failedCallSignature(call)]++
	}
}

// repeatedFailureNudge tells the model how many identical failures happened so
// it can switch strategy instead of repeating the call.
func repeatedFailureNudge(blocked map[string]schema.ToolResult) string {
	if len(blocked) == 0 {
		return ""
	}
	names := make([]string, 0, len(blocked))
	for _, result := range blocked {
		names = append(names, result.ToolName)
	}
	return fmt.Sprintf("Blocked %d repeated failing call(s): %s. The earlier failures are already in context; change the arguments or produce the report.", len(names), strings.Join(names, ", "))
}
