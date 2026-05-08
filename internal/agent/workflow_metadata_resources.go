package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowMetadataResourceSummary is a persisted editor-metadata resource row.
type WorkflowMetadataResourceSummary struct {
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version,omitempty"`
	MinVersion  int    `json:"min_supported_version,omitempty"`
	Path        string `json:"path,omitempty"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
}

// WorkflowNodeMetadataResources lists custom workflow node metadata resources.
func (r *Runtime) WorkflowNodeMetadataResources() []WorkflowMetadataResourceSummary {
	return NewWorkflowRunner(r).WorkflowNodeMetadataResources()
}

// WorkflowExpressionMetadataResources lists custom expression helper metadata resources.
func (r *Runtime) WorkflowExpressionMetadataResources() []WorkflowMetadataResourceSummary {
	return NewWorkflowRunner(r).WorkflowExpressionMetadataResources()
}

// WorkflowNodeMetadataResource loads a custom workflow node metadata resource.
func (r *Runtime) WorkflowNodeMetadataResource(name string) (WorkflowNodeTypeOption, bool) {
	return NewWorkflowRunner(r).WorkflowNodeMetadataResource(name)
}

// WorkflowExpressionMetadataResource loads a custom expression helper metadata resource.
func (r *Runtime) WorkflowExpressionMetadataResource(name string) (WorkflowExpressionFunctionOption, bool) {
	return NewWorkflowRunner(r).WorkflowExpressionMetadataResource(name)
}

// SaveWorkflowNodeMetadataResource saves a custom workflow node metadata resource.
func (r *Runtime) SaveWorkflowNodeMetadataResource(name string, option WorkflowNodeTypeOption) (WorkflowNodeTypeOption, error) {
	return NewWorkflowRunner(r).SaveWorkflowNodeMetadataResource(name, option)
}

// ValidateWorkflowNodeMetadataResource validates and normalizes a custom workflow node metadata resource without saving it.
func (r *Runtime) ValidateWorkflowNodeMetadataResource(name string, option WorkflowNodeTypeOption) (WorkflowNodeTypeOption, error) {
	return NewWorkflowRunner(r).ValidateWorkflowNodeMetadataResource(name, option)
}

// SaveWorkflowExpressionMetadataResource saves a custom expression helper metadata resource.
func (r *Runtime) SaveWorkflowExpressionMetadataResource(name string, option WorkflowExpressionFunctionOption) (WorkflowExpressionFunctionOption, error) {
	return NewWorkflowRunner(r).SaveWorkflowExpressionMetadataResource(name, option)
}

// ValidateWorkflowExpressionMetadataResource validates and normalizes a custom expression helper metadata resource without saving it.
func (r *Runtime) ValidateWorkflowExpressionMetadataResource(name string, option WorkflowExpressionFunctionOption) (WorkflowExpressionFunctionOption, error) {
	return NewWorkflowRunner(r).ValidateWorkflowExpressionMetadataResource(name, option)
}

// DeleteWorkflowNodeMetadataResource deletes a custom workflow node metadata resource.
func (r *Runtime) DeleteWorkflowNodeMetadataResource(name string) error {
	return NewWorkflowRunner(r).DeleteWorkflowNodeMetadataResource(name)
}

// DeleteWorkflowExpressionMetadataResource deletes a custom expression helper metadata resource.
func (r *Runtime) DeleteWorkflowExpressionMetadataResource(name string) error {
	return NewWorkflowRunner(r).DeleteWorkflowExpressionMetadataResource(name)
}

// WorkflowNodeMetadataResources lists custom workflow node metadata resources.
func (w *WorkflowRunner) WorkflowNodeMetadataResources() []WorkflowMetadataResourceSummary {
	return workflowMetadataResources(w.workflowNodeMetadataRoots(), loadWorkflowNodeMetadata, workflowNodeMetadataSummary)
}

// WorkflowExpressionMetadataResources lists custom expression helper metadata resources.
func (w *WorkflowRunner) WorkflowExpressionMetadataResources() []WorkflowMetadataResourceSummary {
	return workflowMetadataResources(w.workflowExpressionFunctionMetadataRoots(), loadWorkflowExpressionFunctionMetadata, workflowExpressionMetadataSummary)
}

func workflowMetadataResources[T any](roots []string, load func(string) (T, error), summarize func(T, string) WorkflowMetadataResourceSummary) []WorkflowMetadataResourceSummary {
	if len(roots) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]WorkflowMetadataResourceSummary, 0)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out = append(out, WorkflowMetadataResourceSummary{Name: filepath.Base(root), Path: root, Valid: false, Error: err.Error()})
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !isWorkflowMetadataResourceFile(entry.Name()) {
				continue
			}
			name := normalizePersistedWorkflowName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			path := filepath.Join(root, entry.Name())
			resource, err := load(path)
			if err != nil {
				out = append(out, WorkflowMetadataResourceSummary{Name: name, Path: path, Valid: false, Error: err.Error()})
				continue
			}
			out = append(out, summarize(resource, path))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WorkflowNodeMetadataResource loads a custom workflow node metadata resource.
func (w *WorkflowRunner) WorkflowNodeMetadataResource(name string) (WorkflowNodeTypeOption, bool) {
	path, err := w.workflowNodeMetadataResourcePathForRead(name)
	if err != nil {
		return WorkflowNodeTypeOption{}, false
	}
	option, err := loadWorkflowNodeMetadata(path)
	if err != nil {
		return WorkflowNodeTypeOption{}, false
	}
	option.Path = path
	option.Custom = true
	return option, true
}

// WorkflowExpressionMetadataResource loads a custom expression helper metadata resource.
func (w *WorkflowRunner) WorkflowExpressionMetadataResource(name string) (WorkflowExpressionFunctionOption, bool) {
	path, err := w.workflowExpressionMetadataResourcePathForRead(name)
	if err != nil {
		return WorkflowExpressionFunctionOption{}, false
	}
	option, err := loadWorkflowExpressionFunctionMetadata(path)
	if err != nil {
		return WorkflowExpressionFunctionOption{}, false
	}
	option.Path = path
	option.Custom = true
	return option, true
}

// SaveWorkflowNodeMetadataResource saves a custom workflow node metadata resource.
func (w *WorkflowRunner) SaveWorkflowNodeMetadataResource(name string, option WorkflowNodeTypeOption) (WorkflowNodeTypeOption, error) {
	option, err := w.ValidateWorkflowNodeMetadataResource(name, option)
	if err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	path, err := w.workflowNodeMetadataResourcePathForWrite(option.Type)
	if err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	if err := writeWorkflowMetadataResource(path, option); err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	option.Source = "custom_metadata"
	option.Path = path
	option.Custom = true
	return option, nil
}

// ValidateWorkflowNodeMetadataResource validates and normalizes a custom workflow node metadata resource without saving it.
func (w *WorkflowRunner) ValidateWorkflowNodeMetadataResource(name string, option WorkflowNodeTypeOption) (WorkflowNodeTypeOption, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	if strings.TrimSpace(option.Type) == "" {
		option.Type = name
	}
	if normalizePersistedWorkflowName(option.Type) != name {
		return WorkflowNodeTypeOption{}, fmt.Errorf("workflow node metadata type %q does not match %q", option.Type, name)
	}
	if !w.workflowNodeTypeExists(name) {
		return WorkflowNodeTypeOption{}, fmt.Errorf("workflow node metadata references unknown node type: %s", name)
	}
	option.Type = name
	option.Kind = workflowNodeMetadataResourceKind
	option.Version = workflowNodeMetadataResourceVersion
	option.MinVersion = workflowNodeMetadataResourceMinSupportedVersion
	if err := validateWorkflowNodeMetadata(option); err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	return option, nil
}

// SaveWorkflowExpressionMetadataResource saves a custom expression helper metadata resource.
func (w *WorkflowRunner) SaveWorkflowExpressionMetadataResource(name string, option WorkflowExpressionFunctionOption) (WorkflowExpressionFunctionOption, error) {
	option, err := w.ValidateWorkflowExpressionMetadataResource(name, option)
	if err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	path, err := w.workflowExpressionMetadataResourcePathForWrite(option.Name)
	if err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	if err := writeWorkflowMetadataResource(path, option); err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	option.Source = "custom_metadata"
	option.Path = path
	option.Custom = true
	return option, nil
}

// ValidateWorkflowExpressionMetadataResource validates and normalizes a custom expression helper metadata resource without saving it.
func (w *WorkflowRunner) ValidateWorkflowExpressionMetadataResource(name string, option WorkflowExpressionFunctionOption) (WorkflowExpressionFunctionOption, error) {
	name = normalizeWorkflowSkillName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	if strings.TrimSpace(option.Name) == "" {
		option.Name = name
	}
	if normalizeWorkflowSkillName(option.Name) != name {
		return WorkflowExpressionFunctionOption{}, fmt.Errorf("workflow expression metadata name %q does not match %q", option.Name, name)
	}
	if _, ok := workflowExpressionFunctionDefinition(name); !ok {
		return WorkflowExpressionFunctionOption{}, fmt.Errorf("workflow expression metadata references unknown helper: %s", name)
	}
	option.Name = name
	option.Kind = workflowExpressionMetadataResourceKind
	option.Version = workflowExpressionMetadataResourceVersion
	option.MinVersion = workflowExpressionMetadataResourceMinSupportedVersion
	if err := validateWorkflowExpressionFunctionMetadata(option); err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	return option, nil
}

// DeleteWorkflowNodeMetadataResource deletes a custom workflow node metadata resource.
func (w *WorkflowRunner) DeleteWorkflowNodeMetadataResource(name string) error {
	path, err := w.workflowNodeMetadataResourcePathForRead(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// DeleteWorkflowExpressionMetadataResource deletes a custom expression helper metadata resource.
func (w *WorkflowRunner) DeleteWorkflowExpressionMetadataResource(name string) error {
	path, err := w.workflowExpressionMetadataResourcePathForRead(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (w *WorkflowRunner) workflowNodeTypeExists(name string) bool {
	name = normalizeWorkflowSkillName(name)
	for _, option := range (&WorkflowRunner{}).WorkflowNodeTypes() {
		if normalizeWorkflowSkillName(option.Type) == name {
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) workflowNodeMetadataResourcePathForWrite(name string) (string, error) {
	root, err := w.workflowPreferredMetadataRoot("workflow_nodes")
	if err != nil {
		return "", err
	}
	return secureWorkflowMetadataResourcePath(root, normalizePersistedWorkflowName(name)+".yaml")
}

func (w *WorkflowRunner) workflowExpressionMetadataResourcePathForWrite(name string) (string, error) {
	root, err := w.workflowPreferredMetadataRoot("expression_helpers")
	if err != nil {
		return "", err
	}
	return secureWorkflowMetadataResourcePath(root, normalizePersistedWorkflowName(name)+".yaml")
}

func (w *WorkflowRunner) workflowNodeMetadataResourcePathForRead(name string) (string, error) {
	return workflowMetadataResourcePathForRead(w.workflowNodeMetadataRoots(), name)
}

func (w *WorkflowRunner) workflowExpressionMetadataResourcePathForRead(name string) (string, error) {
	return workflowMetadataResourcePathForRead(w.workflowExpressionFunctionMetadataRoots(), name)
}

func (w *WorkflowRunner) workflowPreferredMetadataRoot(kind string) (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "metadata", kind), nil
}

func workflowMetadataResourcePathForRead(roots []string, name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	for _, root := range roots {
		for _, ext := range []string{".yaml", ".yml"} {
			path, err := secureWorkflowMetadataResourcePath(root, name+ext)
			if err != nil {
				return "", err
			}
			if _, statErr := os.Stat(path); statErr == nil {
				return path, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func secureWorkflowMetadataResourcePath(root, file string) (string, error) {
	path := filepath.Clean(filepath.Join(root, file))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow metadata path escapes runtime metadata directory")
	}
	return path, nil
}

func writeWorkflowMetadataResource(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func isWorkflowMetadataResourceFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func workflowNodeMetadataSummary(option WorkflowNodeTypeOption, path string) WorkflowMetadataResourceSummary {
	return WorkflowMetadataResourceSummary{
		Kind:        option.Kind,
		Name:        option.Type,
		Label:       option.Label,
		Category:    option.Category,
		Description: option.Description,
		Version:     option.Version,
		MinVersion:  option.MinVersion,
		Path:        path,
		Valid:       true,
	}
}

func workflowExpressionMetadataSummary(option WorkflowExpressionFunctionOption, path string) WorkflowMetadataResourceSummary {
	return WorkflowMetadataResourceSummary{
		Kind:        option.Kind,
		Name:        option.Name,
		Label:       option.Label,
		Category:    option.Category,
		Description: option.Description,
		Version:     option.Version,
		MinVersion:  option.MinVersion,
		Path:        path,
		Valid:       true,
	}
}
