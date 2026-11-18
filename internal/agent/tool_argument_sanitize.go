package agent

import (
	"encoding/json"

	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// sanitizeReadOnlyToolArguments drops argument fields a read-only tool does not
// declare. Models routinely attach host-scoped fields such as target_id to file
// reads; the strict schema then rejected the call, and one rejected read during
// recovery made a quality gate fail a delivery that was actually complete.
func sanitizeReadOnlyToolArguments(tool schema.Tool, call schema.ToolCall) schema.ToolCall {
	if policy.KindForTool(tool) != "read" || len(tool.InputSchema) == 0 || len(call.Arguments) == 0 {
		return call
	}
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &document); err != nil || len(document.Properties) == 0 {
		return call
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil || len(arguments) == 0 {
		return call
	}
	dropped := false
	for key := range arguments {
		if _, ok := document.Properties[key]; ok {
			continue
		}
		delete(arguments, key)
		dropped = true
	}
	if !dropped {
		return call
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return call
	}
	call.Arguments = encoded
	return call
}

// sanitizeToolCall applies the read-only argument cleanup using the run's tool
// catalog, so callers do not need to look the tool up themselves.
func (e ExecutionContext) sanitizeToolCall(call schema.ToolCall) schema.ToolCall {
	if len(e.Tools) == 0 {
		return call
	}
	tool, ok := policy.NewToolCatalog(e.Tools).Find(call.Name)
	if !ok {
		return call
	}
	return sanitizeReadOnlyToolArguments(tool, call)
}
