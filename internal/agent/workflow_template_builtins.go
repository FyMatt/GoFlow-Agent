package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

//go:embed templates/workflows/*.yaml
var embeddedWorkflowTemplateFS embed.FS

var (
	builtInWorkflowTemplateOnce  sync.Once
	builtInWorkflowTemplateCache map[string]WorkflowTemplate
	builtInWorkflowTemplateErr   error
)

func builtInWorkflowTemplates() map[string]WorkflowTemplate {
	templates, err := loadBuiltInWorkflowTemplates()
	if err != nil {
		panic(err)
	}
	return cloneWorkflowTemplateMap(templates)
}

func loadBuiltInWorkflowTemplates() (map[string]WorkflowTemplate, error) {
	builtInWorkflowTemplateOnce.Do(func() {
		entries, err := fs.ReadDir(embeddedWorkflowTemplateFS, "templates/workflows")
		if err != nil {
			builtInWorkflowTemplateErr = fmt.Errorf("read built-in workflow templates: %w", err)
			return
		}
		templates := make(map[string]WorkflowTemplate, len(entries))
		runner := &WorkflowRunner{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".yaml") {
				continue
			}
			source := "templates/workflows/" + entry.Name()
			data, err := embeddedWorkflowTemplateFS.ReadFile(source)
			if err != nil {
				builtInWorkflowTemplateErr = fmt.Errorf("read built-in workflow template %s: %w", entry.Name(), err)
				return
			}
			template, legacyBareGraph, err := parseWorkflowTemplateData(source, data)
			if err != nil {
				builtInWorkflowTemplateErr = fmt.Errorf("parse built-in workflow template %s: %w", entry.Name(), err)
				return
			}
			template, err = runner.normalizeLoadedWorkflowTemplate(template, workflowTemplateSourceBase(source), legacyBareGraph, "built_in", "", false)
			if err != nil {
				builtInWorkflowTemplateErr = fmt.Errorf("validate built-in workflow template %s: %w", entry.Name(), err)
				return
			}
			name := normalizePersistedWorkflowName(template.Name)
			if _, exists := templates[name]; exists {
				builtInWorkflowTemplateErr = fmt.Errorf("duplicate built-in workflow template %q", name)
				return
			}
			templates[name] = cloneWorkflowTemplate(template)
		}
		if len(templates) == 0 {
			builtInWorkflowTemplateErr = fmt.Errorf("built-in workflow templates are empty")
			return
		}
		builtInWorkflowTemplateCache = templates
	})
	if builtInWorkflowTemplateErr != nil {
		return nil, builtInWorkflowTemplateErr
	}
	return builtInWorkflowTemplateCache, nil
}

func cloneWorkflowTemplateMap(templates map[string]WorkflowTemplate) map[string]WorkflowTemplate {
	out := make(map[string]WorkflowTemplate, len(templates))
	for name, template := range templates {
		out[name] = cloneWorkflowTemplate(template)
	}
	return out
}

func cloneWorkflowTemplate(template WorkflowTemplate) WorkflowTemplate {
	template.Tags = append([]string(nil), template.Tags...)
	template.NodeTypes = append([]string(nil), template.NodeTypes...)
	template.Agents = append([]string(nil), template.Agents...)
	template.Skills = append([]string(nil), template.Skills...)
	template.Tools = append([]string(nil), template.Tools...)
	template.TeamTemplates = append([]string(nil), template.TeamTemplates...)
	template.PolicyRules = append([]string(nil), template.PolicyRules...)
	template.Graph.Stages = append([]WorkflowGraphStageDocument(nil), template.Graph.Stages...)
	for i := range template.Graph.Stages {
		stage := &template.Graph.Stages[i]
		stage.Params = copyStringMap(stage.Params)
		stage.Input = copyStringMap(stage.Input)
		stage.Outputs = copyStringMap(stage.Outputs)
		stage.Routes = copyStringMap(stage.Routes)
		stage.Cases = copyStringMap(stage.Cases)
		stage.OnError = append([]string(nil), stage.OnError...)
		stage.Next = append([]string(nil), stage.Next...)
		stage.Artifacts = append([]WorkflowGraphArtifactDocument(nil), stage.Artifacts...)
		for j := range stage.Artifacts {
			stage.Artifacts[j].Metadata = copyStringMap(stage.Artifacts[j].Metadata)
		}
		stage.AcceptanceCriteria = append([]WorkflowGraphAcceptanceCriterionDocument(nil), stage.AcceptanceCriteria...)
	}
	return template
}
