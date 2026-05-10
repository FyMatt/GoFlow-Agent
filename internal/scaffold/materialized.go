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
	Preset           KitPreset
	Names            MaterializedKitNames
	PrimaryProvider  string
	Metadata         map[string]string
	Tool             MaterializedToolConfig
	AgentTools       []string
	SkillTools       []MaterializedSkillTool
	PlannerTools     []string
	ReviewerTools    []string
	WorkflowTitle    string
	WorkflowTemplate bool
}

func RenderMaterializedKitYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	return renderMaterializedTemplate(runtimeHome, preset.Name, "kit.yaml.tmpl", MaterializedKitTemplateData{
		Preset:          preset,
		Names:           names,
		PrimaryProvider: primaryProvider(preset),
		Metadata:        materializedKitMetadata(preset, names),
		WorkflowTitle:   firstOrDefault(preset.RecommendedWorkflow, names.Workflow),
	})
}

func RenderMaterializedAgentConfig(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	data := MaterializedKitTemplateData{Preset: preset, Names: names, PrimaryProvider: primaryProvider(preset)}
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
	return renderMaterializedTemplate(runtimeHome, preset.Name, "agent.yaml.tmpl", data)
}

func RenderMaterializedSkillMarkdown(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	data := MaterializedKitTemplateData{Preset: preset, Names: names}
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
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow.yaml.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
	})
}

func RenderMaterializedWorkflowTemplateYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow-template.yaml.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
	})
}

func RenderMaterializedTeamTemplateYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	data := MaterializedKitTemplateData{Preset: preset, Names: names}
	if normalizeName(preset.Name) == "binary-analysis" {
		data.PlannerTools = []string{names.Tool + "/binary_file_info", names.Tool + "/binary_strings", names.Tool + "/hex_preview"}
		data.ReviewerTools = []string{names.Tool + "/binary_strings", names.Tool + "/hex_preview"}
	} else {
		data.PlannerTools = []string{names.Tool + "/read_text"}
	}
	return renderMaterializedTemplate(runtimeHome, preset.Name, "team.yaml.tmpl", data)
}

func RenderMaterializedPolicyRuleYAML(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	return renderMaterializedTemplate(runtimeHome, preset.Name, "policy-rule.yaml.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
	})
}

func RenderMaterializedWorkflowMarkdown(runtimeHome string, preset KitPreset, names MaterializedKitNames) (string, error) {
	return renderMaterializedTemplate(runtimeHome, preset.Name, "workflow.md.tmpl", MaterializedKitTemplateData{
		Preset: preset,
		Names:  names,
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
