package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowGraphDocument is the persisted, API-facing workflow graph shape.
type WorkflowGraphDocument struct {
	Name        string                       `json:"name" yaml:"name"`
	Description string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Stages      []WorkflowGraphStageDocument `json:"stages" yaml:"stages"`
}

// WorkflowGraphStageDocument is one persisted workflow stage.
type WorkflowGraphStageDocument struct {
	Name         string                `json:"name" yaml:"name"`
	NodeType     string                `json:"node_type,omitempty" yaml:"node_type,omitempty"`
	Agent        string                `json:"agent" yaml:"agent"`
	Skill        string                `json:"skill" yaml:"skill"`
	Tool         string                `json:"tool,omitempty" yaml:"tool,omitempty"`
	Params       map[string]string     `json:"params,omitempty" yaml:"params,omitempty"`
	Approval     bool                  `json:"approval,omitempty" yaml:"approval,omitempty"`
	NextStrategy string                `json:"next_strategy,omitempty" yaml:"next_strategy,omitempty"`
	Next         []string              `json:"next,omitempty" yaml:"next,omitempty"`
	Position     WorkflowGraphPosition `json:"position,omitempty" yaml:"position,omitempty"`
}

// WorkflowGraphPosition stores visual editor node placement.
type WorkflowGraphPosition struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}

// WorkflowGraphSummary describes a built-in or persisted workflow graph.
type WorkflowGraphSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	Stages      int    `json:"stages,omitempty"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
}

// WorkflowOptionSet exposes available agents and skills for workflow editors.
type WorkflowOptionSet struct {
	Agents []WorkflowAgentOption `json:"agents"`
	Skills []WorkflowSkillOption `json:"skills"`
	Tools  []string              `json:"tools"`
}

type WorkflowAgentOption struct {
	Name string `json:"name"`
	Mode string `json:"mode,omitempty"`
}

type WorkflowSkillOption struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	PreferredAgent string   `json:"preferred_agent,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	NextSkills     []string `json:"next_skills,omitempty"`
}

func (w *WorkflowRunner) ListWorkflowGraphs() []WorkflowGraphSummary {
	summaries := []WorkflowGraphSummary{
		{Name: workflowNamePlanFixAudit, Description: "Planner -> fixer -> auditor implementation workflow.", Source: "builtin", Stages: 3, Valid: true},
		{Name: workflowNameSkillChain, Description: "Run the matched skill and declared follow-up skills.", Source: "builtin", Valid: true},
	}
	root, err := w.workflowGraphRoot()
	if err != nil {
		return summaries
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return summaries
		}
		summaries = append(summaries, WorkflowGraphSummary{Name: "(custom workflows)", Source: "custom", Path: root, Valid: false, Error: err.Error()})
		return summaries
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path, err := w.workflowGraphPath(name)
		if err != nil {
			summaries = append(summaries, WorkflowGraphSummary{Name: name, Source: "custom", Valid: false, Error: err.Error()})
			continue
		}
		doc, err := w.LoadWorkflowGraphDocument(name)
		if err != nil {
			summaries = append(summaries, WorkflowGraphSummary{Name: name, Source: "custom", Path: path, Valid: false, Error: err.Error()})
			continue
		}
		source := "custom"
		if _, builtin := w.registry[normalizePersistedWorkflowName(name)]; builtin {
			source = "custom-shadowed-by-builtin"
		}
		summaries = append(summaries, WorkflowGraphSummary{Name: doc.Name, Description: doc.Description, Source: source, Path: path, Stages: len(doc.Stages), Valid: true})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Source != summaries[j].Source {
			return summaries[i].Source < summaries[j].Source
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

func (w *WorkflowRunner) BuiltInWorkflowGraphDocument(name string) (WorkflowGraphDocument, bool) {
	switch normalizePersistedWorkflowName(name) {
	case workflowNamePlanFixAudit:
		return WorkflowGraphDocument{
			Name:        workflowNamePlanFixAudit,
			Description: "Planner creates a plan, fixer implements approved work, auditor reviews the result.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "plan", Agent: workflowAgentPlanner, Skill: "execution-plan", Next: []string{"fix"}, Position: WorkflowGraphPosition{X: 80, Y: 120}},
				{Name: "fix", Agent: workflowAgentFixer, Skill: "code-writing", Approval: true, Next: []string{"audit"}, Position: WorkflowGraphPosition{X: 360, Y: 120}},
				{Name: "audit", Agent: workflowAgentAuditor, Skill: "code-audit", Position: WorkflowGraphPosition{X: 640, Y: 120}},
			},
		}, true
	case workflowNameSkillChain:
		return WorkflowGraphDocument{
			Name:        workflowNameSkillChain,
			Description: "Match the first skill from the request, then execute declared next_skills.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "matched-skill", Agent: "preferred-agent", Skill: "matched-skill", NextStrategy: "skill-chain", Position: WorkflowGraphPosition{X: 220, Y: 120}},
			},
		}, true
	default:
		return WorkflowGraphDocument{}, false
	}
}

func (w *WorkflowRunner) LoadWorkflowGraphDocument(name string) (WorkflowGraphDocument, error) {
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return WorkflowGraphDocument{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowGraphDocument{}, err
	}
	var doc WorkflowGraphDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return WorkflowGraphDocument{}, fmt.Errorf("parse workflow graph: %w", err)
	}
	if err := validateWorkflowGraph(normalizePersistedWorkflowName(name), doc.toInternalGraph()); err != nil {
		return WorkflowGraphDocument{}, err
	}
	return doc, nil
}

func (w *WorkflowRunner) SaveWorkflowGraphDocument(name string, doc WorkflowGraphDocument) error {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return err
	}
	if _, ok := w.registry[name]; ok {
		return fmt.Errorf("cannot overwrite built-in workflow: %s", name)
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = name
	}
	if normalizeWorkflowSkillName(doc.Name) != normalizeWorkflowSkillName(name) {
		return fmt.Errorf("workflow graph name %q does not match %q", doc.Name, name)
	}
	if err := validateWorkflowGraph(name, doc.toInternalGraph()); err != nil {
		return err
	}
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *WorkflowRunner) DeleteWorkflowGraphDocument(name string) error {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return err
	}
	if _, ok := w.registry[name]; ok {
		return fmt.Errorf("cannot delete built-in workflow: %s", name)
	}
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (w *WorkflowRunner) WorkflowOptions() WorkflowOptionSet {
	options := WorkflowOptionSet{}
	if w == nil || w.runtime == nil {
		return options
	}
	for _, name := range w.runtime.AgentNames() {
		profile, ok := w.runtime.Profile(name)
		if !ok {
			continue
		}
		options.Agents = append(options.Agents, WorkflowAgentOption{Name: name, Mode: profile.Mode})
	}
	for _, skill := range w.runtime.SkillList() {
		options.Skills = append(options.Skills, WorkflowSkillOption{
			Name:           skill.Name,
			Description:    skill.Description,
			PreferredAgent: skill.PreferredAgent,
			Mode:           skill.Mode,
			NextSkills:     append([]string(nil), skill.NextSkills...),
		})
	}
	sort.Slice(options.Skills, func(i, j int) bool { return options.Skills[i].Name < options.Skills[j].Name })
	options.Tools = w.runtime.ToolNames()
	sort.Strings(options.Tools)
	return options
}

func (d WorkflowGraphDocument) toInternalGraph() workflowGraph {
	stages := make([]workflowGraphStage, 0, len(d.Stages))
	for _, stage := range d.Stages {
		stages = append(stages, workflowGraphStage{
			Name:         stage.Name,
			NodeType:     stage.NodeType,
			Agent:        stage.Agent,
			Skill:        stage.Skill,
			Tool:         stage.Tool,
			Params:       copyStringMap(stage.Params),
			Approval:     stage.Approval,
			NextStrategy: stage.NextStrategy,
			Next:         append([]string(nil), stage.Next...),
		})
	}
	return workflowGraph{Name: d.Name, Description: d.Description, Stages: stages}
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (w *WorkflowRunner) workflowGraphRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "workflows"), nil
}

func (w *WorkflowRunner) workflowGraphPath(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.workflowGraphRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, name, "workflow.yaml"))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow path escapes runtime workflows directory")
	}
	return path, nil
}

func normalizePersistedWorkflowName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func validatePersistedWorkflowName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid workflow name: %s", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("invalid workflow name %q: use lowercase letters, numbers, hyphen, or underscore", name)
		}
	}
	return nil
}
