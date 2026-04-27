package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

var resourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type skillResourceDocument struct {
	Name             string              `json:"name" yaml:"name"`
	Description      string              `json:"description" yaml:"description"`
	Version          string              `json:"version" yaml:"version"`
	Author           string              `json:"author" yaml:"author"`
	Mode             string              `json:"mode" yaml:"mode"`
	PreferredAgent   string              `json:"preferred_agent" yaml:"preferred_agent"`
	AllowedToolKinds []string            `json:"allowed_tool_kinds" yaml:"allowed_tool_kinds"`
	OutputKind       string              `json:"output_kind" yaml:"output_kind"`
	Priority         int                 `json:"priority,omitempty" yaml:"priority,omitempty"`
	MaxIterations    int                 `json:"max_iterations,omitempty" yaml:"max_iterations,omitempty"`
	NextSkills       []string            `json:"next_skills,omitempty" yaml:"next_skills,omitempty"`
	Tools            []schema.SkillTool  `json:"tools,omitempty" yaml:"tools,omitempty"`
	Params           []schema.SkillParam `json:"params,omitempty" yaml:"params,omitempty"`
	Activation       schema.Activation   `json:"activation" yaml:"activation"`
	Metadata         map[string]string   `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Instructions     string              `json:"instructions"`
	Path             string              `json:"path,omitempty"`
}

type skillResourceMeta struct {
	Name             string              `yaml:"name"`
	Description      string              `yaml:"description"`
	Version          string              `yaml:"version"`
	Author           string              `yaml:"author"`
	Mode             string              `yaml:"mode"`
	PreferredAgent   string              `yaml:"preferred_agent"`
	AllowedToolKinds []string            `yaml:"allowed_tool_kinds"`
	OutputKind       string              `yaml:"output_kind"`
	Priority         int                 `yaml:"priority,omitempty"`
	MaxIterations    int                 `yaml:"max_iterations,omitempty"`
	NextSkills       []string            `yaml:"next_skills,omitempty"`
	Tools            []schema.SkillTool  `yaml:"tools,omitempty"`
	Params           []schema.SkillParam `yaml:"params,omitempty"`
	Activation       schema.Activation   `yaml:"activation"`
	Metadata         map[string]string   `yaml:"metadata,omitempty"`
}

func (s *Server) handleSkillResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.SkillList())
}

func (s *Server) handleSkillResourceItem(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/skills/"), "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, ok := s.skillResourceByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc skillResourceDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		saved, err := s.saveSkillResource(name, doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) skillResourceByName(name string) (skillResourceDocument, bool) {
	normalized := normalizeResourceName(name)
	for _, item := range s.runtime.SkillList() {
		if normalizeResourceName(item.Name) == normalized {
			return skillResourceFromSchema(item), true
		}
	}
	return skillResourceDocument{}, false
}

func (s *Server) saveSkillResource(name string, doc skillResourceDocument) (skillResourceDocument, error) {
	name = normalizeResourceName(name)
	if !resourceNamePattern.MatchString(name) {
		return skillResourceDocument{}, fmt.Errorf("invalid skill name %q: use lowercase letters, numbers, hyphen, or underscore", name)
	}
	doc.Name = name
	applySkillResourceDefaults(&doc)
	root := strings.TrimSpace(s.runtime.SkillRoot())
	if root == "" {
		return skillResourceDocument{}, fmt.Errorf("skill root is not configured")
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return skillResourceDocument{}, fmt.Errorf("resolve skill root: %w", err)
	}
	dir := filepath.Clean(filepath.Join(root, name))
	if !referenceWithinBase(root, dir) {
		return skillResourceDocument{}, fmt.Errorf("skill path escapes skill root")
	}
	path := filepath.Join(dir, "SKILL.md")
	content, err := renderSkillResource(doc)
	if err != nil {
		return skillResourceDocument{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return skillResourceDocument{}, fmt.Errorf("create skill directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return skillResourceDocument{}, fmt.Errorf("write skill: %w", err)
	}
	parsed, err := skill.ParseFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return skillResourceDocument{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return skillResourceDocument{}, fmt.Errorf("replace skill: %w", err)
	}
	if err := s.runtime.ReloadSkills(); err != nil {
		return skillResourceDocument{}, fmt.Errorf("reload skills: %w", err)
	}
	saved := skillResourceFromSchema(*parsed)
	saved.Path = path
	return saved, nil
}

func skillResourceFromSchema(item schema.Skill) skillResourceDocument {
	return skillResourceDocument{
		Name:             item.Name,
		Description:      item.Description,
		Version:          item.Version,
		Author:           item.Author,
		Mode:             item.Mode,
		PreferredAgent:   item.PreferredAgent,
		AllowedToolKinds: append([]string(nil), item.AllowedToolKinds...),
		OutputKind:       item.OutputKind,
		Priority:         item.Priority,
		MaxIterations:    item.MaxIterations,
		NextSkills:       append([]string(nil), item.NextSkills...),
		Tools:            append([]schema.SkillTool(nil), item.Tools...),
		Params:           append([]schema.SkillParam(nil), item.Params...),
		Activation:       item.Activation,
		Metadata:         copyMap(item.Metadata),
		Instructions:     item.Instructions,
		Path:             item.Path,
	}
}

func applySkillResourceDefaults(doc *skillResourceDocument) {
	doc.Name = normalizeResourceName(doc.Name)
	if strings.TrimSpace(doc.Version) == "" {
		doc.Version = "1.0.0"
	}
	if strings.TrimSpace(doc.Author) == "" {
		doc.Author = "GoFlow Studio"
	}
	if strings.TrimSpace(doc.Mode) == "" {
		doc.Mode = "chat"
	}
	if strings.TrimSpace(doc.PreferredAgent) == "" {
		doc.PreferredAgent = "chat"
	}
	if len(doc.AllowedToolKinds) == 0 {
		doc.AllowedToolKinds = []string{"read"}
	}
	if strings.TrimSpace(doc.OutputKind) == "" {
		doc.OutputKind = "summary"
	}
	if len(doc.Activation.Keywords) == 0 {
		doc.Activation.Keywords = []string{doc.Name}
	}
	if strings.TrimSpace(doc.Description) == "" {
		doc.Description = "Custom GoFlow skill."
	}
	if strings.TrimSpace(doc.Instructions) == "" {
		doc.Instructions = "## Role\n\nDescribe what this skill should do.\n\n## Workflow\n\n1. Inspect relevant context.\n2. Execute the task safely.\n3. Summarize results and verification."
	}
}

func renderSkillResource(doc skillResourceDocument) (string, error) {
	meta := skillResourceMeta{
		Name:             doc.Name,
		Description:      doc.Description,
		Version:          doc.Version,
		Author:           doc.Author,
		Mode:             doc.Mode,
		PreferredAgent:   doc.PreferredAgent,
		AllowedToolKinds: doc.AllowedToolKinds,
		OutputKind:       doc.OutputKind,
		Priority:         doc.Priority,
		MaxIterations:    doc.MaxIterations,
		NextSkills:       doc.NextSkills,
		Tools:            doc.Tools,
		Params:           doc.Params,
		Activation:       doc.Activation,
		Metadata:         doc.Metadata,
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("render skill frontmatter: %w", err)
	}
	instructions := strings.TrimSpace(doc.Instructions)
	return "---\n" + string(data) + "---\n\n" + instructions + "\n", nil
}

func normalizeResourceName(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}

func copyMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func sortedSkillNames(skills []schema.Skill) []string {
	names := make([]string, 0, len(skills))
	for _, item := range skills {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names
}
