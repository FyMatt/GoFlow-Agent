package policy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestValidateToolCallSupportsNestedSchemas(t *testing.T) {
	tool := schema.Tool{
		Name: "write_file",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"options":{
					"type":"object",
					"properties":{
						"overwrite":{"type":"boolean"},
						"tags":{"type":"array","items":{"type":"string"}}
					},
					"required":["overwrite"],
					"additionalProperties":false
				}
			},
			"required":["path","options"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"path":"a.txt","options":{"overwrite":true,"tags":["a","b"]}}`),
	}

	if err := ValidateToolCall(call, tool); err != nil {
		t.Fatalf("expected nested schema validation to pass, got %v", err)
	}
}

func TestValidateToolCallRejectsNestedAdditionalProperties(t *testing.T) {
	tool := schema.Tool{
		Name: "write_file",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"options":{
					"type":"object",
					"properties":{"overwrite":{"type":"boolean"}},
					"additionalProperties":false
				}
			},
			"required":["options"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"options":{"overwrite":true,"extra":"x"}}`),
	}

	err := ValidateToolCall(call, tool)
	if err == nil || !strings.Contains(err.Error(), `field "options.extra" is not allowed`) {
		t.Fatalf("expected nested additionalProperties error, got %v", err)
	}
}

func TestValidateToolCallSupportsEnumPatternAndBounds(t *testing.T) {
	tool := schema.Tool{
		Name: "search",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"mode":{"type":"string","enum":["fast","slow"]},
				"query":{"type":"string","pattern":"^[a-z]+$","minLength":3,"maxLength":8},
				"limit":{"type":"integer","minimum":1,"maximum":10}
			},
			"required":["mode","query","limit"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"mode":"fast","query":"alpha","limit":3}`),
	}

	if err := ValidateToolCall(call, tool); err != nil {
		t.Fatalf("expected enum/pattern/bounds validation to pass, got %v", err)
	}
}

func TestValidateToolCallRejectsEnumMismatch(t *testing.T) {
	tool := schema.Tool{
		Name: "search",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"mode":{"type":"string","enum":["fast","slow"]}},
			"required":["mode"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"mode":"medium"}`),
	}

	err := ValidateToolCall(call, tool)
	if err == nil || !strings.Contains(err.Error(), `field "mode" must be one of`) {
		t.Fatalf("expected enum error, got %v", err)
	}
}

func TestValidateToolCallRejectsPatternMismatch(t *testing.T) {
	tool := schema.Tool{
		Name: "search",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"query":{"type":"string","pattern":"^[a-z]+$"}},
			"required":["query"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"query":"Alpha-1"}`),
	}

	err := ValidateToolCall(call, tool)
	if err == nil || !strings.Contains(err.Error(), `field "query" must match pattern`) {
		t.Fatalf("expected pattern error, got %v", err)
	}
}

func TestValidateToolCallRejectsNumericBounds(t *testing.T) {
	tool := schema.Tool{
		Name: "search",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"limit":{"type":"integer","minimum":1,"maximum":10}},
			"required":["limit"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"limit":11}`),
	}

	err := ValidateToolCall(call, tool)
	if err == nil || !strings.Contains(err.Error(), `field "limit" must be <= 10`) {
		t.Fatalf("expected numeric bounds error, got %v", err)
	}
}

func TestValidateToolCallSupportsOneOf(t *testing.T) {
	tool := schema.Tool{
		Name: "lookup",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"value":{
					"oneOf":[
						{"type":"string"},
						{"type":"integer"}
					]
				}
			},
			"required":["value"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"value":42}`),
	}

	if err := ValidateToolCall(call, tool); err != nil {
		t.Fatalf("expected oneOf validation to pass, got %v", err)
	}
}

func TestValidateToolCallRejectsOneOfMismatch(t *testing.T) {
	tool := schema.Tool{
		Name: "lookup",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"value":{
					"oneOf":[
						{"type":"string"},
						{"type":"integer"}
					]
				}
			},
			"required":["value"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"value":true}`),
	}

	err := ValidateToolCall(call, tool)
	if err == nil || !strings.Contains(err.Error(), `field "value" must match exactly one schema`) {
		t.Fatalf("expected oneOf mismatch error, got %v", err)
	}
}

func TestValidateToolCallSupportsAllOf(t *testing.T) {
	tool := schema.Tool{
		Name: "search",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{
					"allOf":[
						{"type":"string","minLength":3},
						{"pattern":"^[a-z]+$"}
					]
				}
			},
			"required":["query"],
			"additionalProperties":false
		}`),
	}
	call := schema.ToolCall{
		Name:      tool.Name,
		Arguments: json.RawMessage(`{"query":"alpha"}`),
	}

	if err := ValidateToolCall(call, tool); err != nil {
		t.Fatalf("expected allOf validation to pass, got %v", err)
	}
}
