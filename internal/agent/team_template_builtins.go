package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

//go:embed templates/teams/*.yaml
var embeddedTeamTemplateFS embed.FS

var (
	builtInTeamTemplateOnce  sync.Once
	builtInTeamTemplateCache map[string]TeamTemplate
	builtInTeamTemplateErr   error
)

func loadBuiltInTeamTemplates() (map[string]TeamTemplate, error) {
	builtInTeamTemplateOnce.Do(func() {
		entries, err := fs.ReadDir(embeddedTeamTemplateFS, "templates/teams")
		if err != nil {
			builtInTeamTemplateErr = fmt.Errorf("read built-in team templates: %w", err)
			return
		}
		templates := make(map[string]TeamTemplate, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".yaml") {
				continue
			}
			source := "templates/teams/" + entry.Name()
			data, err := embeddedTeamTemplateFS.ReadFile(source)
			if err != nil {
				builtInTeamTemplateErr = fmt.Errorf("read built-in team template %s: %w", entry.Name(), err)
				return
			}
			template, err := parseTeamTemplateData(source, data)
			if err != nil {
				builtInTeamTemplateErr = fmt.Errorf("parse built-in team template %s: %w", entry.Name(), err)
				return
			}
			name := normalizePersistedWorkflowName(template.Name)
			if _, exists := templates[name]; exists {
				builtInTeamTemplateErr = fmt.Errorf("duplicate built-in team template %q", name)
				return
			}
			template.Name = name
			template.Source = "built_in"
			template.Custom = false
			template.Path = ""
			template.Roles = len(template.RoleTemplates)
			template.QuorumPresetCount = len(template.QuorumPresets)
			templates[name] = cloneTeamTemplate(template)
		}
		if len(templates) == 0 {
			builtInTeamTemplateErr = fmt.Errorf("built-in team templates are empty")
			return
		}
		builtInTeamTemplateCache = templates
	})
	if builtInTeamTemplateErr != nil {
		return nil, builtInTeamTemplateErr
	}
	return builtInTeamTemplateCache, nil
}

func cloneTeamTemplateMap(templates map[string]TeamTemplate) map[string]TeamTemplate {
	out := make(map[string]TeamTemplate, len(templates))
	for name, template := range templates {
		out[name] = cloneTeamTemplate(template)
	}
	return out
}

func cloneTeamTemplate(template TeamTemplate) TeamTemplate {
	template.Tags = append([]string(nil), template.Tags...)
	template.RoleTemplates = append([]TeamRoleTemplate(nil), template.RoleTemplates...)
	for i := range template.RoleTemplates {
		template.RoleTemplates[i].Responsibilities = append([]string(nil), template.RoleTemplates[i].Responsibilities...)
		template.RoleTemplates[i].Consumes = append([]string(nil), template.RoleTemplates[i].Consumes...)
		template.RoleTemplates[i].Produces = append([]string(nil), template.RoleTemplates[i].Produces...)
		template.RoleTemplates[i].Tools = append([]string(nil), template.RoleTemplates[i].Tools...)
	}
	template.Handoffs = append([]TeamHandoffTemplate(nil), template.Handoffs...)
	for i := range template.Handoffs {
		template.Handoffs[i].Artifacts = append([]string(nil), template.Handoffs[i].Artifacts...)
		template.Handoffs[i].Blackboard = append([]string(nil), template.Handoffs[i].Blackboard...)
	}
	template.BlackboardTemplates = append([]TeamBlackboardTemplate(nil), template.BlackboardTemplates...)
	for i := range template.BlackboardTemplates {
		template.BlackboardTemplates[i].Tags = append([]string(nil), template.BlackboardTemplates[i].Tags...)
	}
	template.QuorumPresets = append([]TeamQuorumPreset(nil), template.QuorumPresets...)
	for i := range template.QuorumPresets {
		template.QuorumPresets[i].Roles = append([]string(nil), template.QuorumPresets[i].Roles...)
	}
	template.OutputContract = append([]string(nil), template.OutputContract...)
	return template
}
