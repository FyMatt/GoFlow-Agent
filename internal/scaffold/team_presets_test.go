package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInTeamPresetsLoadFromYAML(t *testing.T) {
	presets := BuiltInTeamPresets()
	if len(presets) < 8 {
		t.Fatalf("expected built-in team presets, got %d", len(presets))
	}
	framework, ok := BuiltInTeamPresetByName("agent-framework")
	if !ok {
		t.Fatal("expected agent-framework preset")
	}
	if framework.BaseTemplate != "framework-extension-team" || framework.RecommendedWorkflow != "agent-framework-extension" {
		t.Fatalf("unexpected agent-framework preset: %#v", framework)
	}
}

func TestTeamPresetsFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
presets:
  - name: software-review
    default_name: custom-platform-review-team
    title: Platform Review Team
    description: Runtime override.
    category: platform
    base_template: software-task-team
    recommended_workflow: plan-fix-audit
    recommended_entry_agent: planner
  - name: finance-review
    title: Finance Review Team
    description: Finance analysis collaboration preset.
    category: finance
    base_template: documentation-team
    recommended_workflow: docs-review-publish
    recommended_entry_agent: planner
`), 0o644); err != nil {
		t.Fatalf("write custom team preset: %v", err)
	}
	presets, err := TeamPresetsFromDirs(dir)
	if err != nil {
		t.Fatalf("TeamPresetsFromDirs: %v", err)
	}
	var software, finance TeamPreset
	for _, preset := range presets {
		switch preset.Name {
		case "software-review":
			software = preset
		case "finance-review":
			finance = preset
		}
	}
	if software.Title != "Platform Review Team" || software.DefaultName != "custom-platform-review-team" {
		t.Fatalf("expected custom software override, got %#v", software)
	}
	if finance.Name != "finance-review" || finance.DefaultName != "custom-finance-review-team" {
		t.Fatalf("expected custom finance preset, got %#v", finance)
	}
}
