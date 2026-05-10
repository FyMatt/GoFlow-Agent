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

//go:embed templates/tools/scaffolds/*.yaml
var embeddedToolPresetFS embed.FS

// ToolPreset describes a reusable MCP tool scaffold preset.
type ToolPreset struct {
	Name             string            `json:"name" yaml:"name"`
	DefaultName      string            `json:"default_name,omitempty" yaml:"default_name,omitempty"`
	Title            string            `json:"title" yaml:"title"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	Category         string            `json:"category,omitempty" yaml:"category,omitempty"`
	Tags             []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Language         string            `json:"language,omitempty" yaml:"language,omitempty"`
	Isolation        string            `json:"isolation,omitempty" yaml:"isolation,omitempty"`
	WorkspaceMount   string            `json:"workspace_mount,omitempty" yaml:"workspace_mount,omitempty"`
	NetworkMode      string            `json:"network_mode,omitempty" yaml:"network_mode,omitempty"`
	IsolationProfile string            `json:"isolation_profile,omitempty" yaml:"isolation_profile,omitempty"`
	DefaultImage     string            `json:"default_image,omitempty" yaml:"default_image,omitempty"`
	DefaultOptions   map[string]string `json:"default_isolation_options,omitempty" yaml:"default_isolation_options,omitempty"`
	RiskLevel        string            `json:"risk_level,omitempty" yaml:"risk_level,omitempty"`
	RequiresRestart  bool              `json:"requires_restart" yaml:"requires_restart"`
	Capabilities     []string          `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	SafetyGuards     []string          `json:"safety_guards,omitempty" yaml:"safety_guards,omitempty"`
	GeneratedPaths   []string          `json:"generated_paths,omitempty" yaml:"generated_paths,omitempty"`
	ActivationSteps  []string          `json:"activation_steps,omitempty" yaml:"activation_steps,omitempty"`
	Recommendations  []string          `json:"recommendations,omitempty" yaml:"recommendations,omitempty"`
}

type toolPresetFile struct {
	Presets []ToolPreset `yaml:"presets"`
}

var (
	builtinToolPresetOnce sync.Once
	builtinToolPresets    []ToolPreset
	builtinToolPresetErr  error
)

// BuiltInToolPresets returns embedded MCP tool scaffold presets.
func BuiltInToolPresets() []ToolPreset {
	presets, err := loadBuiltInToolPresets()
	if err != nil {
		panic(err)
	}
	return cloneToolPresets(presets)
}

// BuiltInToolPresetByName returns one embedded MCP tool scaffold preset.
func BuiltInToolPresetByName(name string) (ToolPreset, bool) {
	normalized := normalizeName(name)
	for _, preset := range BuiltInToolPresets() {
		if normalizeName(preset.Name) == normalized {
			return preset, true
		}
	}
	return ToolPreset{}, false
}

// ToolPresetsFromDirs loads embedded presets, then applies runtime overrides.
func ToolPresetsFromDirs(dirs ...string) ([]ToolPreset, error) {
	presets := BuiltInToolPresets()
	indexes := make(map[string]int, len(presets))
	for i, preset := range presets {
		indexes[normalizeName(preset.Name)] = i
	}
	for _, dir := range dirs {
		custom, err := loadToolPresetDir(dir)
		if err != nil {
			return nil, err
		}
		for _, preset := range custom {
			preset = normalizeToolPreset(preset)
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
	return cloneToolPresets(presets), nil
}

func loadBuiltInToolPresets() ([]ToolPreset, error) {
	builtinToolPresetOnce.Do(func() {
		data, err := embeddedToolPresetFS.ReadFile("templates/tools/scaffolds/presets.yaml")
		if err != nil {
			builtinToolPresetErr = fmt.Errorf("read built-in tool scaffold presets: %w", err)
			return
		}
		var file toolPresetFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			builtinToolPresetErr = fmt.Errorf("parse built-in tool scaffold presets: %w", err)
			return
		}
		if len(file.Presets) == 0 {
			builtinToolPresetErr = fmt.Errorf("built-in tool scaffold presets are empty")
			return
		}
		seen := make(map[string]struct{}, len(file.Presets))
		for _, preset := range file.Presets {
			preset = normalizeToolPreset(preset)
			name := normalizeName(preset.Name)
			if name == "" {
				builtinToolPresetErr = fmt.Errorf("built-in tool scaffold preset has empty name")
				return
			}
			if _, exists := seen[name]; exists {
				builtinToolPresetErr = fmt.Errorf("duplicate built-in tool scaffold preset %q", preset.Name)
				return
			}
			seen[name] = struct{}{}
			builtinToolPresets = append(builtinToolPresets, preset)
		}
	})
	if builtinToolPresetErr != nil {
		return nil, builtinToolPresetErr
	}
	return builtinToolPresets, nil
}

func loadToolPresetDir(dir string) ([]ToolPreset, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tool scaffold preset directory %s: %w", dir, err)
	}
	out := make([]ToolPreset, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		items, err := loadToolPresetFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadToolPresetFile(path string) ([]ToolPreset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tool scaffold preset %s: %w", path, err)
	}
	var file toolPresetFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse tool scaffold preset %s: %w", path, err)
	}
	if len(file.Presets) > 0 {
		return file.Presets, nil
	}
	var preset ToolPreset
	if err := yaml.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("parse tool scaffold preset %s: %w", path, err)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return nil, fmt.Errorf("tool scaffold preset %s does not define name or presets", path)
	}
	return []ToolPreset{preset}, nil
}

func normalizeToolPreset(preset ToolPreset) ToolPreset {
	preset.Name = normalizeName(preset.Name)
	if strings.TrimSpace(preset.DefaultName) == "" {
		preset.DefaultName = preset.Name
	} else {
		preset.DefaultName = normalizeName(preset.DefaultName)
	}
	preset.Title = strings.TrimSpace(preset.Title)
	preset.Description = strings.TrimSpace(preset.Description)
	preset.Category = strings.TrimSpace(preset.Category)
	preset.Language = strings.TrimSpace(preset.Language)
	preset.Isolation = strings.TrimSpace(preset.Isolation)
	preset.WorkspaceMount = strings.TrimSpace(preset.WorkspaceMount)
	preset.NetworkMode = strings.TrimSpace(preset.NetworkMode)
	preset.IsolationProfile = strings.TrimSpace(preset.IsolationProfile)
	preset.DefaultImage = strings.TrimSpace(preset.DefaultImage)
	preset.RiskLevel = strings.TrimSpace(preset.RiskLevel)
	preset.Tags = normalizeStringList(preset.Tags, true)
	preset.Capabilities = normalizeStringList(preset.Capabilities, true)
	preset.SafetyGuards = normalizeStringList(preset.SafetyGuards, true)
	preset.GeneratedPaths = normalizeStringList(preset.GeneratedPaths, true)
	preset.ActivationSteps = normalizeStringList(preset.ActivationSteps, true)
	preset.Recommendations = normalizeStringList(preset.Recommendations, true)
	if preset.DefaultOptions != nil {
		options := make(map[string]string, len(preset.DefaultOptions))
		for key, value := range preset.DefaultOptions {
			key = strings.ToLower(strings.TrimSpace(key))
			if key != "" {
				options[key] = strings.TrimSpace(value)
			}
		}
		preset.DefaultOptions = options
	}
	return preset
}

func cloneToolPresets(presets []ToolPreset) []ToolPreset {
	out := make([]ToolPreset, len(presets))
	for i, preset := range presets {
		out[i] = cloneToolPreset(preset)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneToolPreset(preset ToolPreset) ToolPreset {
	preset.Tags = append([]string(nil), preset.Tags...)
	preset.Capabilities = append([]string(nil), preset.Capabilities...)
	preset.SafetyGuards = append([]string(nil), preset.SafetyGuards...)
	preset.GeneratedPaths = append([]string(nil), preset.GeneratedPaths...)
	preset.ActivationSteps = append([]string(nil), preset.ActivationSteps...)
	preset.Recommendations = append([]string(nil), preset.Recommendations...)
	if preset.DefaultOptions != nil {
		options := make(map[string]string, len(preset.DefaultOptions))
		for key, value := range preset.DefaultOptions {
			options[key] = value
		}
		preset.DefaultOptions = options
	}
	return preset
}
