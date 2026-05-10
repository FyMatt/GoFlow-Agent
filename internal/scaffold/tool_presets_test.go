package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInToolPresetsLoadFromYAML(t *testing.T) {
	presets := BuiltInToolPresets()
	if len(presets) < 5 {
		t.Fatalf("expected built-in tool presets, got %d", len(presets))
	}
	readonly, ok := BuiltInToolPresetByName("python-container-readonly")
	if !ok {
		t.Fatal("expected python-container-readonly preset")
	}
	if readonly.Isolation != "container" || readonly.WorkspaceMount != "ro" || readonly.NetworkMode != "disabled" {
		t.Fatalf("unexpected readonly preset: %#v", readonly)
	}
	if len(readonly.SafetyGuards) == 0 || len(readonly.Capabilities) == 0 {
		t.Fatalf("expected Studio guidance fields, got %#v", readonly)
	}
}

func TestToolPresetsFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
presets:
  - name: python-container-readonly
    default_name: custom-reader
    title: Custom Reader
    description: override
    category: custom
    language: python
    isolation: container
    workspace_mount: ro
    network_mode: disabled
    isolation_profile: readonly
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
	var readonly, notes ToolPreset
	for _, preset := range presets {
		switch preset.Name {
		case "python-container-readonly":
			readonly = preset
		case "python-notes-local":
			notes = preset
		}
	}
	if readonly.Title != "Custom Reader" || readonly.DefaultName != "custom-reader" {
		t.Fatalf("expected custom readonly override, got %#v", readonly)
	}
	if notes.Name != "python-notes-local" || notes.Language != "python" {
		t.Fatalf("expected custom python notes preset, got %#v", notes)
	}
}
