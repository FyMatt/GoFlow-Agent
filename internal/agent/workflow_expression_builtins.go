package agent

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/expression_helpers/*.yaml
var embeddedWorkflowExpressionFunctionFS embed.FS

type workflowExpressionFunctionFile struct {
	Functions []WorkflowExpressionFunctionOption `yaml:"functions"`
}

var (
	builtInWorkflowExpressionFunctionOnce  sync.Once
	builtInWorkflowExpressionFunctionCache []WorkflowExpressionFunctionOption
	builtInWorkflowExpressionFunctionErr   error
)

func builtInWorkflowExpressionFunctions() []WorkflowExpressionFunctionOption {
	options, err := loadBuiltInWorkflowExpressionFunctions()
	if err != nil {
		panic(err)
	}
	return cloneWorkflowExpressionFunctionOptions(options)
}

func loadBuiltInWorkflowExpressionFunctions() ([]WorkflowExpressionFunctionOption, error) {
	builtInWorkflowExpressionFunctionOnce.Do(func() {
		entries, err := embeddedWorkflowExpressionFunctionFS.ReadDir("templates/expression_helpers")
		if err != nil {
			builtInWorkflowExpressionFunctionErr = fmt.Errorf("read built-in expression helpers: %w", err)
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
			data, err := embeddedWorkflowExpressionFunctionFS.ReadFile("templates/expression_helpers/" + name)
			if err != nil {
				builtInWorkflowExpressionFunctionErr = fmt.Errorf("read built-in expression helper %s: %w", name, err)
				return
			}
			options, err := parseWorkflowExpressionFunctionData(data)
			if err != nil {
				builtInWorkflowExpressionFunctionErr = fmt.Errorf("parse built-in expression helper %s: %w", name, err)
				return
			}
			for _, option := range options {
				option.Name = normalizeWorkflowSkillName(option.Name)
				if err := validateWorkflowExpressionFunctionMetadata(option); err != nil {
					builtInWorkflowExpressionFunctionErr = fmt.Errorf("validate built-in expression helper %s: %w", name, err)
					return
				}
				if _, exists := seen[option.Name]; exists {
					builtInWorkflowExpressionFunctionErr = fmt.Errorf("duplicate built-in expression helper %q", option.Name)
					return
				}
				seen[option.Name] = struct{}{}
				builtInWorkflowExpressionFunctionCache = append(builtInWorkflowExpressionFunctionCache, option)
			}
		}
		if len(builtInWorkflowExpressionFunctionCache) == 0 {
			builtInWorkflowExpressionFunctionErr = fmt.Errorf("built-in expression helpers are empty")
			return
		}
		sortWorkflowExpressionFunctionOptions(builtInWorkflowExpressionFunctionCache)
	})
	if builtInWorkflowExpressionFunctionErr != nil {
		return nil, builtInWorkflowExpressionFunctionErr
	}
	return builtInWorkflowExpressionFunctionCache, nil
}

func parseWorkflowExpressionFunctionData(data []byte) ([]WorkflowExpressionFunctionOption, error) {
	var file workflowExpressionFunctionFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Functions) > 0 {
		return file.Functions, nil
	}
	var option WorkflowExpressionFunctionOption
	if err := yaml.Unmarshal(data, &option); err != nil {
		return nil, err
	}
	if strings.TrimSpace(option.Name) == "" {
		return nil, fmt.Errorf("expression helper file does not define name or functions")
	}
	return []WorkflowExpressionFunctionOption{option}, nil
}

func sortWorkflowExpressionFunctionOptions(options []WorkflowExpressionFunctionOption) {
	sort.Slice(options, func(i, j int) bool {
		if options[i].Category != options[j].Category {
			return options[i].Category < options[j].Category
		}
		return options[i].Name < options[j].Name
	})
}

func cloneWorkflowExpressionFunctionOptions(options []WorkflowExpressionFunctionOption) []WorkflowExpressionFunctionOption {
	out := make([]WorkflowExpressionFunctionOption, len(options))
	for i, option := range options {
		out[i] = cloneWorkflowExpressionFunctionOption(option)
	}
	return out
}

func cloneWorkflowExpressionFunctionOption(option WorkflowExpressionFunctionOption) WorkflowExpressionFunctionOption {
	option.Modes = append([]string(nil), option.Modes...)
	option.NodeTypes = append([]string(nil), option.NodeTypes...)
	option.Args = append([]WorkflowExpressionFunctionArgument(nil), option.Args...)
	for i := range option.Args {
		option.Args[i].Accepts = append([]string(nil), option.Args[i].Accepts...)
	}
	option.Examples = append([]string(nil), option.Examples...)
	option.Hints = append([]string(nil), option.Hints...)
	option.Warnings = append([]string(nil), option.Warnings...)
	return option
}
