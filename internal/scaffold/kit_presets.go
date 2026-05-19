package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/kits/scaffolds/*.yaml
var embeddedKitPresetFS embed.FS

type KitPreset struct {
	Name                string            `json:"name" yaml:"name"`
	DefaultKitName      string            `json:"default_kit_name,omitempty" yaml:"default_kit_name,omitempty"`
	Title               string            `json:"title" yaml:"title"`
	Description         string            `json:"description,omitempty" yaml:"description,omitempty"`
	Category            string            `json:"category,omitempty" yaml:"category,omitempty"`
	Tags                []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Providers           []string          `json:"providers,omitempty" yaml:"providers,omitempty"`
	Agents              []string          `json:"agents,omitempty" yaml:"agents,omitempty"`
	Skills              []string          `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools               []string          `json:"tools,omitempty" yaml:"tools,omitempty"`
	Workflows           []string          `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	WorkflowTemplates   []string          `json:"workflow_templates,omitempty" yaml:"workflow_templates,omitempty"`
	TeamTemplates       []string          `json:"team_templates,omitempty" yaml:"team_templates,omitempty"`
	PolicyRules         []string          `json:"policy_rules,omitempty" yaml:"policy_rules,omitempty"`
	RequiredEnv         []string          `json:"required_env,omitempty" yaml:"required_env,omitempty"`
	RecommendedWorkflow string            `json:"recommended_workflow,omitempty" yaml:"recommended_workflow,omitempty"`
	RecommendedAgent    string            `json:"recommended_agent,omitempty" yaml:"recommended_agent,omitempty"`
	Examples            []KitExample      `json:"examples,omitempty" yaml:"examples,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	VerticalPack        *KitVerticalPack  `json:"vertical_pack,omitempty" yaml:"vertical_pack,omitempty"`
}

type KitExample struct {
	Title       string `json:"title,omitempty" yaml:"title,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Request     string `json:"request,omitempty" yaml:"request,omitempty"`
	Workflow    string `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Agent       string `json:"agent,omitempty" yaml:"agent,omitempty"`
}

type KitVerticalPack struct {
	Domain            string                     `json:"domain,omitempty" yaml:"domain,omitempty"`
	Maturity          string                     `json:"maturity,omitempty" yaml:"maturity,omitempty"`
	Summary           string                     `json:"summary,omitempty" yaml:"summary,omitempty"`
	SupportedTasks    []string                   `json:"supported_tasks,omitempty" yaml:"supported_tasks,omitempty"`
	RequiredInputs    []string                   `json:"required_inputs,omitempty" yaml:"required_inputs,omitempty"`
	ToolBoundaries    []string                   `json:"tool_boundaries,omitempty" yaml:"tool_boundaries,omitempty"`
	SafetyGates       []string                   `json:"safety_gates,omitempty" yaml:"safety_gates,omitempty"`
	QualityGates      []string                   `json:"quality_gates,omitempty" yaml:"quality_gates,omitempty"`
	EvidenceArtifacts []string                   `json:"evidence_artifacts,omitempty" yaml:"evidence_artifacts,omitempty"`
	SimpleMode        []string                   `json:"simple_mode,omitempty" yaml:"simple_mode,omitempty"`
	ExpertMode        []string                   `json:"expert_mode,omitempty" yaml:"expert_mode,omitempty"`
	TokenStrategy     []string                   `json:"token_strategy,omitempty" yaml:"token_strategy,omitempty"`
	ModelRoutes       KitVerticalPackModelRoutes `json:"model_routes,omitempty" yaml:"model_routes,omitempty"`
}

type KitVerticalPackModelRoutes struct {
	Strong   []string `json:"strong,omitempty" yaml:"strong,omitempty"`
	Worker   []string `json:"worker,omitempty" yaml:"worker,omitempty"`
	Verifier []string `json:"verifier,omitempty" yaml:"verifier,omitempty"`
}

type kitPresetFile struct {
	Presets []KitPreset `yaml:"presets"`
}

var (
	builtinKitPresetOnce sync.Once
	builtinKitPresets    []KitPreset
	builtinKitPresetErr  error
)

func BuiltInKitPresets() []KitPreset {
	presets, err := loadBuiltInKitPresets()
	if err != nil {
		panic(err)
	}
	return cloneKitPresets(presets)
}

func BuiltInKitPresetByName(name string) (KitPreset, bool) {
	normalized := normalizeName(name)
	for _, preset := range BuiltInKitPresets() {
		if normalizeName(preset.Name) == normalized {
			return preset, true
		}
	}
	return KitPreset{}, false
}

func BuiltInKitPresetNames() []string {
	presets := BuiltInKitPresets()
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		if strings.TrimSpace(preset.Name) != "" {
			names = append(names, preset.Name)
		}
	}
	sort.Strings(names)
	return names
}

func KitPresetsFromDirs(dirs ...string) ([]KitPreset, error) {
	presets := BuiltInKitPresets()
	indexes := make(map[string]int, len(presets))
	for i, preset := range presets {
		indexes[normalizeName(preset.Name)] = i
	}
	for _, dir := range dirs {
		custom, err := loadKitPresetDir(dir)
		if err != nil {
			return nil, err
		}
		for _, preset := range custom {
			preset = normalizeKitPreset(preset)
			name := normalizeName(preset.Name)
			if name == "" {
				continue
			}
			if index, exists := indexes[name]; exists {
				presets[index] = preset
				continue
			}
			indexes[name] = len(presets)
			presets = append(presets, preset)
		}
	}
	return cloneKitPresets(presets), nil
}

func loadBuiltInKitPresets() ([]KitPreset, error) {
	builtinKitPresetOnce.Do(func() {
		data, err := embeddedKitPresetFS.ReadFile("templates/kits/scaffolds/presets.yaml")
		if err != nil {
			builtinKitPresetErr = fmt.Errorf("read built-in kit preset templates: %w", err)
			return
		}
		var file kitPresetFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			builtinKitPresetErr = fmt.Errorf("parse built-in kit preset templates: %w", err)
			return
		}
		if len(file.Presets) == 0 {
			builtinKitPresetErr = fmt.Errorf("built-in kit preset templates are empty")
			return
		}
		seen := make(map[string]struct{}, len(file.Presets))
		for _, preset := range file.Presets {
			name := normalizeName(preset.Name)
			if name == "" {
				builtinKitPresetErr = fmt.Errorf("built-in kit preset has empty name")
				return
			}
			if _, exists := seen[name]; exists {
				builtinKitPresetErr = fmt.Errorf("duplicate built-in kit preset %q", preset.Name)
				return
			}
			seen[name] = struct{}{}
			builtinKitPresets = append(builtinKitPresets, normalizeKitPreset(preset))
		}
	})
	if builtinKitPresetErr != nil {
		return nil, builtinKitPresetErr
	}
	return builtinKitPresets, nil
}

func loadKitPresetDir(dir string) ([]KitPreset, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read kit preset template directory %s: %w", dir, err)
	}
	out := make([]KitPreset, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		items, err := loadKitPresetFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadKitPresetFile(path string) ([]KitPreset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read kit preset template %s: %w", path, err)
	}
	var file kitPresetFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse kit preset template %s: %w", path, err)
	}
	if len(file.Presets) > 0 {
		return file.Presets, nil
	}
	var preset KitPreset
	if err := yaml.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("parse kit preset template %s: %w", path, err)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return nil, fmt.Errorf("kit preset template %s does not define name or presets", path)
	}
	return []KitPreset{preset}, nil
}

func normalizeKitPreset(preset KitPreset) KitPreset {
	preset.Name = normalizeName(preset.Name)
	if strings.TrimSpace(preset.DefaultKitName) == "" {
		preset.DefaultKitName = preset.Name + "-kit"
	} else {
		preset.DefaultKitName = normalizeName(preset.DefaultKitName)
	}
	preset.Category = strings.TrimSpace(preset.Category)
	preset.Title = strings.TrimSpace(preset.Title)
	preset.Description = strings.TrimSpace(preset.Description)
	preset.RecommendedWorkflow = normalizeName(preset.RecommendedWorkflow)
	preset.RecommendedAgent = normalizeName(preset.RecommendedAgent)
	preset.Tags = normalizeStringList(preset.Tags, false)
	preset.Providers = normalizeStringList(preset.Providers, false)
	preset.Agents = normalizeStringList(preset.Agents, false)
	preset.Skills = normalizeStringList(preset.Skills, false)
	preset.Tools = normalizeStringList(preset.Tools, true)
	preset.Workflows = normalizeStringList(preset.Workflows, false)
	preset.WorkflowTemplates = normalizeStringList(preset.WorkflowTemplates, false)
	preset.TeamTemplates = normalizeStringList(preset.TeamTemplates, false)
	preset.PolicyRules = normalizeStringList(preset.PolicyRules, true)
	preset.RequiredEnv = normalizeStringList(preset.RequiredEnv, true)
	if preset.Metadata != nil {
		meta := make(map[string]string, len(preset.Metadata))
		for key, value := range preset.Metadata {
			if strings.TrimSpace(key) != "" {
				meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		preset.Metadata = meta
	}
	preset.VerticalPack = normalizeKitVerticalPack(preset.VerticalPack)
	return preset
}

func cloneKitPresets(presets []KitPreset) []KitPreset {
	out := make([]KitPreset, len(presets))
	for i, preset := range presets {
		out[i] = cloneKitPreset(preset)
	}
	return out
}

func cloneKitPreset(preset KitPreset) KitPreset {
	preset.Tags = append([]string(nil), preset.Tags...)
	preset.Providers = append([]string(nil), preset.Providers...)
	preset.Agents = append([]string(nil), preset.Agents...)
	preset.Skills = append([]string(nil), preset.Skills...)
	preset.Tools = append([]string(nil), preset.Tools...)
	preset.Workflows = append([]string(nil), preset.Workflows...)
	preset.WorkflowTemplates = append([]string(nil), preset.WorkflowTemplates...)
	preset.TeamTemplates = append([]string(nil), preset.TeamTemplates...)
	preset.PolicyRules = append([]string(nil), preset.PolicyRules...)
	preset.RequiredEnv = append([]string(nil), preset.RequiredEnv...)
	preset.Examples = append([]KitExample(nil), preset.Examples...)
	if preset.Metadata != nil {
		meta := make(map[string]string, len(preset.Metadata))
		for key, value := range preset.Metadata {
			meta[key] = value
		}
		preset.Metadata = meta
	}
	preset.VerticalPack = cloneKitVerticalPack(preset.VerticalPack)
	return preset
}

func normalizeKitVerticalPack(pack *KitVerticalPack) *KitVerticalPack {
	if pack == nil {
		return nil
	}
	normalized := *pack
	normalized.Domain = normalizeName(normalized.Domain)
	normalized.Maturity = normalizeName(normalized.Maturity)
	normalized.Summary = strings.TrimSpace(normalized.Summary)
	normalized.SupportedTasks = normalizeStringList(normalized.SupportedTasks, true)
	normalized.RequiredInputs = normalizeStringList(normalized.RequiredInputs, true)
	normalized.ToolBoundaries = normalizeStringList(normalized.ToolBoundaries, true)
	normalized.SafetyGates = normalizeStringList(normalized.SafetyGates, true)
	normalized.QualityGates = normalizeStringList(normalized.QualityGates, true)
	normalized.EvidenceArtifacts = normalizeStringList(normalized.EvidenceArtifacts, true)
	normalized.SimpleMode = normalizeStringList(normalized.SimpleMode, true)
	normalized.ExpertMode = normalizeStringList(normalized.ExpertMode, true)
	normalized.TokenStrategy = normalizeStringList(normalized.TokenStrategy, true)
	normalized.ModelRoutes.Strong = normalizeStringList(normalized.ModelRoutes.Strong, true)
	normalized.ModelRoutes.Worker = normalizeStringList(normalized.ModelRoutes.Worker, true)
	normalized.ModelRoutes.Verifier = normalizeStringList(normalized.ModelRoutes.Verifier, true)
	if normalized.Domain == "" && normalized.Maturity == "" && normalized.Summary == "" &&
		len(normalized.SupportedTasks) == 0 && len(normalized.RequiredInputs) == 0 &&
		len(normalized.ToolBoundaries) == 0 && len(normalized.SafetyGates) == 0 &&
		len(normalized.QualityGates) == 0 && len(normalized.EvidenceArtifacts) == 0 &&
		len(normalized.SimpleMode) == 0 && len(normalized.ExpertMode) == 0 &&
		len(normalized.TokenStrategy) == 0 && len(normalized.ModelRoutes.Strong) == 0 &&
		len(normalized.ModelRoutes.Worker) == 0 && len(normalized.ModelRoutes.Verifier) == 0 {
		return nil
	}
	return &normalized
}

func cloneKitVerticalPack(pack *KitVerticalPack) *KitVerticalPack {
	if pack == nil {
		return nil
	}
	clone := *pack
	clone.SupportedTasks = append([]string(nil), pack.SupportedTasks...)
	clone.RequiredInputs = append([]string(nil), pack.RequiredInputs...)
	clone.ToolBoundaries = append([]string(nil), pack.ToolBoundaries...)
	clone.SafetyGates = append([]string(nil), pack.SafetyGates...)
	clone.QualityGates = append([]string(nil), pack.QualityGates...)
	clone.EvidenceArtifacts = append([]string(nil), pack.EvidenceArtifacts...)
	clone.SimpleMode = append([]string(nil), pack.SimpleMode...)
	clone.ExpertMode = append([]string(nil), pack.ExpertMode...)
	clone.TokenStrategy = append([]string(nil), pack.TokenStrategy...)
	clone.ModelRoutes.Strong = append([]string(nil), pack.ModelRoutes.Strong...)
	clone.ModelRoutes.Worker = append([]string(nil), pack.ModelRoutes.Worker...)
	clone.ModelRoutes.Verifier = append([]string(nil), pack.ModelRoutes.Verifier...)
	return &clone
}

func normalizeStringList(values []string, keepCase bool) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !keepCase {
			value = normalizeName(value)
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeName(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}
