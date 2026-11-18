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

//go:embed templates/teams/scaffolds/*.yaml
var embeddedTeamPresetFS embed.FS

// TeamPreset describes a reusable Team Template scaffold preset.
type TeamPreset struct {
	Name                  string   `json:"name" yaml:"name"`
	DefaultName           string   `json:"default_name,omitempty" yaml:"default_name,omitempty"`
	Title                 string   `json:"title" yaml:"title"`
	Description           string   `json:"description,omitempty" yaml:"description,omitempty"`
	Category              string   `json:"category,omitempty" yaml:"category,omitempty"`
	Tags                  []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	BaseTemplate          string   `json:"base_template" yaml:"base_template"`
	RecommendedWorkflow   string   `json:"recommended_workflow,omitempty" yaml:"recommended_workflow,omitempty"`
	RecommendedEntryAgent string   `json:"recommended_entry_agent,omitempty" yaml:"recommended_entry_agent,omitempty"`
}

type teamPresetFile struct {
	Presets []TeamPreset `yaml:"presets"`
}

var (
	builtinTeamPresetOnce sync.Once
	builtinTeamPresets    []TeamPreset
	builtinTeamPresetErr  error
)

// BuiltInTeamPresets returns embedded Team Template scaffold presets.
func BuiltInTeamPresets() []TeamPreset {
	presets, err := loadBuiltInTeamPresets()
	if err != nil {
		panic(err)
	}
	return cloneTeamPresets(presets)
}

// BuiltInTeamPresetByName returns one embedded Team Template scaffold preset.
func BuiltInTeamPresetByName(name string) (TeamPreset, bool) {
	normalized := normalizeName(name)
	for _, preset := range BuiltInTeamPresets() {
		if normalizeName(preset.Name) == normalized {
			return preset, true
		}
	}
	return TeamPreset{}, false
}

// TeamPresetsFromDirs loads embedded presets, then applies runtime overrides.
func TeamPresetsFromDirs(dirs ...string) ([]TeamPreset, error) {
	presets := BuiltInTeamPresets()
	indexes := make(map[string]int, len(presets))
	for i, preset := range presets {
		indexes[normalizeName(preset.Name)] = i
	}
	for _, dir := range dirs {
		custom, err := loadTeamPresetDir(dir)
		if err != nil {
			return nil, err
		}
		for _, preset := range custom {
			preset = normalizeTeamPreset(preset)
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
	return cloneTeamPresets(presets), nil
}

func loadBuiltInTeamPresets() ([]TeamPreset, error) {
	builtinTeamPresetOnce.Do(func() {
		data, err := embeddedTeamPresetFS.ReadFile("templates/teams/scaffolds/presets.yaml")
		if err != nil {
			builtinTeamPresetErr = fmt.Errorf("read built-in team scaffold presets: %w", err)
			return
		}
		var file teamPresetFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			builtinTeamPresetErr = fmt.Errorf("parse built-in team scaffold presets: %w", err)
			return
		}
		if len(file.Presets) == 0 {
			builtinTeamPresetErr = fmt.Errorf("built-in team scaffold presets are empty")
			return
		}
		seen := make(map[string]struct{}, len(file.Presets))
		for _, preset := range file.Presets {
			preset = normalizeTeamPreset(preset)
			name := normalizeName(preset.Name)
			if name == "" {
				builtinTeamPresetErr = fmt.Errorf("built-in team scaffold preset has empty name")
				return
			}
			if _, exists := seen[name]; exists {
				builtinTeamPresetErr = fmt.Errorf("duplicate built-in team scaffold preset %q", preset.Name)
				return
			}
			seen[name] = struct{}{}
			builtinTeamPresets = append(builtinTeamPresets, preset)
		}
	})
	if builtinTeamPresetErr != nil {
		return nil, builtinTeamPresetErr
	}
	return builtinTeamPresets, nil
}

func loadTeamPresetDir(dir string) ([]TeamPreset, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read team scaffold preset directory %s: %w", dir, err)
	}
	out := make([]TeamPreset, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		items, err := loadTeamPresetFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func loadTeamPresetFile(path string) ([]TeamPreset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read team scaffold preset %s: %w", path, err)
	}
	var file teamPresetFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse team scaffold preset %s: %w", path, err)
	}
	if len(file.Presets) > 0 {
		return file.Presets, nil
	}
	var preset TeamPreset
	if err := yaml.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("parse team scaffold preset %s: %w", path, err)
	}
	if strings.TrimSpace(preset.Name) == "" {
		return nil, fmt.Errorf("team scaffold preset %s does not define name or presets", path)
	}
	return []TeamPreset{preset}, nil
}

func normalizeTeamPreset(preset TeamPreset) TeamPreset {
	preset.Name = normalizeName(preset.Name)
	if strings.TrimSpace(preset.DefaultName) == "" {
		preset.DefaultName = "custom-" + preset.Name + "-team"
	} else {
		preset.DefaultName = normalizeName(preset.DefaultName)
	}
	preset.Title = strings.TrimSpace(preset.Title)
	preset.Description = strings.TrimSpace(preset.Description)
	preset.Category = strings.TrimSpace(preset.Category)
	preset.BaseTemplate = normalizeName(preset.BaseTemplate)
	preset.RecommendedWorkflow = normalizeName(preset.RecommendedWorkflow)
	preset.RecommendedEntryAgent = normalizeName(preset.RecommendedEntryAgent)
	preset.Tags = normalizeStringList(preset.Tags, true)
	return preset
}

func cloneTeamPresets(presets []TeamPreset) []TeamPreset {
	out := make([]TeamPreset, len(presets))
	for i, preset := range presets {
		out[i] = cloneTeamPreset(preset)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneTeamPreset(preset TeamPreset) TeamPreset {
	preset.Tags = append([]string(nil), preset.Tags...)
	return preset
}
