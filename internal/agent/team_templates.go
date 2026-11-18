package agent

import (
	"sort"
)

// TeamTemplateSummary is a compact reusable multi-agent team row.
type TeamTemplateSummary struct {
	Kind                  string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version               int      `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion            int      `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	MigratedFromVersion   int      `json:"migrated_from_version,omitempty" yaml:"migrated_from_version,omitempty"`
	Name                  string   `json:"name" yaml:"name"`
	Title                 string   `json:"title" yaml:"title"`
	Description           string   `json:"description,omitempty" yaml:"description,omitempty"`
	Category              string   `json:"category,omitempty" yaml:"category,omitempty"`
	Tags                  []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Roles                 int      `json:"roles" yaml:"roles"`
	QuorumPresetCount     int      `json:"quorum_presets,omitempty" yaml:"quorum_preset_count,omitempty"`
	RecommendedWorkflow   string   `json:"recommended_workflow,omitempty" yaml:"recommended_workflow,omitempty"`
	RecommendedEntryAgent string   `json:"recommended_entry_agent,omitempty" yaml:"recommended_entry_agent,omitempty"`
	Source                string   `json:"source,omitempty" yaml:"source,omitempty"`
	Path                  string   `json:"path,omitempty" yaml:"path,omitempty"`
	Custom                bool     `json:"custom,omitempty" yaml:"custom,omitempty"`
}

// TeamTemplate describes a reusable collaboration pattern for Studio and future team execution.
type TeamTemplate struct {
	TeamTemplateSummary `yaml:",inline"`
	RoleTemplates       []TeamRoleTemplate       `json:"role_templates,omitempty" yaml:"role_templates,omitempty"`
	Handoffs            []TeamHandoffTemplate    `json:"handoffs,omitempty" yaml:"handoffs,omitempty"`
	BlackboardTemplates []TeamBlackboardTemplate `json:"blackboard_templates,omitempty" yaml:"blackboard_templates,omitempty"`
	QuorumPresets       []TeamQuorumPreset       `json:"quorum_presets,omitempty" yaml:"quorum_presets,omitempty"`
	OutputContract      []string                 `json:"output_contract,omitempty" yaml:"output_contract,omitempty"`
}

// TeamRoleTemplate describes one role in a reusable team.
type TeamRoleTemplate struct {
	Name             string   `json:"name" yaml:"name"`
	Label            string   `json:"label,omitempty" yaml:"label,omitempty"`
	Agent            string   `json:"agent" yaml:"agent"`
	Skill            string   `json:"skill,omitempty" yaml:"skill,omitempty"`
	Responsibilities []string `json:"responsibilities,omitempty" yaml:"responsibilities,omitempty"`
	Consumes         []string `json:"consumes,omitempty" yaml:"consumes,omitempty"`
	Produces         []string `json:"produces,omitempty" yaml:"produces,omitempty"`
	Tools            []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Notes            string   `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// TeamHandoffTemplate describes an expected handoff between roles.
type TeamHandoffTemplate struct {
	From        string   `json:"from" yaml:"from"`
	To          string   `json:"to" yaml:"to"`
	Kind        string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Subject     string   `json:"subject,omitempty" yaml:"subject,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Blackboard  []string `json:"blackboard,omitempty" yaml:"blackboard,omitempty"`
	Condition   string   `json:"condition,omitempty" yaml:"condition,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

// TeamBlackboardTemplate describes shared-state slots a team should maintain.
type TeamBlackboardTemplate struct {
	Kind        string   `json:"kind" yaml:"kind"`
	Title       string   `json:"title,omitempty" yaml:"title,omitempty"`
	OwnerRole   string   `json:"owner_role,omitempty" yaml:"owner_role,omitempty"`
	Status      string   `json:"status,omitempty" yaml:"status,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

// TeamQuorumPreset describes a reusable team approval gate policy.
type TeamQuorumPreset struct {
	Name         string   `json:"name" yaml:"name"`
	Title        string   `json:"title,omitempty" yaml:"title,omitempty"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	Required     int      `json:"required,omitempty" yaml:"required,omitempty"`
	Roles        []string `json:"roles,omitempty" yaml:"roles,omitempty"`
	RejectBlocks bool     `json:"reject_blocks,omitempty" yaml:"reject_blocks,omitempty"`
	Default      bool     `json:"default,omitempty" yaml:"default,omitempty"`
}

// TeamTemplates returns built-in reusable multi-agent team summaries.
func TeamTemplates() []TeamTemplateSummary {
	return (&WorkflowRunner{}).TeamTemplates()
}

// LoadTeamTemplate loads one built-in team template by name.
func LoadTeamTemplate(name string) (TeamTemplate, bool) {
	return (&WorkflowRunner{}).TeamTemplate(name)
}

// TeamTemplates returns built-in and runtime custom reusable multi-agent team summaries.
func (w *WorkflowRunner) TeamTemplates() []TeamTemplateSummary {
	templates := builtInTeamTemplates()
	if custom := w.customTeamTemplateMap(); len(custom) > 0 {
		for name, template := range custom {
			templates[name] = template
		}
	}
	out := make([]TeamTemplateSummary, 0, len(templates))
	for _, template := range templates {
		summary := template.TeamTemplateSummary
		summary.Roles = len(template.RoleTemplates)
		summary.QuorumPresetCount = len(template.QuorumPresets)
		if summary.Source == "" {
			summary.Source = "built_in"
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TeamTemplate loads one built-in or runtime custom team template by name.
func (w *WorkflowRunner) TeamTemplate(name string) (TeamTemplate, bool) {
	if template, ok := w.customTeamTemplate(name); ok {
		template.TeamTemplateSummary.Roles = len(template.RoleTemplates)
		return template, true
	}
	template, ok := builtInTeamTemplates()[normalizePersistedWorkflowName(name)]
	if !ok {
		return TeamTemplate{}, false
	}
	template.TeamTemplateSummary.Roles = len(template.RoleTemplates)
	template.TeamTemplateSummary.QuorumPresetCount = len(template.QuorumPresets)
	if template.TeamTemplateSummary.Source == "" {
		template.TeamTemplateSummary.Source = "built_in"
	}
	return template, true
}

func builtInTeamTemplates() map[string]TeamTemplate {
	templates, err := loadBuiltInTeamTemplates()
	if err != nil {
		panic(err)
	}
	return cloneTeamTemplateMap(templates)
}
