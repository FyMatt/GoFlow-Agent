package schema

import "encoding/json"

// CopyMessage returns a deep copy of a chat message, including provider
// metadata that may need to be replayed to compatible backends.
func CopyMessage(message Message) Message {
	if message.ToolCall != nil {
		call := CopyToolCall(*message.ToolCall)
		message.ToolCall = &call
	}
	message.ToolCalls = CopyToolCalls(message.ToolCalls)
	if len(message.ProviderFields) > 0 {
		fields := make(map[string]json.RawMessage, len(message.ProviderFields))
		for key, value := range message.ProviderFields {
			if len(value) == 0 {
				continue
			}
			fields[key] = append(json.RawMessage(nil), value...)
		}
		if len(fields) > 0 {
			message.ProviderFields = fields
		} else {
			message.ProviderFields = nil
		}
	}
	return message
}

// CopyMessages returns a deep copy of a chat message slice.
func CopyMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	copied := make([]Message, len(messages))
	for i, message := range messages {
		copied[i] = CopyMessage(message)
	}
	return copied
}

// CopyToolCalls returns a deep copy of tool calls.
func CopyToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	copied := make([]ToolCall, len(calls))
	for i, call := range calls {
		copied[i] = CopyToolCall(call)
	}
	return copied
}

// CopyToolCall returns a deep copy of one tool call.
func CopyToolCall(call ToolCall) ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	return call
}
