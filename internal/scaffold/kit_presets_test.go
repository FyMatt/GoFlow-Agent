package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInKitPresetsLoadFromYAML(t *testing.T) {
	presets := BuiltInKitPresets()
	if len(presets) < 9 {
		t.Fatalf("expected built-in kit presets, got %d", len(presets))
	}
	binary, ok := BuiltInKitPresetByName("binary-analysis")
	if !ok {
		t.Fatal("expected binary-analysis preset")
	}
	if binary.RecommendedWorkflow != "binary-triage" {
		t.Fatalf("unexpected binary workflow: %q", binary.RecommendedWorkflow)
	}
	if len(binary.Examples) == 0 || binary.Examples[0].Request == "" {
		t.Fatalf("expected binary preset example request, got %#v", binary.Examples)
	}
	if len(binary.Tools) == 0 || binary.Tools[0] == "" {
		t.Fatalf("expected binary preset tools, got %#v", binary.Tools)
	}
}

func TestKitPresetsFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
presets:
  - name: binary-analysis
    title: Custom Binary
    description: override
    category: custom
    agents: [custom-agent]
  - name: finance-analysis
    title: Finance Analysis Kit
    description: custom finance kit
    category: finance
    agents: [finance-agent]
`), 0o644); err != nil {
		t.Fatalf("write custom preset: %v", err)
	}
	presets, err := KitPresetsFromDirs(dir)
	if err != nil {
		t.Fatalf("KitPresetsFromDirs: %v", err)
	}
	var binary, finance KitPreset
	for _, preset := range presets {
		switch preset.Name {
		case "binary-analysis":
			binary = preset
		case "finance-analysis":
			finance = preset
		}
	}
	if binary.Title != "Custom Binary" || len(binary.Agents) != 1 || binary.Agents[0] != "custom-agent" {
		t.Fatalf("expected custom binary override, got %#v", binary)
	}
	if finance.Name != "finance-analysis" || finance.Category != "finance" {
		t.Fatalf("expected custom finance preset, got %#v", finance)
	}
}
