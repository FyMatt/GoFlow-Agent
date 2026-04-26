package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// ToolCatalog indexes tools by full name and short name.
type ToolCatalog struct {
	byName map[string]schema.Tool
}

func NewToolCatalog(tools []schema.Tool) ToolCatalog {
	catalog := ToolCatalog{byName: make(map[string]schema.Tool, len(tools)*2)}
	for _, tool := range tools {
		catalog.byName[tool.Name] = tool
		if tool.Server != "" {
			catalog.byName[tool.Server+"/"+tool.Name] = tool
		}
	}
	return catalog
}

func (c ToolCatalog) Find(name string) (schema.Tool, bool) {
	tool, ok := c.byName[name]
	return tool, ok
}

func KindForTool(tool schema.Tool) config.ToolKind {
	kind := strings.TrimSpace(tool.Kind)
	if kind == "" {
		name := strings.ToLower(tool.Name)
		switch {
		case strings.Contains(name, "read") || strings.Contains(name, "list") || strings.Contains(name, "view"):
			kind = string(config.ToolKindRead)
		case strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "delete"):
			kind = string(config.ToolKindWrite)
		case strings.Contains(name, "http") || strings.Contains(name, "fetch") || strings.Contains(name, "request"):
			kind = string(config.ToolKindNetwork)
		case strings.Contains(name, "exec") || strings.Contains(name, "run") || strings.Contains(name, "shell"):
			kind = string(config.ToolKindExec)
		default:
			kind = string(config.ToolKindUnknown)
		}
	}
	return config.ToolKind(kind)
}

func ValidateToolCall(call schema.ToolCall, tool schema.Tool) error {
	var raw any
	dec := json.NewDecoder(bytes.NewReader(call.Arguments))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("decode tool arguments: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode tool arguments: unexpected trailing data")
		}
		return fmt.Errorf("decode tool arguments: %w", err)
	}
	args, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("decode tool arguments: expected JSON object")
	}
	if len(tool.InputSchema) == 0 {
		return nil
	}

	var schemaDef map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schemaDef); err != nil {
		return fmt.Errorf("decode tool schema: %w", err)
	}
	if err := validateAgainstSchema(tool.Name, "$", args, schemaDef); err != nil {
		return err
	}
	return nil
}

func EnforceToolPolicy(agent config.AgentProfile, tool schema.Tool) error {
	kind := KindForTool(tool)
	allowed := agent.AllowedToolKinds
	if len(allowed) == 0 {
		allowed = []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown}
	}
	kindAllowed := false
	for _, candidate := range allowed {
		if candidate == kind {
			kindAllowed = true
			break
		}
	}
	if !kindAllowed {
		allowedStrings := make([]string, 0, len(allowed))
		for _, candidate := range allowed {
			allowedStrings = append(allowedStrings, string(candidate))
		}
		sort.Strings(allowedStrings)
		return fmt.Errorf("tool kind %s is not allowed; allowed kinds: %s", kind, strings.Join(allowedStrings, ", "))
	}
	if len(agent.AllowedTools) > 0 {
		for _, name := range agent.AllowedTools {
			if strings.TrimSpace(name) == tool.Name {
				if agent.ToolPolicy == config.ToolPolicyDeny {
					return fmt.Errorf("tool policy denies tool %s", tool.Name)
				}
				return nil
			}
		}
		return fmt.Errorf("tool %s is not allowed; allowed tools: %s", tool.Name, strings.Join(agent.AllowedTools, ", "))
	}
	if agent.ToolPolicy == config.ToolPolicyDeny {
		return fmt.Errorf("tool policy denies tool kind %s", kind)
	}
	return nil
}

func validateAgainstSchema(toolName, path string, value any, schemaDef map[string]any) error {
	if len(schemaDef) == 0 {
		return nil
	}

	if allOf, ok := schemaSlice(schemaDef, "allOf"); ok {
		for _, candidate := range allOf {
			if err := validateAgainstSchema(toolName, path, value, candidate); err != nil {
				return err
			}
		}
	}

	if oneOf, ok := schemaSlice(schemaDef, "oneOf"); ok {
		matches := 0
		for _, candidate := range oneOf {
			if err := validateAgainstSchema(toolName, path, value, candidate); err == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("tool %s field %q must match exactly one schema", toolName, displayPath(path))
		}
	}

	if enumValues, ok := schemaArray(schemaDef, "enum"); ok {
		if !matchesEnum(value, enumValues) {
			return fmt.Errorf("tool %s field %q must be one of %s", toolName, displayPath(path), joinEnum(enumValues))
		}
	}

	if expectedType, _ := schemaDef["type"].(string); expectedType != "" {
		if err := validateValueType(expectedType, value); err != nil {
			return fmt.Errorf("tool %s field %q %w", toolName, displayPath(path), err)
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := validateObject(toolName, path, typed, schemaDef); err != nil {
			return err
		}
	case []any:
		if err := validateArray(toolName, path, typed, schemaDef); err != nil {
			return err
		}
	case string:
		if err := validateString(toolName, path, typed, schemaDef); err != nil {
			return err
		}
	default:
		if err := validateNumber(toolName, path, value, schemaDef); err != nil {
			return err
		}
	}

	return nil
}

func validateObject(toolName, path string, value map[string]any, schemaDef map[string]any) error {
	properties := schemaObjectMap(schemaDef, "properties")
	required := schemaStringSlice(schemaDef, "required")
	for _, key := range required {
		if _, ok := value[key]; !ok {
			return fmt.Errorf("tool %s missing required field %q", toolName, joinPath(path, key))
		}
	}

	allowAdditional := true
	if raw, exists := schemaDef["additionalProperties"]; exists {
		if flag, ok := raw.(bool); ok {
			allowAdditional = flag
		}
	}

	for key, item := range value {
		childPath := joinPath(path, key)
		propertySchema, ok := properties[key]
		if !ok {
			if !allowAdditional && len(properties) > 0 {
				return fmt.Errorf("tool %s field %q is not allowed", toolName, childPath)
			}
			continue
		}
		if err := validateAgainstSchema(toolName, childPath, item, propertySchema); err != nil {
			return err
		}
	}
	return nil
}

func validateArray(toolName, path string, value []any, schemaDef map[string]any) error {
	if minItems, ok := schemaInt(schemaDef, "minItems"); ok && len(value) < minItems {
		return fmt.Errorf("tool %s field %q must contain at least %d items", toolName, displayPath(path), minItems)
	}
	if maxItems, ok := schemaInt(schemaDef, "maxItems"); ok && len(value) > maxItems {
		return fmt.Errorf("tool %s field %q must contain at most %d items", toolName, displayPath(path), maxItems)
	}
	itemSchema, ok := schemaMap(schemaDef, "items")
	if !ok {
		return nil
	}
	for index, item := range value {
		if err := validateAgainstSchema(toolName, fmt.Sprintf("%s[%d]", path, index), item, itemSchema); err != nil {
			return err
		}
	}
	return nil
}

func validateString(toolName, path, value string, schemaDef map[string]any) error {
	if minLength, ok := schemaInt(schemaDef, "minLength"); ok && len(value) < minLength {
		return fmt.Errorf("tool %s field %q must be at least %d characters", toolName, displayPath(path), minLength)
	}
	if maxLength, ok := schemaInt(schemaDef, "maxLength"); ok && len(value) > maxLength {
		return fmt.Errorf("tool %s field %q must be at most %d characters", toolName, displayPath(path), maxLength)
	}
	if pattern, ok := schemaDef["pattern"].(string); ok && pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("tool %s field %q has invalid pattern %q: %w", toolName, displayPath(path), pattern, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("tool %s field %q must match pattern %q", toolName, displayPath(path), pattern)
		}
	}
	return nil
}

func validateNumber(toolName, path string, value any, schemaDef map[string]any) error {
	number, ok := asFloat64(value)
	if !ok {
		return nil
	}
	if minimum, ok := schemaFloat(schemaDef, "minimum"); ok && number < minimum {
		return fmt.Errorf("tool %s field %q must be >= %s", toolName, displayPath(path), formatNumber(minimum))
	}
	if maximum, ok := schemaFloat(schemaDef, "maximum"); ok && number > maximum {
		return fmt.Errorf("tool %s field %q must be <= %s", toolName, displayPath(path), formatNumber(maximum))
	}
	return nil
}

func validateValueType(expected string, value any) error {
	switch expected {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("must be a string")
		}
	case "number":
		if _, ok := asFloat64(value); !ok {
			return fmt.Errorf("must be a number")
		}
	case "integer":
		number, ok := asFloat64(value)
		if !ok || math.Mod(number, 1) != 0 {
			return fmt.Errorf("must be an integer")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a boolean")
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("must be an object")
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("must be an array")
		}
	}
	return nil
}

func schemaMap(values map[string]any, key string) (map[string]any, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	mapped, ok := raw.(map[string]any)
	return mapped, ok
}

func schemaObjectMap(values map[string]any, key string) map[string]map[string]any {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	mapped, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]map[string]any, len(mapped))
	for name, item := range mapped {
		if schemaDef, ok := item.(map[string]any); ok {
			result[name] = schemaDef
		}
	}
	return result
}

func schemaSlice(values map[string]any, key string) ([]map[string]any, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		schemaDef, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		result = append(result, schemaDef)
	}
	return result, true
}

func schemaArray(values map[string]any, key string) ([]any, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	items, ok := raw.([]any)
	return items, ok
}

func schemaStringSlice(values map[string]any, key string) []string {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func schemaInt(values map[string]any, key string) (int, bool) {
	value, ok := asFloatFromMap(values, key)
	if !ok {
		return 0, false
	}
	return int(value), true
}

func schemaFloat(values map[string]any, key string) (float64, bool) {
	return asFloatFromMap(values, key)
}

func asFloatFromMap(values map[string]any, key string) (float64, bool) {
	raw, ok := values[key]
	if !ok {
		return 0, false
	}
	return asFloat64(raw)
}

func asFloat64(value any) (float64, bool) {
	switch n := value.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func matchesEnum(value any, allowed []any) bool {
	for _, candidate := range allowed {
		if enumValueEquals(value, candidate) {
			return true
		}
	}
	return false
}

func enumValueEquals(left, right any) bool {
	if lf, ok := asFloat64(left); ok {
		if rf, ok := asFloat64(right); ok {
			return lf == rf
		}
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func joinEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%q", fmt.Sprint(value)))
	}
	return strings.Join(parts, ", ")
}

func joinPath(parent, child string) string {
	if parent == "$" || parent == "" {
		return child
	}
	return parent + "." + child
}

func displayPath(path string) string {
	return strings.TrimPrefix(path, "$")
}

func formatNumber(value float64) string {
	if math.Mod(value, 1) == 0 {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%g", value)
}
