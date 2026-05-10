package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/skills/scaffolds/*.yaml
var embeddedSkillTemplateFS embed.FS

// SkillTemplate describes a reusable SKILL.md scaffold.
type SkillTemplate struct {
	Name             string              `json:"name" yaml:"name"`
	Description      string              `json:"description" yaml:"description"`
	Mode             string              `json:"mode,omitempty" yaml:"mode,omitempty"`
	PreferredAgent   string              `json:"preferred_agent,omitempty" yaml:"preferred_agent,omitempty"`
	AllowedToolKinds []string            `json:"allowed_tool_kinds,omitempty" yaml:"allowed_tool_kinds,omitempty"`
	OutputKind       string              `json:"output_kind,omitempty" yaml:"output_kind,omitempty"`
	NextSkills       []string            `json:"next_skills,omitempty" yaml:"next_skills,omitempty"`
	Tools            []SkillTemplateTool `json:"tools,omitempty" yaml:"tools,omitempty"`
	Keywords         []string            `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	ExampleRequest   string              `json:"example_request,omitempty" yaml:"example_request,omitempty"`
	Body             string              `json:"body" yaml:"body"`
}

// SkillTemplateTool describes one tool dependency in a generated skill.
type SkillTemplateTool struct {
	Name     string `json:"name" yaml:"name"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

type skillTemplateFile struct {
	Templates []SkillTemplate `yaml:"templates"`
}

var (
	builtinSkillTemplateOnce sync.Once
	builtinSkillTemplates    []SkillTemplate
	builtinSkillTemplateErr  error
)

// BuiltInSkillTemplates returns embedded SKILL.md scaffold templates.
func BuiltInSkillTemplates() []SkillTemplate {
	templates, err := loadBuiltInSkillTemplates()
	if err != nil {
		panic(err)
	}
	return cloneSkillTemplates(templates)
}

// BuiltInSkillTemplateByName returns one embedded SKILL.md scaffold template.
func BuiltInSkillTemplateByName(name string) (SkillTemplate, bool) {
	normalized := normalizeName(name)
	for _, template := range BuiltInSkillTemplates() {
		if normalizeName(template.Name) == normalized {
			return template, true
		}
	}
	return SkillTemplate{}, false
}

// SkillTemplatesFromDirs loads embedded templates, then applies runtime overrides.
func SkillTemplatesFromDirs(dirs ...string) ([]SkillTemplate, error) {
	templates := BuiltInSkillTemplates()
	indexes := make(map[string]int, len(templates))
	for i, template := range templates {
		indexes[normalizeName(template.Name)] = i
	}
	for _, dir := range dirs {
		custom, err := loadSkillTemplateDir(dir)
		if err != nil {
			return nil, err
		}
		for _, template := range custom {
			template = normalizeSkillTemplate(template)
			name := normalizeName(template.Name)
			if name == "" {
				continue
			}
			if index, exists := indexes[name]; exists {
				templates[index] = template
				continue
			}
			indexes[name] = len(templates)
			templates = append(templates, template)
		}
	}
	return cloneSkillTemplates(templates), nil
}

func loadBuiltInSkillTemplates() ([]SkillTemplate, error) {
	builtinSkillTemplateOnce.Do(func() {
		data, err := embeddedSkillTemplateFS.ReadFile("templates/skills/scaffolds/templates.yaml")
		if err != nil {
			builtinSkillTemplateErr = fmt.Errorf("read built-in skill scaffold templates: %w", err)
			return
		}
		var file skillTemplateFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			builtinSkillTemplateErr = fmt.Errorf("parse built-in skill scaffold templates: %w", err)
			return
		}
		if len(file.Templates) == 0 {
			builtinSkillTemplateErr = fmt.Errorf("built-in skill scaffold templates are empty")
			return
		}
		seen := make(map[string]struct{}, len(file.Templates))
		for _, template := range file.Templates {
			template = normalizeSkillTemplate(template)
			name := normalizeName(template.Name)
			if name == "" {
				builtinSkillTemplateErr = fmt.Errorf("built-in skill scaffold template has empty name")
				return
			}
			if _, exists := seen[name]; exists {
				builtinSkillTemplateErr = fmt.Errorf("duplicate built-in skill scaffold template %q", template.Name)
				return
			}
			seen[name] = struct{}{}
			builtinSkillTemplates = append(builtinSkillTemplates, template)
		}
	})
	if builtinSkillTemplateErr != nil {
		return nil, builtinSkillTemplateErr
	}
	return builtinSkillTemplates, nil
}

func loadSkillTemplateDir(dir string) ([]SkillTemplate, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skill scaffold template directory %s: %w", dir, err)
	}
	out := make([]SkillTemplate, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		items, err := loadSkillTemplateFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadSkillTemplateFile(path string) ([]SkillTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill scaffold template %s: %w", path, err)
	}
	var file skillTemplateFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse skill scaffold template %s: %w", path, err)
	}
	if len(file.Templates) > 0 {
		return file.Templates, nil
	}
	var template SkillTemplate
	if err := yaml.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("parse skill scaffold template %s: %w", path, err)
	}
	if strings.TrimSpace(template.Name) == "" {
		return nil, fmt.Errorf("skill scaffold template %s does not define name or templates", path)
	}
	return []SkillTemplate{template}, nil
}

func normalizeSkillTemplate(template SkillTemplate) SkillTemplate {
	template.Name = normalizeName(template.Name)
	template.Description = strings.TrimSpace(template.Description)
	template.Mode = strings.TrimSpace(template.Mode)
	template.PreferredAgent = normalizeName(template.PreferredAgent)
	template.OutputKind = strings.TrimSpace(template.OutputKind)
	template.ExampleRequest = strings.TrimSpace(template.ExampleRequest)
	template.Body = strings.TrimSpace(template.Body)
	template.AllowedToolKinds = normalizeStringList(template.AllowedToolKinds, true)
	template.NextSkills = normalizeStringList(template.NextSkills, false)
	template.Keywords = normalizeStringList(template.Keywords, true)
	for i := range template.Tools {
		template.Tools[i].Name = strings.TrimSpace(template.Tools[i].Name)
	}
	return template
}

func cloneSkillTemplates(templates []SkillTemplate) []SkillTemplate {
	out := make([]SkillTemplate, len(templates))
	for i, template := range templates {
		out[i] = cloneSkillTemplate(template)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Mode != out[j].Mode {
			return out[i].Mode < out[j].Mode
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneSkillTemplate(template SkillTemplate) SkillTemplate {
	template.AllowedToolKinds = append([]string(nil), template.AllowedToolKinds...)
	template.NextSkills = append([]string(nil), template.NextSkills...)
	template.Tools = append([]SkillTemplateTool(nil), template.Tools...)
	template.Keywords = append([]string(nil), template.Keywords...)
	return template
}
