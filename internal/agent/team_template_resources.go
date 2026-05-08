package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	teamTemplateResourceKind                = "goflow.team_template_resource"
	teamTemplateResourceVersion             = 2
	teamTemplateResourceMinSupportedVersion = 1
)

// TeamTemplateResourceSummary is a persisted team template row for Studio catalogs.
type TeamTemplateResourceSummary struct {
	Kind                string   `json:"kind,omitempty"`
	Name                string   `json:"name"`
	Title               string   `json:"title,omitempty"`
	Description         string   `json:"description,omitempty"`
	Category            string   `json:"category,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Version             int      `json:"version,omitempty"`
	MinVersion          int      `json:"min_supported_version,omitempty"`
	MigratedFromVersion int      `json:"migrated_from_version,omitempty"`
	Roles               int      `json:"roles,omitempty"`
	QuorumPresets       int      `json:"quorum_presets,omitempty"`
	Path                string   `json:"path,omitempty"`
	Valid               bool     `json:"valid"`
	Error               string   `json:"error,omitempty"`
}

// TeamTemplateResources lists user-editable team template resources.
func (r *Runtime) TeamTemplateResources() []TeamTemplateResourceSummary {
	return NewWorkflowRunner(r).TeamTemplateResources()
}

// SaveTeamTemplateResource saves a user-editable team template resource.
func (r *Runtime) SaveTeamTemplateResource(name string, template TeamTemplate) (TeamTemplate, error) {
	return NewWorkflowRunner(r).SaveTeamTemplateResource(name, template)
}

// ValidateTeamTemplateResource validates and normalizes a user-editable team template without writing it.
func (r *Runtime) ValidateTeamTemplateResource(name string, template TeamTemplate) (TeamTemplate, error) {
	return NewWorkflowRunner(r).ValidateTeamTemplateResource(name, template)
}

// DeleteTeamTemplateResource deletes a user-editable team template resource.
func (r *Runtime) DeleteTeamTemplateResource(name string) error {
	return NewWorkflowRunner(r).DeleteTeamTemplateResource(name)
}

// TeamTemplates returns built-in and custom team summaries for this runtime.
func (r *Runtime) TeamTemplates() []TeamTemplateSummary {
	return NewWorkflowRunner(r).TeamTemplates()
}

// TeamTemplate loads one built-in or custom team template for this runtime.
func (r *Runtime) TeamTemplate(name string) (TeamTemplate, bool) {
	return NewWorkflowRunner(r).TeamTemplate(name)
}

// TeamTemplateResources lists user-editable team template resources.
func (w *WorkflowRunner) TeamTemplateResources() []TeamTemplateResourceSummary {
	root, err := w.teamTemplateResourceRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []TeamTemplateResourceSummary{{Name: "(team templates)", Path: root, Valid: false, Error: err.Error()}}
	}
	out := make([]TeamTemplateResourceSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isTeamTemplateResourceFile(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		path, err := w.teamTemplateResourcePathForRead(name)
		if err != nil {
			out = append(out, TeamTemplateResourceSummary{Name: name, Valid: false, Error: err.Error()})
			continue
		}
		template, err := w.loadTeamTemplateResourcePath(path)
		if err != nil {
			out = append(out, TeamTemplateResourceSummary{Name: name, Path: path, Valid: false, Error: err.Error()})
			continue
		}
		out = append(out, teamTemplateResourceSummary(template, path))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveTeamTemplateResource saves a user-editable team template resource.
func (w *WorkflowRunner) SaveTeamTemplateResource(name string, template TeamTemplate) (TeamTemplate, error) {
	template, err := w.ValidateTeamTemplateResource(name, template)
	if err != nil {
		return TeamTemplate{}, err
	}
	path, err := w.teamTemplateResourcePathForWrite(template.Name)
	if err != nil {
		return TeamTemplate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return TeamTemplate{}, err
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return TeamTemplate{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return TeamTemplate{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return TeamTemplate{}, err
	}
	template.Path = path
	return template, nil
}

// ValidateTeamTemplateResource validates and normalizes a user-editable team template resource without writing it.
func (w *WorkflowRunner) ValidateTeamTemplateResource(name string, template TeamTemplate) (TeamTemplate, error) {
	name = normalizePersistedWorkflowName(firstNonEmptyTeamTemplateName(name, template.Name))
	if err := validatePersistedWorkflowName(name); err != nil {
		return TeamTemplate{}, err
	}
	if strings.TrimSpace(template.Name) == "" {
		template.Name = name
	}
	if err := validateTeamTemplateResourceVersion(template.Kind, template.Version, template.MinVersion); err != nil {
		return TeamTemplate{}, err
	}
	if normalizePersistedWorkflowName(template.Name) != name {
		return TeamTemplate{}, fmt.Errorf("team template name %q does not match %q", template.Name, name)
	}
	template = normalizeTeamTemplateResource(template, false)
	if err := validateTeamTemplateResource(template); err != nil {
		return TeamTemplate{}, err
	}
	template.Source = "custom"
	template.Custom = true
	template.Roles = len(template.RoleTemplates)
	template.QuorumPresetCount = len(template.QuorumPresets)
	return template, nil
}

// DeleteTeamTemplateResource deletes a user-editable team template resource.
func (w *WorkflowRunner) DeleteTeamTemplateResource(name string) error {
	path, err := w.teamTemplateResourcePathForRead(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (w *WorkflowRunner) customTeamTemplateMap() map[string]TeamTemplate {
	resources := w.TeamTemplateResources()
	if len(resources) == 0 {
		return nil
	}
	out := make(map[string]TeamTemplate, len(resources))
	for _, resource := range resources {
		if !resource.Valid {
			continue
		}
		template, ok := w.customTeamTemplate(resource.Name)
		if ok {
			out[normalizePersistedWorkflowName(resource.Name)] = template
		}
	}
	return out
}

func (w *WorkflowRunner) customTeamTemplate(name string) (TeamTemplate, bool) {
	path, err := w.teamTemplateResourcePathForRead(name)
	if err != nil {
		return TeamTemplate{}, false
	}
	template, err := w.loadTeamTemplateResourcePath(path)
	if err != nil {
		return TeamTemplate{}, false
	}
	return template, true
}

func (w *WorkflowRunner) loadTeamTemplateResourcePath(path string) (TeamTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TeamTemplate{}, err
	}
	var template TeamTemplate
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &template); err != nil {
			return TeamTemplate{}, fmt.Errorf("parse team template json: %w", err)
		}
	default:
		if err := yaml.Unmarshal(data, &template); err != nil {
			return TeamTemplate{}, fmt.Errorf("parse team template yaml: %w", err)
		}
	}
	if err := validateTeamTemplateResourceVersion(template.Kind, template.Version, template.MinVersion); err != nil {
		return TeamTemplate{}, err
	}
	migrated := template.Version == 0
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.TrimSpace(template.Name) == "" {
		template.Name = name
	}
	template = normalizeTeamTemplateResource(template, migrated)
	if err := validateTeamTemplateResource(template); err != nil {
		return TeamTemplate{}, err
	}
	template.Source = "custom"
	template.Custom = true
	template.Path = path
	template.Roles = len(template.RoleTemplates)
	template.QuorumPresetCount = len(template.QuorumPresets)
	return template, nil
}

func (w *WorkflowRunner) teamTemplateResourceRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "templates", "teams"), nil
}

func (w *WorkflowRunner) teamTemplateResourcePathForWrite(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.teamTemplateResourceRoot()
	if err != nil {
		return "", err
	}
	return secureTeamTemplateResourcePath(root, name+".yaml")
}

func (w *WorkflowRunner) teamTemplateResourcePathForRead(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.teamTemplateResourceRoot()
	if err != nil {
		return "", err
	}
	for _, ext := range []string{".yaml", ".yml", ".json"} {
		path, err := secureTeamTemplateResourcePath(root, name+ext)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func secureTeamTemplateResourcePath(root, file string) (string, error) {
	path := filepath.Clean(filepath.Join(root, file))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("team template path escapes runtime templates directory")
	}
	return path, nil
}

func isTeamTemplateResourceFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func firstNonEmptyTeamTemplateName(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func teamTemplateResourceSummary(template TeamTemplate, path string) TeamTemplateResourceSummary {
	return TeamTemplateResourceSummary{
		Kind:                template.Kind,
		Name:                template.Name,
		Title:               template.Title,
		Description:         template.Description,
		Category:            template.Category,
		Tags:                append([]string(nil), template.Tags...),
		Version:             template.Version,
		MinVersion:          template.MinVersion,
		MigratedFromVersion: template.MigratedFromVersion,
		Roles:               len(template.RoleTemplates),
		QuorumPresets:       len(template.QuorumPresets),
		Path:                path,
		Valid:               true,
	}
}

func validateTeamTemplateResourceVersion(kind string, version, minVersion int) error {
	if strings.TrimSpace(kind) != "" && strings.TrimSpace(kind) != teamTemplateResourceKind {
		return fmt.Errorf("unsupported team template resource kind %q", kind)
	}
	if version == 0 {
		version = teamTemplateResourceMinSupportedVersion
	}
	if version < teamTemplateResourceMinSupportedVersion {
		return fmt.Errorf("team template resource version %d is no longer supported", version)
	}
	if version > teamTemplateResourceVersion {
		return fmt.Errorf("team template resource version %d is newer than supported version %d", version, teamTemplateResourceVersion)
	}
	if minVersion > teamTemplateResourceVersion {
		return fmt.Errorf("team template resource requires reader version %d, supported version is %d", minVersion, teamTemplateResourceVersion)
	}
	return nil
}

func normalizeTeamTemplateResource(template TeamTemplate, migrated bool) TeamTemplate {
	original := template.Version
	if original == 0 {
		original = teamTemplateResourceMinSupportedVersion
	}
	template.Kind = teamTemplateResourceKind
	template.Version = teamTemplateResourceVersion
	template.MinVersion = teamTemplateResourceMinSupportedVersion
	if (migrated || original != teamTemplateResourceVersion) && template.MigratedFromVersion == 0 {
		template.MigratedFromVersion = original
	}
	template.Name = normalizePersistedWorkflowName(template.Name)
	if strings.TrimSpace(template.Title) == "" {
		template.Title = template.Name
	}
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	template.Roles = len(template.RoleTemplates)
	template.QuorumPresetCount = len(template.QuorumPresets)
	return template
}

func validateTeamTemplateResource(template TeamTemplate) error {
	if err := validatePersistedWorkflowName(normalizePersistedWorkflowName(template.Name)); err != nil {
		return err
	}
	if strings.TrimSpace(template.Title) == "" {
		return fmt.Errorf("team template %s title is required", template.Name)
	}
	if len(template.RoleTemplates) == 0 {
		return fmt.Errorf("team template %s requires at least one role", template.Name)
	}
	roles := make(map[string]struct{}, len(template.RoleTemplates))
	for i, role := range template.RoleTemplates {
		roleName := normalizePersistedWorkflowName(role.Name)
		if err := validatePersistedWorkflowName(roleName); err != nil {
			return fmt.Errorf("team template %s role %d has invalid name: %w", template.Name, i, err)
		}
		if _, exists := roles[roleName]; exists {
			return fmt.Errorf("team template %s has duplicate role %q", template.Name, role.Name)
		}
		roles[roleName] = struct{}{}
		if strings.TrimSpace(role.Agent) == "" {
			return fmt.Errorf("team template %s role %s agent is required", template.Name, role.Name)
		}
	}
	for _, handoff := range template.Handoffs {
		from := normalizePersistedWorkflowName(handoff.From)
		to := normalizePersistedWorkflowName(handoff.To)
		if from == "" || to == "" {
			return fmt.Errorf("team template %s handoff requires from and to roles", template.Name)
		}
		if _, ok := roles[from]; !ok {
			return fmt.Errorf("team template %s handoff references unknown from role %q", template.Name, handoff.From)
		}
		if _, ok := roles[to]; !ok {
			return fmt.Errorf("team template %s handoff references unknown to role %q", template.Name, handoff.To)
		}
	}
	for _, item := range template.BlackboardTemplates {
		if strings.TrimSpace(item.Kind) == "" {
			return fmt.Errorf("team template %s blackboard item kind is required", template.Name)
		}
		if owner := normalizePersistedWorkflowName(item.OwnerRole); owner != "" {
			if _, ok := roles[owner]; !ok {
				return fmt.Errorf("team template %s blackboard item %s references unknown owner role %q", template.Name, item.Kind, item.OwnerRole)
			}
		}
	}
	presets := make(map[string]struct{}, len(template.QuorumPresets))
	for i, preset := range template.QuorumPresets {
		name := normalizePersistedWorkflowName(preset.Name)
		if err := validatePersistedWorkflowName(name); err != nil {
			return fmt.Errorf("team template %s quorum preset %d has invalid name: %w", template.Name, i, err)
		}
		if _, exists := presets[name]; exists {
			return fmt.Errorf("team template %s has duplicate quorum preset %q", template.Name, preset.Name)
		}
		presets[name] = struct{}{}
		if preset.Required < 0 {
			return fmt.Errorf("team template %s quorum preset %s required must not be negative", template.Name, preset.Name)
		}
		for _, role := range preset.Roles {
			if _, ok := roles[normalizePersistedWorkflowName(role)]; !ok {
				return fmt.Errorf("team template %s quorum preset %s references unknown role %q", template.Name, preset.Name, role)
			}
		}
		if preset.Required == 0 && len(preset.Roles) == 0 {
			return fmt.Errorf("team template %s quorum preset %s requires required or roles", template.Name, preset.Name)
		}
	}
	return nil
}
