package llm

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// SanitizeToolMessages enforces the tool-message invariant strict providers
// check: every assistant tool_calls entry needs a matching tool response, and a
// tool message may only answer a call the immediately preceding assistant asked
// for. Orphan observations are rewritten as user notes so no evidence is lost,
// and unanswered calls get an explicit placeholder response instead of a 400.
func SanitizeToolMessages(messages []schema.Message) ([]schema.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}
	repairs := 0
	allowed := map[string]struct{}{}
	out := make([]schema.Message, 0, len(messages)+4)
	for _, original := range messages {
		message := schema.CopyMessage(original)
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "assistant":
			allowed = map[string]struct{}{}
			for _, call := range message.ToolCalls {
				if id := strings.TrimSpace(call.ID); id != "" {
					allowed[id] = struct{}{}
				}
			}
			out = append(out, message)
		case "tool":
			callID := strings.TrimSpace(message.ToolCallID)
			if _, ok := allowed[callID]; callID != "" && ok {
				out = append(out, message)
				delete(allowed, callID)
				continue
			}
			message.Role = "user"
			message.Content = fmt.Sprintf("[GoFlow preserved orphan tool observation call_id=%s tool=%s] %s", callID, strings.TrimSpace(message.Name), message.Content)
			message.ToolCallID = ""
			message.Name = ""
			allowed = map[string]struct{}{}
			repairs++
			out = append(out, message)
		default:
			allowed = map[string]struct{}{}
			out = append(out, message)
		}
	}
	// Fill responses for tool calls that were never answered.
	if len(out) == 0 {
		return out, repairs
	}
	filled := make([]schema.Message, 0, len(out)+4)
	for index, message := range out {
		filled = append(filled, message)
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || len(message.ToolCalls) == 0 {
			continue
		}
		answered := map[string]struct{}{}
		for next := index + 1; next < len(out); next++ {
			if !strings.EqualFold(strings.TrimSpace(out[next].Role), "tool") {
				break
			}
			answered[strings.TrimSpace(out[next].ToolCallID)] = struct{}{}
		}
		for _, call := range message.ToolCalls {
			id := strings.TrimSpace(call.ID)
			if id == "" {
				continue
			}
			if _, ok := answered[id]; ok {
				continue
			}
			filled = append(filled, schema.Message{
				Role:       "tool",
				ToolCallID: id,
				Name:       call.Name,
				Content:    "[GoFlow placeholder] this tool call produced no recorded result before the next model turn",
			})
			repairs++
		}
	}
	return filled, repairs
}
