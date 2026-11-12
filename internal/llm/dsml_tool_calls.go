package llm

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	dsmlToolCallsOpen  = "<｜｜DSML｜｜tool_calls>"
	dsmlToolCallsClose = "</｜｜DSML｜｜tool_calls>"
	dsmlInvokeOpen     = "<｜｜DSML｜｜invoke"
	dsmlInvokeClose    = "</｜｜DSML｜｜invoke>"
	dsmlParameterOpen  = "<｜｜DSML｜｜parameter"
	dsmlParameterClose = "</｜｜DSML｜｜parameter>"
	xmlToolCallsOpen   = "<tool_calls>"
	xmlToolCallsClose  = "</tool_calls>"
	xmlInvokeOpen      = "<tool_call"
	xmlInvokeClose     = "</tool_call>"
	xmlSingleCallOpen  = "<tool_call>"
	xmlSingleCallClose = "</tool_call>"
	xmlFunctionOpen    = "<function"
	xmlFunctionClose   = "</function>"
	xmlAltInvokeOpen   = "<invoke"
	xmlAltInvokeClose  = "</invoke>"
	xmlParameterOpen   = "<parameter"
	xmlParameterClose  = "</parameter>"
)

// parseDSMLToolCalls accepts DeepSeek's textual tool-call envelopes only when
// the entire assistant content is one complete, well-formed envelope.
func parseDSMLToolCalls(content string) ([]schema.ToolCall, string, bool) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, dsmlToolCallsOpen) && strings.HasSuffix(trimmed, dsmlToolCallsClose) {
		return parseTextToolCalls(content, trimmed, dsmlToolCallsOpen, dsmlToolCallsClose, dsmlInvokeOpen, dsmlInvokeClose, dsmlParameterOpen, dsmlParameterClose, false)
	}
	if strings.HasPrefix(trimmed, xmlToolCallsOpen) && strings.HasSuffix(trimmed, xmlToolCallsClose) {
		if calls, ok := parsePlainXMLToolCalls(trimmed); ok {
			ensureToolCallIDs(calls)
			return calls, "", true
		}
		return nil, content, false
	}
	if strings.HasPrefix(trimmed, xmlSingleCallOpen) && strings.HasSuffix(trimmed, xmlSingleCallClose) {
		return parseTextToolCalls(content, trimmed, xmlSingleCallOpen, xmlSingleCallClose, xmlFunctionOpen, xmlFunctionClose, xmlParameterOpen, xmlParameterClose, true)
	}
	if calls, cleaned, ok := parseToCodeToolCalls(trimmed); ok {
		ensureToolCallIDs(calls)
		return calls, cleaned, true
	}
	if calls, ok := parseFencedJSONToolCall(trimmed); ok {
		ensureToolCallIDs(calls)
		return calls, "", true
	}
	return nil, content, false
}

func parseToCodeToolCalls(content string) ([]schema.ToolCall, string, bool) {
	rest := strings.TrimSpace(content)
	if !strings.HasPrefix(rest, "to=") {
		return nil, content, false
	}
	calls := make([]schema.ToolCall, 0, 1)
	seen := make(map[string]struct{})
	for strings.HasPrefix(rest, "to=") {
		lineEnd := strings.IndexByte(rest, '\n')
		if lineEnd < 0 {
			return nil, content, false
		}
		header := strings.TrimSpace(rest[:lineEnd])
		fields := strings.Fields(header)
		if len(fields) < 2 || !strings.EqualFold(fields[len(fields)-1], "code") {
			return nil, content, false
		}
		for _, marker := range fields[1 : len(fields)-1] {
			if strings.ContainsAny(marker, `./\=`) {
				return nil, content, false
			}
		}
		toolName := strings.TrimSpace(strings.TrimPrefix(fields[0], "to="))
		if !validDSMLName(toolName) {
			return nil, content, false
		}
		if !strings.Contains(toolName, "/") && strings.Count(toolName, ".") == 1 {
			toolName = strings.Replace(toolName, ".", "/", 1)
		}
		jsonAndTail := strings.TrimSpace(rest[lineEnd+1:])
		decoder := json.NewDecoder(strings.NewReader(jsonAndTail))
		var arguments map[string]any
		if err := decoder.Decode(&arguments); err != nil || arguments == nil {
			return nil, content, false
		}
		normalized, err := json.Marshal(arguments)
		if err != nil {
			return nil, content, false
		}
		key := toolName + "\x00" + string(normalized)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			calls = append(calls, schema.ToolCall{Name: toolName, Arguments: normalized})
		}
		offset := decoder.InputOffset()
		if offset < 0 || offset > int64(len(jsonAndTail)) {
			return nil, content, false
		}
		rest = strings.TrimSpace(jsonAndTail[offset:])
	}
	if len(calls) == 0 {
		return nil, content, false
	}
	return calls, rest, true
}

func parsePlainXMLToolCalls(trimmed string) ([]schema.ToolCall, bool) {
	body := trimmed
	for strings.HasPrefix(body, xmlToolCallsOpen) && strings.HasSuffix(body, xmlToolCallsClose) {
		body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, xmlToolCallsOpen), xmlToolCallsClose))
	}
	if body == "" || strings.Contains(body, xmlToolCallsOpen) || strings.Contains(body, xmlToolCallsClose) {
		return nil, false
	}
	for _, envelope := range []struct {
		invokeOpen  string
		invokeClose string
	}{
		{invokeOpen: xmlInvokeOpen, invokeClose: xmlInvokeClose},
		{invokeOpen: xmlAltInvokeOpen, invokeClose: xmlAltInvokeClose},
	} {
		synthetic := xmlToolCallsOpen + body + xmlToolCallsClose
		calls, _, ok := parseTextToolCalls(synthetic, synthetic, xmlToolCallsOpen, xmlToolCallsClose, envelope.invokeOpen, envelope.invokeClose, xmlParameterOpen, xmlParameterClose, true)
		if ok {
			return calls, true
		}
	}
	return nil, false
}

func parseFencedJSONToolCall(content string) ([]schema.ToolCall, bool) {
	const (
		open  = "```json"
		close = "```"
	)
	if !strings.HasPrefix(content, open) || !strings.HasSuffix(content, close) {
		return nil, false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, open), close))
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope) != 2 {
		return nil, false
	}
	toolRaw, hasTool := envelope["tool"]
	argumentsRaw, hasArguments := envelope["arguments"]
	if !hasTool || !hasArguments {
		return nil, false
	}
	var toolName string
	if err := json.Unmarshal(toolRaw, &toolName); err != nil || !validDSMLName(toolName) {
		return nil, false
	}
	var arguments map[string]any
	if err := json.Unmarshal(argumentsRaw, &arguments); err != nil || arguments == nil {
		return nil, false
	}
	normalized, err := json.Marshal(arguments)
	if err != nil {
		return nil, false
	}
	return []schema.ToolCall{{Name: toolName, Arguments: normalized}}, true
}

func parseTextToolCalls(content, trimmed, callsOpen, callsClose, invokeOpen, invokeClose, parameterOpen, parameterClose string, plainStrings bool) ([]schema.ToolCall, string, bool) {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, callsOpen), callsClose))
	calls := make([]schema.ToolCall, 0, 1)
	for body != "" {
		if !strings.HasPrefix(body, invokeOpen) {
			return nil, content, false
		}
		tagEnd := strings.IndexByte(body, '>')
		if tagEnd < 0 {
			return nil, content, false
		}
		toolName, ok := dsmlAttribute(body[:tagEnd+1], "name")
		if !ok || !validDSMLName(toolName) {
			return nil, content, false
		}
		invokeEnd := strings.Index(body[tagEnd+1:], invokeClose)
		if invokeEnd < 0 {
			return nil, content, false
		}
		invokeEnd += tagEnd + 1
		paramsBody := strings.TrimSpace(body[tagEnd+1 : invokeEnd])
		params := make(map[string]any)
		for paramsBody != "" {
			if !strings.HasPrefix(paramsBody, parameterOpen) {
				return nil, content, false
			}
			paramTagEnd := strings.IndexByte(paramsBody, '>')
			if paramTagEnd < 0 {
				return nil, content, false
			}
			paramName, ok := dsmlAttribute(paramsBody[:paramTagEnd+1], "name")
			if !ok || !validDSMLName(paramName) {
				return nil, content, false
			}
			if _, exists := params[paramName]; exists {
				return nil, content, false
			}
			paramEnd := strings.Index(paramsBody[paramTagEnd+1:], parameterClose)
			if paramEnd < 0 {
				return nil, content, false
			}
			paramEnd += paramTagEnd + 1
			rawValue := paramsBody[paramTagEnd+1 : paramEnd]
			stringValue, _ := dsmlAttribute(paramsBody[:paramTagEnd+1], "string")
			if strings.EqualFold(stringValue, "true") || plainStrings && !looksLikeJSON(rawValue) {
				params[paramName] = rawValue
			} else {
				var value any
				if err := json.Unmarshal([]byte(strings.TrimSpace(rawValue)), &value); err != nil {
					return nil, content, false
				}
				params[paramName] = value
			}
			paramsBody = strings.TrimSpace(paramsBody[paramEnd+len(parameterClose):])
		}
		arguments, err := json.Marshal(params)
		if err != nil {
			return nil, content, false
		}
		calls = append(calls, schema.ToolCall{Name: toolName, Arguments: arguments})
		body = strings.TrimSpace(body[invokeEnd+len(invokeClose):])
	}
	if len(calls) == 0 {
		return nil, content, false
	}
	ensureToolCallIDs(calls)
	return calls, "", true
}

func looksLikeJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "true" || trimmed == "false" || trimmed == "null" {
		return true
	}
	if trimmed == "" {
		return false
	}
	return strings.ContainsRune(`{[\"-0123456789`, rune(trimmed[0]))
}

func dsmlAttribute(tag, name string) (string, bool) {
	needle := name + `="`
	start := strings.Index(tag, needle)
	if start < 0 {
		return "", false
	}
	if start > 0 && !unicode.IsSpace(rune(tag[start-1])) {
		return "", false
	}
	start += len(needle)
	end := strings.IndexByte(tag[start:], '"')
	if end < 0 {
		return "", false
	}
	return tag[start : start+end], true
}

func validDSMLName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}
