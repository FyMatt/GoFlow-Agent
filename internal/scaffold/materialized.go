package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/kits/materialized/*/*.tmpl
var materializedTemplateFS embed.FS

type MaterializedKitNames struct {
	Kit              string
	Agent            string
	Skill            string
	Tool             string
	Workflow         string
	WorkflowTemplate string
	TeamTemplate     string
	PolicyRule       string
}

type MaterializedToolConfig struct {
	Source string
	Target string
}

type MaterializedSkillTool struct {
	Name     string
	Required bool
}

type MaterializedKitTemplateData struct {
	Preset          KitPreset
	Names           MaterializedKitNames
	PrimaryProvider string
	Metadata        map[string]string
	Tool            MaterializedToolConfig
	AgentTools      []string
	SkillTools      []MaterializedSkillTool
	PlannerTools    []string
	ReviewerTools   []string
	BuiltinTools    []string
	// DomainToolNotes carries tool-boundary notes into materialized resources; the API returns activation_steps after saving them.
	DomainToolNotes  []string
	WorkflowTitle    string
	WorkflowTemplate bool
}

func RenderMaterializedKitYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "kit.yaml.tmpl", MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		PrimaryProvider: primaryProvider(preset),
		Metadata:        materializedKitMetadata(preset, names),
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
		WorkflowTitle:   firstOrDefault(preset.RecommendedWorkflow, names.Workflow),
	})
}

func RenderMaterializedAgentConfig(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	data := MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		PrimaryProvider: primaryProvider(preset),
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	}
	if normalizeName(preset.Name) == "binary-analysis" {
		data.AgentTools = []string{
			names.Tool + "/ping",
			names.Tool + "/binary_file_info",
			names.Tool + "/binary_strings",
			names.Tool + "/hex_preview",
			names.Tool + "/read_text",
		}
	} else {
		data.AgentTools = []string{names.Tool + "/ping", names.Tool + "/read_text"}
	}
	data.AgentTools = appendMissingStrings(data.AgentTools, data.BuiltinTools...)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "agent.yaml.tmpl", data)
}

func RenderMaterializedSkillMarkdown(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	data := MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	}
	if normalizeName(preset.Name) == "binary-analysis" {
		data.SkillTools = []MaterializedSkillTool{
			{Name: names.Tool + "/binary_file_info", Required: true},
			{Name: names.Tool + "/binary_strings", Required: true},
			{Name: names.Tool + "/hex_preview", Required: false},
			{Name: names.Tool + "/read_text", Required: false},
		}
	} else {
		data.SkillTools = []MaterializedSkillTool{{Name: names.Tool + "/read_text", Required: false}}
	}
	for _, name := range data.BuiltinTools {
		data.SkillTools = appendSkillToolIfMissing(data.SkillTools, MaterializedSkillTool{Name: name, Required: false})
	}
	return renderMaterializedTemplate(runtimeHome, preset.Name, "skill.md.tmpl", data)
}

func RenderMaterializedToolConfig(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolSource := filepath.ToSlash(filepath.Join(runtimeHome, "mcp_servers", names.Tool+".py"))
	if strings.TrimSpace(runtimeHome) == "" {
		toolSource = filepath.ToSlash(filepath.Join("mcp_servers", names.Tool+".py"))
	}
	return renderMaterializedTemplate(runtimeHome, preset.Name, "tool-config.yaml.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
		Tool: MaterializedToolConfig{
			Source: toolSource,
			Target: "/goflow-tools/" + names.Tool + ".py",
		},
	})
}

func RenderMaterializedWorkflowYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow.yaml.tmpl", MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	})
}

func RenderMaterializedWorkflowTemplateYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow-template.yaml.tmpl", MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		PrimaryProvider: primaryProvider(preset),
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	})
}

func RenderMaterializedTeamTemplateYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	data := MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	}
	if normalizeName(preset.Name) == "binary-analysis" {
		data.PlannerTools = []string{names.Tool + "/binary_file_info", names.Tool + "/binary_strings", names.Tool + "/hex_preview"}
		data.ReviewerTools = []string{names.Tool + "/binary_strings", names.Tool + "/hex_preview"}
	} else {
		data.PlannerTools = []string{names.Tool + "/read_text"}
	}
	data.PlannerTools = appendMissingStrings(data.PlannerTools, data.BuiltinTools...)
	data.ReviewerTools = appendMissingStrings(data.ReviewerTools, reviewerBuiltinTools(data.BuiltinTools)...)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "team.yaml.tmpl", data)
}

func RenderMaterializedPolicyRuleYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	return renderMaterializedTemplate(runtimeHome, preset.Name, "policy-rule.yaml.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
	})
}

func RenderMaterializedWorkflowMarkdown(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	toolNames := materializedToolNames(preset, names)
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow.md.tmpl", MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		BuiltinTools:    builtinMaterializedTools(toolNames, names),
		DomainToolNotes: domainToolBoundaryNotes(toolNames),
	})
}

func renderMaterializedTemplate(runtimeHome, presetName, templateName string, data MaterializedKitTemplateData) (string, error) {
	presetName = normalizeName(presetName)
	searchPaths := []string{}
	if strings.TrimSpace(runtimeHome) != "" {
		searchPaths = append(searchPaths,
			filepath.Join(runtimeHome, "templates", "kits", "materialized", presetName, templateName),
			filepath.Join(runtimeHome, "templates", "kits", "materialized", "generic", templateName),
		)
	}
	searchPaths = append(searchPaths,
		filepath.Join("templates", "kits", "materialized", presetName, templateName),
		filepath.Join("templates", "kits", "materialized", "generic", templateName),
	)
	content, err := readMaterializedTemplate(searchPaths...)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateName).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse materialized template %s: %w", templateName, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render materialized template %s: %w", templateName, err)
	}
	return out.String(), nil
}

func readMaterializedTemplate(paths ...string) (string, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if strings.HasPrefix(path, string(filepath.Separator)) || len(path) > 1 && path[1] == ':' {
			if data, err := os.ReadFile(path); err == nil {
				return string(data), nil
			}
			continue
		}
		if data, err := materializedTemplateFS.ReadFile(filepath.ToSlash(path)); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("materialized template not found: %s", strings.Join(paths, ", "))
}

func firstOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func primaryProvider(preset KitPreset) string {
	for _, provider := range preset.Providers {
		if strings.TrimSpace(provider) != "" {
			return strings.TrimSpace(provider)
		}
	}
	return "primary"
}

func materializedKitMetadata(preset KitPreset, names MaterializedKitNames) map[string]string {
	meta := make(map[string]string, len(preset.Metadata)+10)
	for key, value := range preset.Metadata {
		key = strings.TrimSpace(key)
		if key != "" {
			meta[key] = strings.TrimSpace(value)
		}
	}
	meta["scaffold_preset"] = normalizeName(preset.Name)
	meta["materialized"] = "true"
	meta["generated_agent"] = names.Agent
	meta["generated_skill"] = names.Skill
	meta["generated_tool"] = names.Tool
	meta["generated_workflow"] = names.Workflow
	meta["generated_workflow_template"] = names.WorkflowTemplate
	meta["generated_team_template"] = names.TeamTemplate
	meta["generated_policy_rule"] = names.PolicyRule
	meta["recommended_agent"] = names.Agent
	meta["recommended_workflow"] = names.Workflow
	meta["recommended_team"] = names.TeamTemplate
	return meta
}

func materializedToolNames(preset KitPreset, names MaterializedKitNames) []string {
	out := []string{names.Tool}
	out = append(out, preset.Tools...)
	return normalizeStringList(out, true)
}

func builtinMaterializedTools(tools []string, names MaterializedKitNames) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" || strings.EqualFold(tool, names.Tool) || strings.HasPrefix(strings.ToLower(tool), strings.ToLower(names.Tool)+"/") {
			continue
		}
		out = append(out, tool)
	}
	return normalizeStringList(out, true)
}

func appendMissingStrings(values []string, extra ...string) []string {
	out := append([]string(nil), values...)
	seen := make(map[string]struct{}, len(out)+len(extra))
	for _, value := range out {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendSkillToolIfMissing(values []MaterializedSkillTool, extra MaterializedSkillTool) []MaterializedSkillTool {
	name := strings.TrimSpace(extra.Name)
	if name == "" {
		return values
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value.Name), name) {
			return values
		}
	}
	return append(values, extra)
}

func reviewerBuiltinTools(tools []string) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		normalized := strings.ToLower(strings.TrimSpace(tool))
		switch {
		case normalized == "":
			continue
		case strings.Contains(normalized, "/write_"):
			continue
		case strings.Contains(normalized, "config_dry_run"):
			continue
		default:
			out = append(out, tool)
		}
	}
	return out
}

func domainToolBoundaryNotes(tools []string) []string {
	notes := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(note string) {
		note = strings.TrimSpace(note)
		if note == "" {
			return
		}
		key := strings.ToLower(note)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		notes = append(notes, note)
	}
	for _, tool := range tools {
		normalized := strings.ToLower(strings.TrimSpace(tool))
		switch {
		case strings.HasPrefix(normalized, "network_tools/"):
			add("network_tools are planning and policy-check tools only; require authorized_scope, allowed_hosts, credential_ref, command allowlists, dry_run, rollback, approval metadata, and artifact refs; they do not connect to devices or apply configuration.")
		case strings.HasPrefix(normalized, "web_tools/browser_"):
			add("browser automation tools require authorized hosts and scoped evidence collection; active probe behavior must stay approval-gated.")
		case strings.HasPrefix(normalized, "file_tools/write_"):
			add("workspace write tools require scoped ownership, approval policy, and concise changed-file evidence.")
		case strings.Contains(normalized, "binary_") || strings.Contains(normalized, "hex_preview"):
			add("binary analysis tools are static triage helpers; do not execute untrusted binaries and keep raw evidence as artifacts.")
		}
	}
	return notes
}
