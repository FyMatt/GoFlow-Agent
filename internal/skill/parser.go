package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

var skillNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

type frontmatterSkill struct {
	Name             string              `yaml:"name"`
	Description      string              `yaml:"description"`
	Version          string              `yaml:"version"`
	Author           string              `yaml:"author"`
	Tools            []schema.SkillTool  `yaml:"tools"`
	Params           []schema.SkillParam `yaml:"params"`
	Activation       schema.Activation   `yaml:"activation"`
	Mode             string              `yaml:"mode"`
	PreferredAgent   string              `yaml:"preferred_agent"`
	AllowedToolKinds []string            `yaml:"allowed_tool_kinds"`
	OutputKind       string              `yaml:"output_kind"`
	Priority         int                 `yaml:"priority"`
	MaxIterations    int                 `yaml:"max_iterations"`
	NextSkills       []string            `yaml:"next_skills"`
	Metadata         map[string]string   `yaml:"metadata"`
}

// ParseFile parses a SKILL.md file with YAML frontmatter and markdown instructions.
func ParseFile(path string) (*schema.Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill: %w", err)
	}
	content := string(data)
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("skill %s missing YAML frontmatter", path)
	}

	var meta frontmatterSkill
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		return nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	if err := validateSkillMeta(path, meta); err != nil {
		return nil, err
	}

	return &schema.Skill{
		Name:             strings.TrimSpace(meta.Name),
		Description:      strings.TrimSpace(meta.Description),
		Version:          strings.TrimSpace(meta.Version),
		Author:           strings.TrimSpace(meta.Author),
		Tools:            meta.Tools,
		Params:           meta.Params,
		Activation:       meta.Activation,
		Instructions:     strings.TrimSpace(parts[2]),
		Path:             path,
		Mode:             strings.TrimSpace(meta.Mode),
		PreferredAgent:   strings.TrimSpace(meta.PreferredAgent),
		AllowedToolKinds: meta.AllowedToolKinds,
		OutputKind:       strings.TrimSpace(meta.OutputKind),
		Priority:         meta.Priority,
		MaxIterations:    meta.MaxIterations,
		NextSkills:       meta.NextSkills,
		Metadata:         meta.Metadata,
	}, nil
}

func validateSkillMeta(path string, meta frontmatterSkill) error {
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Description) == "" {
		return fmt.Errorf("skill %s missing required name or description", path)
	}
	if !skillNamePattern.MatchString(meta.Name) {
		return fmt.Errorf("skill %s has invalid name %q: use letters, numbers, spaces, underscores, or hyphens", path, meta.Name)
	}
	if strings.TrimSpace(meta.Version) == "" {
		return fmt.Errorf("skill %s missing version", path)
	}
	if strings.TrimSpace(meta.Author) == "" {
		return fmt.Errorf("skill %s missing author", path)
	}
	if len(meta.Activation.Keywords) == 0 {
		return fmt.Errorf("skill %s must define at least one activation keyword", path)
	}
	for _, tool := range meta.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("skill %s has a tool with an empty name", path)
		}
	}
	folder := filepath.Base(filepath.Dir(path))
	normalizedName := normalizeForMatch(meta.Name)
	normalizedFolder := normalizeForMatch(folder)
	if normalizedName != "" && normalizedFolder != "" && normalizedName != normalizedFolder {
		return fmt.Errorf("skill %s name %q should align with folder %q", path, meta.Name, folder)
	}
	return nil
}
