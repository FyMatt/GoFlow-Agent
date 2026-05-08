package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

const (
	workflowSchemaResourceKind                = "goflow.workflow_schema_resource"
	workflowSchemaResourceVersion             = 2
	workflowSchemaResourceMinSupportedVersion = 1
)

// WorkflowSchemaResourceSummary is a persisted schema resource row for Studio catalogs.
type WorkflowSchemaResourceSummary struct {
	Name                string `json:"name"`
	Workflow            string `json:"workflow,omitempty"`
	Description         string `json:"description,omitempty"`
	Version             int    `json:"version,omitempty"`
	MigratedFromVersion int    `json:"migrated_from_version,omitempty"`
	Path                string `json:"path,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	Stages              int    `json:"stages,omitempty"`
	Outputs             int    `json:"outputs,omitempty"`
	Valid               bool   `json:"valid"`
	Error               string `json:"error,omitempty"`
}

// WorkflowSchemaResource stores one reusable workflow schema bundle on disk.
type WorkflowSchemaResource struct {
	Kind                string                         `json:"kind,omitempty"`
	Version             int                            `json:"version"`
	MinVersion          int                            `json:"min_supported_version,omitempty"`
	MigratedFromVersion int                            `json:"migrated_from_version,omitempty"`
	Name                string                         `json:"name,omitempty"`
	Description         string                         `json:"description,omitempty"`
	Schema              session.WorkflowSchemaSnapshot `json:"schema"`
	Path                string                         `json:"path,omitempty"`
}

// WorkflowSchemaResources lists reusable workflow schema resources.
func (r *Runtime) WorkflowSchemaResources() []WorkflowSchemaResourceSummary {
	return NewWorkflowRunner(r).WorkflowSchemaResources()
}

// WorkflowSchemaResource loads one reusable workflow schema resource.
func (r *Runtime) WorkflowSchemaResource(name string) (WorkflowSchemaResource, error) {
	return NewWorkflowRunner(r).WorkflowSchemaResource(name)
}

// ValidateWorkflowSchemaResource validates and normalizes one reusable workflow schema resource without saving or activating it.
func (r *Runtime) ValidateWorkflowSchemaResource(name string, resource WorkflowSchemaResource) (WorkflowSchemaResource, error) {
	return NewWorkflowRunner(r).ValidateWorkflowSchemaResource(name, resource)
}

// SaveWorkflowSchemaResource saves one reusable workflow schema resource and imports it into the active catalog.
func (r *Runtime) SaveWorkflowSchemaResource(name string, resource WorkflowSchemaResource) (WorkflowSchemaResource, error) {
	return NewWorkflowRunner(r).SaveWorkflowSchemaResource(name, resource)
}

// DeleteWorkflowSchemaResource deletes one reusable workflow schema resource.
func (r *Runtime) DeleteWorkflowSchemaResource(name string) error {
	return NewWorkflowRunner(r).DeleteWorkflowSchemaResource(name)
}

// ActivateWorkflowSchemaResource imports one saved schema resource into the active catalog.
func (r *Runtime) ActivateWorkflowSchemaResource(name string, merge bool) (session.WorkflowSchemaSnapshot, error) {
	return NewWorkflowRunner(r).ActivateWorkflowSchemaResource(name, merge)
}

// CaptureWorkflowSchemaResource saves the active session schema for a workflow as a reusable resource.
func (r *Runtime) CaptureWorkflowSchemaResource(name, description string) (WorkflowSchemaResource, error) {
	return NewWorkflowRunner(r).CaptureWorkflowSchemaResource(name, description)
}

// WorkflowSchemaResources lists reusable workflow schema resources.
func (w *WorkflowRunner) WorkflowSchemaResources() []WorkflowSchemaResourceSummary {
	root, err := w.workflowSchemaResourceRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []WorkflowSchemaResourceSummary{{Name: "(workflow schemas)", Path: root, Valid: false, Error: err.Error()}}
	}
	summaries := make([]WorkflowSchemaResourceSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		path, err := w.workflowSchemaResourcePath(name)
		if err != nil {
			summaries = append(summaries, WorkflowSchemaResourceSummary{Name: name, Valid: false, Error: err.Error()})
			continue
		}
		resource, err := w.loadWorkflowSchemaResourcePath(path)
		if err != nil {
			summaries = append(summaries, WorkflowSchemaResourceSummary{Name: name, Path: path, Valid: false, Error: err.Error()})
			continue
		}
		summaries = append(summaries, workflowSchemaResourceSummary(resource, path))
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	return summaries
}

// WorkflowSchemaResource loads one reusable workflow schema resource.
func (w *WorkflowRunner) WorkflowSchemaResource(name string) (WorkflowSchemaResource, error) {
	path, err := w.workflowSchemaResourcePath(name)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	return w.loadWorkflowSchemaResourcePath(path)
}

// SaveWorkflowSchemaResource saves one reusable workflow schema resource and imports it into the active catalog.
func (w *WorkflowRunner) SaveWorkflowSchemaResource(name string, resource WorkflowSchemaResource) (WorkflowSchemaResource, error) {
	resource, err := w.ValidateWorkflowSchemaResource(name, resource)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	if w == nil || w.runtime == nil {
		return WorkflowSchemaResource{}, fmt.Errorf("workflow runtime not configured")
	}
	normalized, err := w.runtime.ImportWorkflowSchema(resource.Schema, false)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	resource.Schema = normalized
	path, err := w.workflowSchemaResourcePath(resource.Name)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WorkflowSchemaResource{}, err
	}
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return WorkflowSchemaResource{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return WorkflowSchemaResource{}, err
	}
	resource.Path = path
	return resource, nil
}

// ValidateWorkflowSchemaResource validates and normalizes one reusable workflow schema resource without saving or activating it.
func (w *WorkflowRunner) ValidateWorkflowSchemaResource(name string, resource WorkflowSchemaResource) (WorkflowSchemaResource, error) {
	name = normalizePersistedWorkflowName(firstWorkflowSchemaResourceValue(name, resource.Name, resource.Schema.Workflow))
	if err := validatePersistedWorkflowName(name); err != nil {
		return WorkflowSchemaResource{}, err
	}
	if strings.TrimSpace(resource.Name) == "" {
		resource.Name = name
	}
	if err := validateWorkflowSchemaResourceVersion(resource.Kind, resource.Version, resource.MinVersion); err != nil {
		return WorkflowSchemaResource{}, err
	}
	if normalizePersistedWorkflowName(resource.Name) != name {
		return WorkflowSchemaResource{}, fmt.Errorf("workflow schema resource name %q does not match %q", resource.Name, name)
	}
	if strings.TrimSpace(resource.Schema.Workflow) == "" {
		resource.Schema.Workflow = name
	}
	if normalizePersistedWorkflowName(resource.Schema.Workflow) != name {
		return WorkflowSchemaResource{}, fmt.Errorf("workflow schema workflow %q does not match %q", resource.Schema.Workflow, name)
	}
	validator := session.New(1)
	normalized, err := validator.ImportWorkflowSchema(resource.Schema, false)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	resource = migrateWorkflowSchemaResource(resource)
	resource.Name = name
	resource.Schema = normalized
	return resource, nil
}

// DeleteWorkflowSchemaResource deletes one reusable workflow schema resource.
func (w *WorkflowRunner) DeleteWorkflowSchemaResource(name string) error {
	path, err := w.workflowSchemaResourcePath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// ActivateWorkflowSchemaResource imports one saved schema resource into the active catalog.
func (w *WorkflowRunner) ActivateWorkflowSchemaResource(name string, merge bool) (session.WorkflowSchemaSnapshot, error) {
	resource, err := w.WorkflowSchemaResource(name)
	if err != nil {
		return session.WorkflowSchemaSnapshot{}, err
	}
	if w == nil || w.runtime == nil {
		return session.WorkflowSchemaSnapshot{}, fmt.Errorf("workflow runtime not configured")
	}
	return w.runtime.ImportWorkflowSchema(resource.Schema, merge)
}

// CaptureWorkflowSchemaResource saves the active session schema for a workflow as a reusable resource.
func (w *WorkflowRunner) CaptureWorkflowSchemaResource(name, description string) (WorkflowSchemaResource, error) {
	if w == nil || w.runtime == nil {
		return WorkflowSchemaResource{}, fmt.Errorf("workflow runtime not configured")
	}
	schema, ok := w.runtime.WorkflowSchema(name)
	if !ok {
		return WorkflowSchemaResource{}, fmt.Errorf("workflow schema not found: %s", name)
	}
	return w.SaveWorkflowSchemaResource(name, WorkflowSchemaResource{
		Version:     workflowSchemaResourceVersion,
		Name:        normalizePersistedWorkflowName(name),
		Description: strings.TrimSpace(description),
		Schema:      schema,
	})
}

func (w *WorkflowRunner) loadWorkflowSchemaResourcePath(path string) (WorkflowSchemaResource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowSchemaResource{}, err
	}
	var resource WorkflowSchemaResource
	if err := json.Unmarshal(data, &resource); err != nil {
		return WorkflowSchemaResource{}, fmt.Errorf("parse workflow schema resource: %w", err)
	}
	if strings.TrimSpace(resource.Schema.Workflow) == "" {
		var legacy session.WorkflowSchemaSnapshot
		if err := json.Unmarshal(data, &legacy); err == nil && strings.TrimSpace(legacy.Workflow) != "" {
			resource = WorkflowSchemaResource{
				Version: workflowSchemaResourceMinSupportedVersion,
				Name:    legacy.Workflow,
				Schema:  legacy,
			}
		}
	}
	if err := validateWorkflowSchemaResourceVersion(resource.Kind, resource.Version, resource.MinVersion); err != nil {
		return WorkflowSchemaResource{}, err
	}
	resource = migrateWorkflowSchemaResource(resource)
	if strings.TrimSpace(resource.Name) == "" {
		resource.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if strings.TrimSpace(resource.Schema.Workflow) == "" {
		resource.Schema.Workflow = resource.Name
	}
	resource.Path = path
	return resource, nil
}

func (w *WorkflowRunner) workflowSchemaResourceRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "schemas", "workflows"), nil
}

func (w *WorkflowRunner) workflowSchemaResourcePath(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.workflowSchemaResourceRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, name+".json"))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow schema path escapes runtime schemas directory")
	}
	return path, nil
}

func workflowSchemaResourceSummary(resource WorkflowSchemaResource, path string) WorkflowSchemaResourceSummary {
	summary := WorkflowSchemaResourceSummary{
		Name:                firstWorkflowSchemaResourceValue(resource.Name, resource.Schema.Workflow),
		Workflow:            resource.Schema.Workflow,
		Description:         resource.Description,
		Version:             resource.Version,
		MigratedFromVersion: resource.MigratedFromVersion,
		Path:                path,
		UpdatedAt:           resource.Schema.UpdatedAt,
		Stages:              len(resource.Schema.Stages),
		Valid:               true,
	}
	for _, stage := range resource.Schema.Stages {
		summary.Outputs += len(stage.Outputs)
	}
	if strings.TrimSpace(summary.Name) == "" {
		summary.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return summary
}

func validateWorkflowSchemaResourceVersion(kind string, version, minVersion int) error {
	if strings.TrimSpace(kind) != "" && strings.TrimSpace(kind) != workflowSchemaResourceKind {
		return fmt.Errorf("unsupported workflow schema resource kind %q", kind)
	}
	if version == 0 {
		version = workflowSchemaResourceMinSupportedVersion
	}
	if version < workflowSchemaResourceMinSupportedVersion {
		return fmt.Errorf("workflow schema resource version %d is no longer supported", version)
	}
	if version > workflowSchemaResourceVersion {
		return fmt.Errorf("workflow schema resource version %d is newer than supported version %d", version, workflowSchemaResourceVersion)
	}
	if minVersion > workflowSchemaResourceVersion {
		return fmt.Errorf("workflow schema resource requires reader version %d, supported version is %d", minVersion, workflowSchemaResourceVersion)
	}
	return nil
}

func migrateWorkflowSchemaResource(resource WorkflowSchemaResource) WorkflowSchemaResource {
	original := resource.Version
	if original == 0 {
		original = workflowSchemaResourceMinSupportedVersion
	}
	resource.Kind = workflowSchemaResourceKind
	resource.Version = workflowSchemaResourceVersion
	resource.MinVersion = workflowSchemaResourceMinSupportedVersion
	if original != workflowSchemaResourceVersion && resource.MigratedFromVersion == 0 {
		resource.MigratedFromVersion = original
	}
	return resource
}

func firstWorkflowSchemaResourceValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
