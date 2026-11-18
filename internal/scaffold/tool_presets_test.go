package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInToolPresetsLoadFromYAML(t *testing.T) {
	presets := BuiltInToolPresets()
	if len(presets) != 1 {
		t.Fatalf("expected built-in tool presets, got %d", len(presets))
	}
	if _, ok := BuiltInToolPresetByName("python-container-readonly"); ok {
		t.Fatal("container preset must be removed")
	}
	local, ok := BuiltInToolPresetByName("python-local")
	if !ok || local.Isolation != "auto" {
		t.Fatalf("expected native local preset, got %#v", local)
	}
}

func TestToolPresetsFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
presets:
  - name: python-local
    default_name: custom-reader
    title: Custom Reader
    description: override
    category: custom
    language: python
    isolation: process_group
    requires_restart: true
  - name: python-notes-local
    default_name: notes-helper
    title: Python Notes Helper
    description: local notes helper preset
    category: local
    language: python
    isolation: process_group
    requires_restart: true
`), 0o644); err != nil {
		t.Fatalf("write custom tool preset: %v", err)
	}
	presets, err := ToolPresetsFromDirs(dir)
	if err != nil {
		t.Fatalf("ToolPresetsFromDirs: %v", err)
	}
	var local, notes ToolPreset
	for _, preset := range presets {
		switch preset.Name {
		case "python-local":
			local = preset
		case "python-notes-local":
			notes = preset
		}
	}
	if local.Title != "Custom Reader" || local.DefaultName != "custom-reader" {
		t.Fatalf("expected custom local override, got %#v", local)
	}
	if notes.Name != "python-notes-local" || notes.Language != "python" {
		t.Fatalf("expected custom python notes preset, got %#v", notes)
	}
}
