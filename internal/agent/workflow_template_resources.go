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
	workflowTemplateResourceKind                = "goflow.workflow_template_resource"
	workflowTemplateResourceVersion             = 2
	workflowTemplateResourceMinSupportedVersion = 1
)

// WorkflowTemplateResourceSummary is a persisted workflow template row for Studio catalogs.
type WorkflowTemplateResourceSummary struct {
	Kind                string   `json:"kind,omitempty"`
	Name                string   `json:"name"`
	Title               string   `json:"title,omitempty"`
	Description         string   `json:"description,omitempty"`
	Category            string   `json:"category,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Version             int      `json:"version,omitempty"`
	MinVersion          int      `json:"min_supported_version,omitempty"`
	MigratedFromVersion int      `json:"migrated_from_version,omitempty"`
	Stages              int      `json:"stages,omitempty"`
	Path                string   `json:"path,omitempty"`
	Valid               bool     `json:"valid"`
	Error               string   `json:"error,omitempty"`
}

// WorkflowTemplateResources lists user-editable workflow template resources.
func (r *Runtime) WorkflowTemplateResources() []WorkflowTemplateResourceSummary {
	return NewWorkflowRunner(r).WorkflowTemplateResources()
}

// SaveWorkflowTemplateResource saves a user-editable workflow template resource.
func (r *Runtime) SaveWorkflowTemplateResource(name string, template WorkflowTemplate) (WorkflowTemplate, error) {
	return NewWorkflowRunner(r).SaveWorkflowTemplateResource(name, template)
}

// ValidateWorkflowTemplateResource validates and normalizes a user-editable workflow template resource without saving it.
func (r *Runtime) ValidateWorkflowTemplateResource(name string, template WorkflowTemplate) (WorkflowTemplate, error) {
	return NewWorkflowRunner(r).ValidateWorkflowTemplateResource(name, template)
}

// DeleteWorkflowTemplateResource deletes a user-editable workflow template resource.
func (r *Runtime) DeleteWorkflowTemplateResource(name string) error {
	return NewWorkflowRunner(r).DeleteWorkflowTemplateResource(name)
}

// CaptureWorkflowTemplateResource saves an existing workflow graph as a user-editable template resource.
func (r *Runtime) CaptureWorkflowTemplateResource(name, sourceWorkflow, title, description string) (WorkflowTemplate, error) {
	return NewWorkflowRunner(r).CaptureWorkflowTemplateResource(name, sourceWorkflow, title, description)
}

// ForkWorkflowTemplateResource saves a built-in or custom workflow template as a user-editable resource.
func (r *Runtime) ForkWorkflowTemplateResource(name, sourceTemplate, title, description string) (WorkflowTemplate, error) {
	return NewWorkflowRunner(r).ForkWorkflowTemplateResource(name, sourceTemplate, title, description)
}

// WorkflowTemplateResources lists user-editable workflow template resources.
func (w *WorkflowRunner) WorkflowTemplateResources() []WorkflowTemplateResourceSummary {
	root, err := w.workflowTemplateResourceRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []WorkflowTemplateResourceSummary{{Name: "(workflow templates)", Path: root, Valid: false, Error: err.Error()}}
	}
	out := make([]WorkflowTemplateResourceSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isWorkflowTemplateResourceFile(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		path, err := w.workflowTemplateResourcePathForRead(name)
		if err != nil {
			out = append(out, WorkflowTemplateResourceSummary{Name: name, Valid: false, Error: err.Error()})
			continue
		}
		template, err := w.loadWorkflowTemplateResourcePath(path)
		if err != nil {
			out = append(out, WorkflowTemplateResourceSummary{Name: name, Path: path, Valid: false, Error: err.Error()})
			continue
		}
		out = append(out, workflowTemplateResourceSummary(template, path))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveWorkflowTemplateResource saves a user-editable workflow template resource.
func (w *WorkflowRunner) SaveWorkflowTemplateResource(name string, template WorkflowTemplate) (WorkflowTemplate, error) {
	template, err := w.ValidateWorkflowTemplateResource(name, template)
	if err != nil {
		return WorkflowTemplate{}, err
	}
	path, err := w.workflowTemplateResourcePathForWrite(template.Name)
	if err != nil {
		return WorkflowTemplate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WorkflowTemplate{}, err
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return WorkflowTemplate{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return WorkflowTemplate{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return WorkflowTemplate{}, err
	}
	template.Path = path
	return template, nil
}

// ValidateWorkflowTemplateResource validates and normalizes a user-editable workflow template resource without saving it.
func (w *WorkflowRunner) ValidateWorkflowTemplateResource(name string, template WorkflowTemplate) (WorkflowTemplate, error) {
	name = normalizePersistedWorkflowName(firstWorkflowTemplateResourceValue(name, template.Name, template.Graph.Name))
	if err := validatePersistedWorkflowName(name); err != nil {
		return WorkflowTemplate{}, err
	}
	if strings.TrimSpace(template.Name) == "" {
		template.Name = name
	}
	if err := validateWorkflowTemplateResourceVersion(template.Kind, template.Version, template.MinVersion); err != nil {
		return WorkflowTemplate{}, err
	}
	if normalizePersistedWorkflowName(template.Name) != name {
		return WorkflowTemplate{}, fmt.Errorf("workflow template name %q does not match %q", template.Name, name)
	}
	if strings.TrimSpace(template.Graph.Name) == "" {
		template.Graph.Name = name
	}
	if normalizePersistedWorkflowName(template.Graph.Name) != name {
		return WorkflowTemplate{}, fmt.Errorf("workflow template graph name %q does not match %q", template.Graph.Name, name)
	}
	if strings.TrimSpace(template.Title) == "" {
		template.Title = template.Name
	}
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	if err := w.validateWorkflowGraph(name, template.Graph.toInternalGraph()); err != nil {
		return WorkflowTemplate{}, err
	}
	template = normalizeWorkflowTemplateResourceVersion(template, false)
	template.Source = "custom"
	template.Custom = true
	template.Stages = len(template.Graph.Stages)
	return template, nil
}

// DeleteWorkflowTemplateResource deletes a user-editable workflow template resource.
func (w *WorkflowRunner) DeleteWorkflowTemplateResource(name string) error {
	path, err := w.workflowTemplateResourcePathForRead(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// CaptureWorkflowTemplateResource saves an existing workflow graph as a user-editable template resource.
func (w *WorkflowRunner) CaptureWorkflowTemplateResource(name, sourceWorkflow, title, description string) (WorkflowTemplate, error) {
	if strings.TrimSpace(sourceWorkflow) == "" {
		sourceWorkflow = name
	}
	graph, err := w.LoadWorkflowGraphDocument(sourceWorkflow)
	if err != nil {
		return WorkflowTemplate{}, err
	}
	name = normalizePersistedWorkflowName(name)
	if name == "" {
		name = normalizePersistedWorkflowName(graph.Name)
	}
	graph.Name = name
	template := WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        name,
			Title:       firstWorkflowTemplateResourceValue(title, graph.Name),
			Description: firstWorkflowTemplateResourceValue(description, graph.Description),
			Category:    "custom",
			Source:      "custom",
			Custom:      true,
		},
		Graph: graph,
	}
	return w.SaveWorkflowTemplateResource(name, template)
}

// ForkWorkflowTemplateResource saves a built-in or custom workflow template as a user-editable resource.
func (w *WorkflowRunner) ForkWorkflowTemplateResource(name, sourceTemplate, title, description string) (WorkflowTemplate, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return WorkflowTemplate{}, err
	}
	if strings.TrimSpace(sourceTemplate) == "" {
		sourceTemplate = name
	}
	template, ok := w.WorkflowTemplate(sourceTemplate)
	if !ok {
		return WorkflowTemplate{}, fmt.Errorf("workflow template not found: %s", sourceTemplate)
	}
	sourceName := template.Name
	template.Name = name
	template.Graph.Name = name
	template.Title = firstWorkflowTemplateResourceValue(title, template.Title, name)
	template.Description = firstWorkflowTemplateResourceValue(description, template.Description)
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	if template.Tags == nil {
		template.Tags = []string{"fork"}
	} else if !workflowStringListContains(template.Tags, "fork") {
		template.Tags = append(append([]string(nil), template.Tags...), "fork")
	}
	template.Source = "custom"
	template.Custom = true
	template.Path = ""
	template.MigratedFromVersion = 0
	if template.Graph.Description == "" {
		template.Graph.Description = template.Description
	}
	if normalizePersistedWorkflowName(sourceName) != name {
		if template.Graph.Description == "" {
			template.Graph.Description = fmt.Sprintf("Forked from workflow template %s.", sourceName)
		}
	}
	return w.SaveWorkflowTemplateResource(name, template)
}

func (w *WorkflowRunner) customWorkflowTemplateMap() map[string]WorkflowTemplate {
	resources := w.WorkflowTemplateResources()
	if len(resources) == 0 {
		return nil
	}
	out := make(map[string]WorkflowTemplate, len(resources))
	for _, resource := range resources {
		if !resource.Valid {
			continue
		}
		template, ok := w.customWorkflowTemplate(resource.Name)
		if ok {
			out[normalizePersistedWorkflowName(resource.Name)] = template
		}
	}
	return out
}

func (w *WorkflowRunner) customWorkflowTemplate(name string) (WorkflowTemplate, bool) {
	path, err := w.workflowTemplateResourcePathForRead(name)
	if err != nil {
		return WorkflowTemplate{}, false
	}
	template, err := w.loadWorkflowTemplateResourcePath(path)
	if err != nil {
		return WorkflowTemplate{}, false
	}
	return template, true
}

func (w *WorkflowRunner) loadWorkflowTemplateResourcePath(path string) (WorkflowTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowTemplate{}, err
	}
	var template WorkflowTemplate
	legacyBareGraph := false
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &template); err != nil {
			return WorkflowTemplate{}, fmt.Errorf("parse workflow template json: %w", err)
		}
		if len(template.Graph.Stages) == 0 {
			var graph WorkflowGraphDocument
			if err := json.Unmarshal(data, &graph); err == nil && strings.TrimSpace(graph.Name) != "" && len(graph.Stages) > 0 {
				template = workflowTemplateFromBareGraph(graph)
				legacyBareGraph = true
			}
		}
	default:
		if err := yaml.Unmarshal(data, &template); err != nil {
			return WorkflowTemplate{}, fmt.Errorf("parse workflow template yaml: %w", err)
		}
		if len(template.Graph.Stages) == 0 {
			var graph WorkflowGraphDocument
			if err := yaml.Unmarshal(data, &graph); err == nil && strings.TrimSpace(graph.Name) != "" && len(graph.Stages) > 0 {
				template = workflowTemplateFromBareGraph(graph)
				legacyBareGraph = true
			}
		}
	}
	if err := validateWorkflowTemplateResourceVersion(template.Kind, template.Version, template.MinVersion); err != nil {
		return WorkflowTemplate{}, err
	}
	template = normalizeWorkflowTemplateResourceVersion(template, legacyBareGraph || template.Version == 0)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.TrimSpace(template.Name) == "" {
		template.Name = name
	}
	if strings.TrimSpace(template.Title) == "" {
		template.Title = template.Name
	}
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	if strings.TrimSpace(template.Graph.Name) == "" {
		template.Graph.Name = template.Name
	}
	if err := w.validateWorkflowGraph(normalizePersistedWorkflowName(template.Graph.Name), template.Graph.toInternalGraph()); err != nil {
		return WorkflowTemplate{}, err
	}
	template.Source = "custom"
	template.Custom = true
	template.Path = path
	template.Stages = len(template.Graph.Stages)
	return template, nil
}

func (w *WorkflowRunner) workflowTemplateResourceRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "templates", "workflows"), nil
}

func (w *WorkflowRunner) workflowTemplateResourcePathForWrite(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.workflowTemplateResourceRoot()
	if err != nil {
		return "", err
	}
	return secureWorkflowTemplateResourcePath(root, name+".yaml")
}

func (w *WorkflowRunner) workflowTemplateResourcePathForRead(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.workflowTemplateResourceRoot()
	if err != nil {
		return "", err
	}
	for _, ext := range []string{".yaml", ".yml", ".json"} {
		path, err := secureWorkflowTemplateResourcePath(root, name+ext)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(path); statErr == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func secureWorkflowTemplateResourcePath(root, file string) (string, error) {
	path := filepath.Clean(filepath.Join(root, file))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow template path escapes runtime templates directory")
	}
	return path, nil
}

func isWorkflowTemplateResourceFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func workflowTemplateResourceSummary(template WorkflowTemplate, path string) WorkflowTemplateResourceSummary {
	return WorkflowTemplateResourceSummary{
		Kind:                template.Kind,
		Name:                template.Name,
		Title:               template.Title,
		Description:         template.Description,
		Category:            template.Category,
		Tags:                append([]string(nil), template.Tags...),
		Version:             template.Version,
		MinVersion:          template.MinVersion,
		MigratedFromVersion: template.MigratedFromVersion,
		Stages:              len(template.Graph.Stages),
		Path:                path,
		Valid:               true,
	}
}

func workflowTemplateFromBareGraph(graph WorkflowGraphDocument) WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        graph.Name,
			Title:       graph.Name,
			Description: graph.Description,
			Category:    "custom",
		},
		Graph: graph,
	}
}

func validateWorkflowTemplateResourceVersion(kind string, version, minVersion int) error {
	if strings.TrimSpace(kind) != "" && strings.TrimSpace(kind) != workflowTemplateResourceKind {
		return fmt.Errorf("unsupported workflow template resource kind %q", kind)
	}
	if version == 0 {
		version = workflowTemplateResourceMinSupportedVersion
	}
	if version < workflowTemplateResourceMinSupportedVersion {
		return fmt.Errorf("workflow template resource version %d is no longer supported", version)
	}
	if version > workflowTemplateResourceVersion {
		return fmt.Errorf("workflow template resource version %d is newer than supported version %d", version, workflowTemplateResourceVersion)
	}
	if minVersion > workflowTemplateResourceVersion {
		return fmt.Errorf("workflow template resource requires reader version %d, supported version is %d", minVersion, workflowTemplateResourceVersion)
	}
	return nil
}

func normalizeWorkflowTemplateResourceVersion(template WorkflowTemplate, migrated bool) WorkflowTemplate {
	original := template.Version
	if original == 0 {
		original = workflowTemplateResourceMinSupportedVersion
	}
	template.Kind = workflowTemplateResourceKind
	template.Version = workflowTemplateResourceVersion
	template.MinVersion = workflowTemplateResourceMinSupportedVersion
	if (migrated || original != workflowTemplateResourceVersion) && template.MigratedFromVersion == 0 {
		template.MigratedFromVersion = original
	}
	return template
}

func firstWorkflowTemplateResourceValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func workflowStringListContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}
