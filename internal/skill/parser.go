package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

var skillNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)
var skillScriptNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type frontmatterSkill struct {
	Name             string               `yaml:"name"`
	Description      string               `yaml:"description"`
	Version          string               `yaml:"version"`
	Author           string               `yaml:"author"`
	Format           string               `yaml:"format"`
	Tools            []schema.SkillTool   `yaml:"tools"`
	AllowedTools     flexibleStringList   `yaml:"allowed-tools"`
	AllowedToolsAlt  flexibleStringList   `yaml:"allowed_tools"`
	Params           []schema.SkillParam  `yaml:"params"`
	Scripts          []schema.SkillScript `yaml:"scripts"`
	Activation       schema.Activation    `yaml:"activation"`
	Mode             string               `yaml:"mode"`
	PreferredAgent   string               `yaml:"preferred_agent"`
	AllowedToolKinds []string             `yaml:"allowed_tool_kinds"`
	OutputKind       string               `yaml:"output_kind"`
	Priority         int                  `yaml:"priority"`
	MaxIterations    int                  `yaml:"max_iterations"`
	NextSkills       []string             `yaml:"next_skills"`
	Metadata         map[string]string    `yaml:"metadata"`
}

type flexibleStringList []string

func (l *flexibleStringList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	var values []string
	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			if item == nil {
				continue
			}
			values = append(values, splitFlexibleListValue(item.Value)...)
		}
	case yaml.ScalarNode:
		values = append(values, splitFlexibleListValue(value.Value)...)
	default:
		return fmt.Errorf("expected string or list")
	}
	*l = append((*l)[:0], uniqueNonEmptyStrings(values)...)
	return nil
}

// ParseFile parses a SKILL.md file with YAML frontmatter and markdown instructions.
func ParseFile(path string) (*schema.Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill: %w", err)
	}
	content := string(data)
	metaText, instructions, err := splitSkillFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("skill %s %w", path, err)
	}

	var meta frontmatterSkill
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	normalized := normalizeSkillMeta(path, meta)
	if err := validateSkillMeta(path, normalized); err != nil {
		return nil, err
	}
	resources, err := discoverSkillResources(filepath.Dir(path))
	if err != nil {
		return nil, err
	}

	return &schema.Skill{
		Name:             strings.TrimSpace(normalized.Name),
		Description:      strings.TrimSpace(normalized.Description),
		Version:          strings.TrimSpace(normalized.Version),
		Author:           strings.TrimSpace(normalized.Author),
		Format:           strings.TrimSpace(normalized.Format),
		Tools:            normalized.Tools,
		Params:           normalized.Params,
		Scripts:          normalized.Scripts,
		Activation:       normalized.Activation,
		Instructions:     strings.TrimSpace(instructions),
		Path:             path,
		Resources:        resources,
		Mode:             strings.TrimSpace(normalized.Mode),
		PreferredAgent:   strings.TrimSpace(normalized.PreferredAgent),
		AllowedToolKinds: normalized.AllowedToolKinds,
		OutputKind:       strings.TrimSpace(normalized.OutputKind),
		Priority:         normalized.Priority,
		MaxIterations:    normalized.MaxIterations,
		NextSkills:       normalized.NextSkills,
		Metadata:         normalized.Metadata,
	}, nil
}

func validateSkillMeta(path string, meta frontmatterSkill) error {
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Description) == "" {
		return fmt.Errorf("skill %s missing required name or description", path)
	}
	if !skillNamePattern.MatchString(meta.Name) {
		return fmt.Errorf("skill %s has invalid name %q: use letters, numbers, spaces, underscores, or hyphens", path, meta.Name)
	}
	for _, tool := range meta.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("skill %s has a tool with an empty name", path)
		}
	}
	if err := validateSkillScripts(path, meta.Scripts); err != nil {
		return err
	}
	if isStrictGoFlowSkill(meta) {
		if strings.TrimSpace(meta.Version) == "" {
			return fmt.Errorf("skill %s missing version", path)
		}
		if strings.TrimSpace(meta.Author) == "" {
			return fmt.Errorf("skill %s missing author", path)
		}
		if len(meta.Activation.Keywords) == 0 {
			return fmt.Errorf("skill %s must define at least one activation keyword", path)
		}
		folder := filepath.Base(filepath.Dir(path))
		normalizedName := normalizeForMatch(meta.Name)
		normalizedFolder := normalizeForMatch(folder)
		if normalizedName != "" && normalizedFolder != "" && normalizedName != normalizedFolder {
			return fmt.Errorf("skill %s name %q should align with folder %q", path, meta.Name, folder)
		}
	}
	return nil
}

func validateSkillScripts(path string, scripts []schema.SkillScript) error {
	seen := map[string]struct{}{}
	for _, script := range normalizeSkillScripts(scripts) {
		if script.Name == "" {
			return fmt.Errorf("skill %s has a script with an empty name", path)
		}
		if !skillScriptNamePattern.MatchString(script.Name) {
			return fmt.Errorf("skill %s script %q has invalid name: use letters, numbers, underscores, or hyphens", path, script.Name)
		}
		key := strings.ToLower(script.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("skill %s has duplicate script %q", path, script.Name)
		}
		seen[key] = struct{}{}
		if script.Path == "" {
			return fmt.Errorf("skill %s script %q missing path", path, script.Name)
		}
		if filepath.IsAbs(script.Path) || strings.HasPrefix(script.Path, "../") || strings.Contains(script.Path, "/../") {
			return fmt.Errorf("skill %s script %q path must stay inside the skill scripts directory", path, script.Name)
		}
		if !strings.HasPrefix(script.Path, "scripts/") {
			return fmt.Errorf("skill %s script %q path must start with scripts/", path, script.Name)
		}
		if script.Timeout != "" {
			if _, err := time.ParseDuration(script.Timeout); err != nil {
				return fmt.Errorf("skill %s script %q has invalid timeout %q: %w", path, script.Name, script.Timeout, err)
			}
		}
		if script.Runtime != "" && !allowedSkillScriptValue(script.Runtime, "python", "node", "bash", "powershell", "go", "binary") {
			return fmt.Errorf("skill %s script %q has unsupported runtime %q", path, script.Name, script.Runtime)
		}
		if script.Output != "" && !allowedSkillScriptValue(script.Output, "text", "json", "files", "artifact") {
			return fmt.Errorf("skill %s script %q has unsupported output %q", path, script.Name, script.Output)
		}
		if script.Isolation != "" && !allowedSkillScriptValue(script.Isolation, "container", "process_group", "windows_job", "windows_restricted_token", "linux_cgroup", "linux_netns") {
			return fmt.Errorf("skill %s script %q has unsupported isolation %q", path, script.Name, script.Isolation)
		}
		if script.WorkspaceMount != "" && !allowedSkillScriptValue(script.WorkspaceMount, "none", "ro", "rw") {
			return fmt.Errorf("skill %s script %q has unsupported workspace_mount %q", path, script.Name, script.WorkspaceMount)
		}
		if script.Network != "" && !allowedSkillScriptValue(script.Network, "disabled", "enabled", "host") {
			return fmt.Errorf("skill %s script %q has unsupported network %q", path, script.Name, script.Network)
		}
		if script.Approval != "" && !allowedSkillScriptValue(script.Approval, "required", "optional", "never") {
			return fmt.Errorf("skill %s script %q has unsupported approval %q", path, script.Name, script.Approval)
		}
		if script.ArgsSchema != nil {
			if typ, _ := script.ArgsSchema["type"].(string); typ != "" && typ != "object" {
				return fmt.Errorf("skill %s script %q args_schema must be an object schema", path, script.Name)
			}
			if additional, ok := script.ArgsSchema["additionalProperties"].(bool); ok && additional {
				return fmt.Errorf("skill %s script %q args_schema should set additionalProperties: false", path, script.Name)
			}
		}
	}
	return nil
}

func allowedSkillScriptValue(value string, allowed ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func splitSkillFrontmatter(content string) (string, string, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", "", fmt.Errorf("missing YAML frontmatter")
	}
	rest := normalized[len("---\n"):]
	index := strings.Index(rest, "\n---")
	if index < 0 {
		return "", "", fmt.Errorf("missing closing YAML frontmatter delimiter")
	}
	meta := rest[:index]
	body := rest[index+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	return meta, body, nil
}

func normalizeSkillMeta(path string, meta frontmatterSkill) frontmatterSkill {
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	meta.Version = strings.TrimSpace(meta.Version)
	meta.Author = strings.TrimSpace(meta.Author)
	meta.Format = strings.TrimSpace(meta.Format)
	if meta.Metadata == nil {
		meta.Metadata = map[string]string{}
	}
	if meta.Version == "" && meta.Author == "" && len(meta.Activation.Keywords) == 0 {
		meta.Format = fallbackSkillFormat(meta)
	}
	if meta.Format == "" {
		meta.Format = "goflow"
	}
	if _, ok := meta.Metadata["format"]; !ok {
		meta.Metadata["format"] = meta.Format
	}
	folder := filepath.Base(filepath.Dir(path))
	if _, ok := meta.Metadata["folder"]; !ok && strings.TrimSpace(folder) != "" {
		meta.Metadata["folder"] = folder
	}
	if len(meta.Activation.Keywords) == 0 {
		meta.Activation.Keywords = deriveActivationKeywords(meta.Name, meta.Description)
	}
	meta.Scripts = normalizeSkillScripts(meta.Scripts)
	allowedTools := uniqueNonEmptyStrings(append([]string(meta.AllowedTools), []string(meta.AllowedToolsAlt)...))
	if len(allowedTools) > 0 {
		meta.Metadata["source_allowed_tools"] = strings.Join(allowedTools, ", ")
		meta.Tools = mergeSkillTools(meta.Tools, translateAllowedToolsToSkillTools(allowedTools))
		if len(meta.AllowedToolKinds) == 0 {
			meta.AllowedToolKinds = translateAllowedToolsToKinds(allowedTools)
		}
	}
	return meta
}

func normalizeSkillScripts(scripts []schema.SkillScript) []schema.SkillScript {
	out := make([]schema.SkillScript, 0, len(scripts))
	for _, script := range scripts {
		script.Name = strings.TrimSpace(script.Name)
		script.Description = strings.TrimSpace(script.Description)
		script.Path = normalizeSkillScriptPath(script.Path)
		script.Runtime = strings.ToLower(strings.TrimSpace(script.Runtime))
		script.Output = strings.ToLower(strings.TrimSpace(script.Output))
		script.Timeout = strings.TrimSpace(script.Timeout)
		script.Isolation = strings.ToLower(strings.TrimSpace(script.Isolation))
		script.WorkspaceMount = strings.ToLower(strings.TrimSpace(script.WorkspaceMount))
		script.Network = strings.ToLower(strings.TrimSpace(script.Network))
		script.Approval = strings.ToLower(strings.TrimSpace(script.Approval))
		if script.Approval == "" {
			script.Approval = "required"
		}
		if script.Metadata == nil {
			script.Metadata = map[string]string{}
		}
		out = append(out, script)
	}
	return out
}

func normalizeSkillScriptPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." {
		return ""
	}
	return value
}

func isStrictGoFlowSkill(meta frontmatterSkill) bool {
	return strings.EqualFold(strings.TrimSpace(meta.Format), "goflow") || strings.TrimSpace(meta.Version) != "" || strings.TrimSpace(meta.Author) != ""
}

func fallbackSkillFormat(meta frontmatterSkill) string {
	if strings.TrimSpace(meta.Format) != "" {
		return strings.TrimSpace(meta.Format)
	}
	if len(meta.AllowedTools) > 0 || len(meta.AllowedToolsAlt) > 0 {
		return "claude-compatible"
	}
	if len(meta.Metadata) > 0 {
		return "codex-compatible"
	}
	return "portable-skill"
}

func deriveActivationKeywords(name, description string) []string {
	values := []string{name}
	values = append(values, splitNameKeywords(name)...)
	values = append(values, significantDescriptionKeywords(description)...)
	return uniqueNonEmptyStrings(values)
}

func splitNameKeywords(name string) []string {
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", "\\", " ")
	parts := strings.Fields(replacer.Replace(name))
	keywords := make([]string, 0, len(parts))
	for _, part := range parts {
		if len([]rune(part)) >= 3 {
			keywords = append(keywords, part)
		}
	}
	return keywords
}

func significantDescriptionKeywords(description string) []string {
	replacer := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "\n", " ")
	stop := map[string]struct{}{
		"and": {}, "are": {}, "but": {}, "for": {}, "from": {}, "into": {}, "that": {}, "the": {}, "this": {}, "use": {}, "when": {}, "with": {}, "you": {}, "your": {},
	}
	parts := strings.Fields(replacer.Replace(strings.ToLower(description)))
	keywords := make([]string, 0, 8)
	for _, part := range parts {
		part = strings.Trim(part, `"'`)
		if len([]rune(part)) < 4 {
			continue
		}
		if _, blocked := stop[part]; blocked {
			continue
		}
		keywords = append(keywords, part)
		if len(keywords) >= 8 {
			break
		}
	}
	return keywords
}

func splitFlexibleListValue(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			values = append(values, field)
		}
	}
	return values
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
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

func mergeSkillTools(existing, translated []schema.SkillTool) []schema.SkillTool {
	if len(translated) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(translated))
	merged := make([]schema.SkillTool, 0, len(existing)+len(translated))
	for _, tool := range append(existing, translated...) {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tool.Name = name
		merged = append(merged, tool)
	}
	return merged
}

func translateAllowedToolsToSkillTools(values []string) []schema.SkillTool {
	var tools []schema.SkillTool
	for _, value := range values {
		for _, name := range goflowToolNamesForExternalTool(value) {
			tools = append(tools, schema.SkillTool{Name: name})
		}
	}
	return tools
}

func translateAllowedToolsToKinds(values []string) []string {
	var kinds []string
	for _, value := range values {
		kinds = append(kinds, goflowToolKindsForExternalTool(value)...)
	}
	return uniqueNonEmptyStrings(kinds)
}

func goflowToolNamesForExternalTool(value string) []string {
	normalized := normalizeExternalToolName(value)
	switch normalized {
	case "read":
		return []string{"file_tools/read_file"}
	case "grep":
		return []string{"file_tools/search_files"}
	case "glob", "ls", "list":
		return []string{"file_tools/list_tree"}
	case "write", "edit", "multiedit", "notebookedit":
		return []string{"file_tools/write_file"}
	case "bash", "shell":
		return nil
	case "webfetch", "fetch":
		return []string{"web_tools/fetch_url"}
	case "websearch", "search":
		return []string{"web_tools/web_search"}
	default:
		return nil
	}
}

func goflowToolKindsForExternalTool(value string) []string {
	normalized := normalizeExternalToolName(value)
	switch normalized {
	case "read", "grep", "glob", "ls", "list":
		return []string{"read"}
	case "write", "edit", "multiedit", "notebookedit":
		return []string{"write"}
	case "bash", "shell":
		return []string{"exec"}
	case "webfetch", "websearch", "fetch", "search":
		return []string{"network"}
	default:
		return nil
	}
}

func normalizeExternalToolName(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexAny(value, " ("); index >= 0 {
		value = value[:index]
	}
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return strings.ToLower(value)
}

func discoverSkillResources(root string) ([]schema.SkillResource, error) {
	var resources []schema.SkillResource
	for _, dir := range []string{"scripts", "references", "assets", "templates", "agents"} {
		base := filepath.Join(root, dir)
		info, err := os.Stat(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat skill resource dir %s: %w", base, err)
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			resources = append(resources, schema.SkillResource{
				Path: filepath.ToSlash(rel),
				Kind: dir,
				Size: info.Size(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk skill resource dir %s: %w", base, err)
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Path < resources[j].Path
	})
	return resources, nil
}
