package agent

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/workflow_nodes/*.yaml
var embeddedWorkflowNodeTypeFS embed.FS

type workflowNodeTypeFile struct {
	Nodes []WorkflowNodeTypeOption `yaml:"nodes"`
}

var (
	builtInWorkflowNodeTypeOnce  sync.Once
	builtInWorkflowNodeTypeCache []WorkflowNodeTypeOption
	builtInWorkflowNodeTypeErr   error
)

func builtInWorkflowNodeTypes() []WorkflowNodeTypeOption {
	options, err := loadBuiltInWorkflowNodeTypes()
	if err != nil {
		panic(err)
	}
	return cloneWorkflowNodeTypeOptions(options)
}

func loadBuiltInWorkflowNodeTypes() ([]WorkflowNodeTypeOption, error) {
	builtInWorkflowNodeTypeOnce.Do(func() {
		entries, err := embeddedWorkflowNodeTypeFS.ReadDir("templates/workflow_nodes")
		if err != nil {
			builtInWorkflowNodeTypeErr = fmt.Errorf("read built-in workflow node types: %w", err)
			return
		}
		seen := make(map[string]struct{})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
				continue
			}
			data, err := embeddedWorkflowNodeTypeFS.ReadFile("templates/workflow_nodes/" + name)
			if err != nil {
				builtInWorkflowNodeTypeErr = fmt.Errorf("read built-in workflow node type %s: %w", name, err)
				return
			}
			options, err := parseWorkflowNodeTypeOptionData(data)
			if err != nil {
				builtInWorkflowNodeTypeErr = fmt.Errorf("parse built-in workflow node type %s: %w", name, err)
				return
			}
			for _, option := range options {
				option.Type = normalizeWorkflowSkillName(option.Type)
				if option.Source == "" {
					option.Source = "built_in"
				}
				option.Path = ""
				option.Custom = false
				if err := validateBuiltInWorkflowNodeTypeOption(option); err != nil {
					builtInWorkflowNodeTypeErr = fmt.Errorf("validate built-in workflow node type %s: %w", name, err)
					return
				}
				if _, exists := seen[option.Type]; exists {
					builtInWorkflowNodeTypeErr = fmt.Errorf("duplicate built-in workflow node type %q", option.Type)
					return
				}
				seen[option.Type] = struct{}{}
				builtInWorkflowNodeTypeCache = append(builtInWorkflowNodeTypeCache, option)
			}
		}
		if len(builtInWorkflowNodeTypeCache) == 0 {
			builtInWorkflowNodeTypeErr = fmt.Errorf("built-in workflow node types are empty")
			return
		}
	})
	if builtInWorkflowNodeTypeErr != nil {
		return nil, builtInWorkflowNodeTypeErr
	}
	return builtInWorkflowNodeTypeCache, nil
}

func parseWorkflowNodeTypeOptionData(data []byte) ([]WorkflowNodeTypeOption, error) {
	var file workflowNodeTypeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Nodes) > 0 {
		return file.Nodes, nil
	}
	var option WorkflowNodeTypeOption
	if err := yaml.Unmarshal(data, &option); err != nil {
		return nil, err
	}
	if strings.TrimSpace(option.Type) == "" {
		return nil, fmt.Errorf("workflow node type file does not define type or nodes")
	}
	return []WorkflowNodeTypeOption{option}, nil
}

func validateBuiltInWorkflowNodeTypeOption(option WorkflowNodeTypeOption) error {
	if strings.TrimSpace(option.Type) == "" {
		return fmt.Errorf("workflow node type is required")
	}
	if strings.TrimSpace(option.Label) == "" {
		return fmt.Errorf("workflow node type %s label is required", option.Type)
	}
	if strings.TrimSpace(option.Category) == "" {
		return fmt.Errorf("workflow node type %s category is required", option.Type)
	}
	if err := validateWorkflowNodeMetadata(option); err != nil {
		return err
	}
	return nil
}

func cloneWorkflowNodeTypeOptions(options []WorkflowNodeTypeOption) []WorkflowNodeTypeOption {
	out := make([]WorkflowNodeTypeOption, len(options))
	for i, option := range options {
		out[i] = cloneWorkflowNodeTypeOption(option)
	}
	return out
}

func cloneWorkflowNodeTypeOption(option WorkflowNodeTypeOption) WorkflowNodeTypeOption {
	option.DefaultStage = cloneWorkflowGraphStageDocument(option.DefaultStage)
	option.Fields = append([]WorkflowNodeFieldOption(nil), option.Fields...)
	for i := range option.Fields {
		option.Fields[i].Options = append([]string(nil), option.Fields[i].Options...)
		option.Fields[i].Examples = append([]string(nil), option.Fields[i].Examples...)
		option.Fields[i].Hints = append([]string(nil), option.Fields[i].Hints...)
	}
	option.Outputs = append([]WorkflowNodeVariableOption(nil), option.Outputs...)
	option.Tags = append([]string(nil), option.Tags...)
	option.Hints = append([]string(nil), option.Hints...)
	option.Warnings = append([]string(nil), option.Warnings...)
	option.Examples = append([]WorkflowNodeExampleOption(nil), option.Examples...)
	for i := range option.Examples {
		option.Examples[i].Stage = cloneWorkflowGraphStageDocument(option.Examples[i].Stage)
		option.Examples[i].Notes = append([]string(nil), option.Examples[i].Notes...)
	}
	return option
}

func cloneWorkflowGraphStageDocument(stage WorkflowGraphStageDocument) WorkflowGraphStageDocument {
	stage.Params = copyStringMap(stage.Params)
	stage.Input = copyStringMap(stage.Input)
	stage.Outputs = copyStringMap(stage.Outputs)
	stage.Routes = copyStringMap(stage.Routes)
	stage.Cases = copyStringMap(stage.Cases)
	stage.Next = append([]string(nil), stage.Next...)
	stage.OnError = append([]string(nil), stage.OnError...)
	stage.Artifacts = append([]WorkflowGraphArtifactDocument(nil), stage.Artifacts...)
	for i := range stage.Artifacts {
		stage.Artifacts[i].Metadata = copyStringMap(stage.Artifacts[i].Metadata)
	}
	stage.AcceptanceCriteria = append([]WorkflowGraphAcceptanceCriterionDocument(nil), stage.AcceptanceCriteria...)
	return stage
}
