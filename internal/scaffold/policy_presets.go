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

//go:embed templates/policies/scaffolds/*.yaml
var embeddedPolicyPresetFS embed.FS

// PolicyRulePreset describes a reusable workflow policy rule scaffold.
type PolicyRulePreset struct {
	Name        string                  `json:"name" yaml:"name"`
	DefaultName string                  `json:"default_name,omitempty" yaml:"default_name,omitempty"`
	Title       string                  `json:"title" yaml:"title"`
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Category    string                  `json:"category,omitempty" yaml:"category,omitempty"`
	Tags        []string                `json:"tags,omitempty" yaml:"tags,omitempty"`
	Operator    string                  `json:"operator" yaml:"operator"`
	Expression  string                  `json:"expression,omitempty" yaml:"expression,omitempty"`
	Reason      string                  `json:"reason,omitempty" yaml:"reason,omitempty"`
	Defaults    map[string]string       `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Params      []PolicyRuleFieldOption `json:"params,omitempty" yaml:"params,omitempty"`
}

// PolicyRuleFieldOption mirrors workflow node field metadata without tying
// scaffold presets to the runtime workflow package.
type PolicyRuleFieldOption struct {
	Name        string   `json:"name" yaml:"name"`
	Label       string   `json:"label,omitempty" yaml:"label,omitempty"`
	Type        string   `json:"type,omitempty" yaml:"type,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Placeholder string   `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty" yaml:"default,omitempty"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
	Examples    []string `json:"examples,omitempty" yaml:"examples,omitempty"`
	Hints       []string `json:"hints,omitempty" yaml:"hints,omitempty"`
}

type policyRulePresetFile struct {
	Presets []PolicyRulePreset `yaml:"presets"`
}

var (
	builtinPolicyRulePresetOnce sync.Once
	builtinPolicyRulePresets    []PolicyRulePreset
	builtinPolicyRulePresetErr  error
)

// BuiltInPolicyRulePresets returns embedded policy rule scaffold presets.
func BuiltInPolicyRulePresets() []PolicyRulePreset {
	presets, err := loadBuiltInPolicyRulePresets()
	if err != nil {
		panic(err)
	}
	return clonePolicyRulePresets(presets)
}

// BuiltInPolicyRulePresetByName returns one embedded policy rule scaffold preset.
func BuiltInPolicyRulePresetByName(name string) (PolicyRulePreset, bool) {
	normalized := normalizeName(name)
	for _, preset := range BuiltInPolicyRulePresets() {
		if normalizeName(preset.Name) == normalized {
			return preset, true
		}
	}
	return PolicyRulePreset{}, false
}

// PolicyRulePresetsFromDirs loads embedded presets, then applies runtime overrides.
func PolicyRulePresetsFromDirs(dirs ...string) ([]PolicyRulePreset, error) {
	presets := BuiltInPolicyRulePresets()
	indexes := make(map[string]int, len(presets))
	for i, preset := range presets {
		indexes[normalizeName(preset.Name)] = i
	}
	for _, dir := range dirs {
		custom, err := loadPolicyRulePresetDir(dir)
		if err != nil {
			return nil, err
		}
		for _, preset := range custom {
			preset = normalizePolicyRulePreset(preset)
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
	return clonePolicyRulePresets(presets), nil
}

func loadBuiltInPolicyRulePresets() ([]PolicyRulePreset, error) {
	builtinPolicyRulePresetOnce.Do(func() {
		data, err := embeddedPolicyPresetFS.ReadFile("templates/policies/scaffolds/presets.yaml")
		if err != nil {
			builtinPolicyRulePresetErr = fmt.Errorf("read built-in policy rule scaffold presets: %w", err)
			return
		}
		var file policyRulePresetFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			builtinPolicyRulePresetErr = fmt.Errorf("parse built-in policy rule scaffold presets: %w", err)
			return
		}
		if len(file.Presets) == 0 {
			builtinPolicyRulePresetErr = fmt.Errorf("built-in policy rule scaffold presets are empty")
			return
		}
		seen := make(map[string]struct{}, len(file.Presets))
		for _, preset := range file.Presets {
			preset = normalizePolicyRulePreset(preset)
			name := normalizeName(preset.Name)
			if name == "" {
				builtinPolicyRulePresetErr = fmt.Errorf("built-in policy rule scaffold preset has empty name")
				return
			}
			if _, exists := seen[name]; exists {
				builtinPolicyRulePresetErr = fmt.Errorf("duplicate built-in policy rule scaffold preset %q", preset.Name)
				return
			}
			seen[name] = struct{}{}
			builtinPolicyRulePresets = append(builtinPolicyRulePresets, preset)
		}
	})
	if builtinPolicyRulePresetErr != nil {
		return nil, builtinPolicyRulePresetErr
	}
	return builtinPolicyRulePresets, nil
}

func loadPolicyRulePresetDir(dir string) ([]PolicyRulePreset, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read policy rule scaffold preset directory %s: %w", dir, err)
	}
	out := make([]PolicyRulePreset, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		items, err := loadPolicyRulePresetFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadPolicyRulePresetFile(path string) ([]PolicyRulePreset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy rule scaffold preset %s: %w", path, err)
	}
	var file policyRulePresetFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse policy rule scaffold preset %s: %w", path, err)
	}
	if len(file.Presets) > 0 {
		return file.Presets, nil
	}
	var preset PolicyRulePreset
	if err := yaml.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("parse policy rule scaffold preset %s: %w", path, err)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return nil, fmt.Errorf("policy rule scaffold preset %s does not define name or presets", path)
	}
	return []PolicyRulePreset{preset}, nil
}

func normalizePolicyRulePreset(preset PolicyRulePreset) PolicyRulePreset {
	preset.Name = normalizeName(preset.Name)
	if strings.TrimSpace(preset.DefaultName) == "" {
		preset.DefaultName = preset.Name
	} else {
		preset.DefaultName = normalizeName(preset.DefaultName)
	}
	preset.Title = strings.TrimSpace(preset.Title)
	preset.Description = strings.TrimSpace(preset.Description)
	preset.Category = strings.TrimSpace(preset.Category)
	preset.Operator = strings.TrimSpace(preset.Operator)
	preset.Expression = strings.TrimSpace(preset.Expression)
	preset.Reason = strings.TrimSpace(preset.Reason)
	preset.Tags = normalizeStringList(preset.Tags, true)
	if preset.Defaults != nil {
		defaults := make(map[string]string, len(preset.Defaults))
		for key, value := range preset.Defaults {
			key = strings.TrimSpace(key)
			if key != "" {
				defaults[key] = strings.TrimSpace(value)
			}
		}
		preset.Defaults = defaults
	}
	for i := range preset.Params {
		preset.Params[i] = normalizePolicyRuleFieldOption(preset.Params[i])
	}
	return preset
}

func normalizePolicyRuleFieldOption(field PolicyRuleFieldOption) PolicyRuleFieldOption {
	field.Name = strings.TrimSpace(field.Name)
	field.Label = strings.TrimSpace(field.Label)
	field.Type = strings.TrimSpace(field.Type)
	field.Description = strings.TrimSpace(field.Description)
	field.Placeholder = strings.TrimSpace(field.Placeholder)
	field.Default = strings.TrimSpace(field.Default)
	field.Options = normalizeStringList(field.Options, true)
	field.Examples = normalizeStringList(field.Examples, true)
	field.Hints = normalizeStringList(field.Hints, true)
	return field
}

func clonePolicyRulePresets(presets []PolicyRulePreset) []PolicyRulePreset {
	out := make([]PolicyRulePreset, len(presets))
	for i, preset := range presets {
		out[i] = clonePolicyRulePreset(preset)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func clonePolicyRulePreset(preset PolicyRulePreset) PolicyRulePreset {
	preset.Tags = append([]string(nil), preset.Tags...)
	preset.Params = append([]PolicyRuleFieldOption(nil), preset.Params...)
	for i := range preset.Params {
		preset.Params[i].Options = append([]string(nil), preset.Params[i].Options...)
		preset.Params[i].Examples = append([]string(nil), preset.Params[i].Examples...)
		preset.Params[i].Hints = append([]string(nil), preset.Params[i].Hints...)
	}
	if preset.Defaults != nil {
		defaults := make(map[string]string, len(preset.Defaults))
		for key, value := range preset.Defaults {
			defaults[key] = value
		}
		preset.Defaults = defaults
	}
	return preset
}
