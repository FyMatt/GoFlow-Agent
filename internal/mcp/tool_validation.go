package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func validateDiscoveredTool(tool schema.Tool) error {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if name != tool.Name {
		return fmt.Errorf("tool name must not contain surrounding whitespace")
	}
	if err := validateToolName(name); err != nil {
		return err
	}

	kind := strings.TrimSpace(tool.Kind)
	if kind == "" {
		return fmt.Errorf("tool kind is required")
	}
	if kind != tool.Kind {
		return fmt.Errorf("tool kind must not contain surrounding whitespace")
	}
	switch config.ToolKind(kind) {
	case config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown:
	default:
		return fmt.Errorf("tool kind %q is invalid; expected read, write, exec, network, or unknown", kind)
	}

	if err := validateToolInputSchema(tool.InputSchema); err != nil {
		return err
	}
	return nil
}

func validateToolName(name string) error {
	if len(name) > 64 {
		return fmt.Errorf("tool name %q is too long; maximum is 64 bytes", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("tool name %q must match [A-Za-z0-9_-]+", name)
		}
	}
	return nil
}

func validateToolInputSchema(inputSchema json.RawMessage) error {
	if len(inputSchema) == 0 {
		return fmt.Errorf("input_schema is required")
	}
	var schemaDoc struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	decoder := json.NewDecoder(bytes.NewReader(inputSchema))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaDoc); err != nil {
		return fmt.Errorf("decode input_schema: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode input_schema: unexpected trailing data")
		}
		return fmt.Errorf("decode input_schema: %w", err)
	}
	if schemaDoc.Type != "object" {
		return fmt.Errorf("input_schema type must be object")
	}
	if schemaDoc.Properties == nil {
		return fmt.Errorf("input_schema object must declare properties")
	}
	if schemaDoc.AdditionalProperties == nil || *schemaDoc.AdditionalProperties {
		return fmt.Errorf("input_schema object must set additionalProperties=false")
	}
	for _, required := range schemaDoc.Required {
		if _, ok := schemaDoc.Properties[required]; !ok {
			return fmt.Errorf("input_schema required field %q is not declared in properties", required)
		}
	}
	return nil
}
