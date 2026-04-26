package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

func summarizeToolArguments(arguments []byte) string {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" {
		return ""
	}

	var raw any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return truncateSummary(trimmed)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return truncateSummary(trimmed)
	}
	args, ok := raw.(map[string]any)
	if !ok {
		return truncateSummary(trimmed)
	}

	parts := make([]string, 0, len(args))
	appendStringField := func(key string) {
		value, ok := args[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, truncateToolSummaryValue(value)))
	}

	for _, key := range []string{"path", "file", "target", "command", "cmd", "name"} {
		appendStringField(key)
	}
	if content, ok := args["content"].(string); ok {
		parts = append(parts, fmt.Sprintf("content=%d chars", utf8.RuneCountInString(content)))
	}

	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := part
		if index := strings.Index(part, "="); index >= 0 {
			key = part[:index]
		}
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		if key == "content" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, summarizeToolValue(args[key])))
		if len(strings.Join(parts, " ")) >= 120 {
			break
		}
	}
	if len(parts) == 0 {
		return truncateSummary(trimmed)
	}
	return truncateSummary(strings.Join(parts, " "))
}

func summarizeToolValue(value any) string {
	switch typed := value.(type) {
	case string:
		return truncateToolSummaryValue(typed)
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return truncateToolSummaryValue(fmt.Sprint(typed))
		}
		return truncateToolSummaryValue(string(data))
	}
}

func truncateToolSummaryValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if len(value) <= 80 {
		return value
	}
	return value[:77] + "..."
}
