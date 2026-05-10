package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInPolicyRulePresetsLoadFromYAML(t *testing.T) {
	presets := BuiltInPolicyRulePresets()
	if len(presets) < 6 {
		t.Fatalf("expected built-in policy rule presets, got %d", len(presets))
	}
	risk, ok := BuiltInPolicyRulePresetByName("risk-threshold")
	if !ok {
		t.Fatal("expected risk-threshold preset")
	}
	if risk.DefaultName != "high-risk-gate" || risk.Operator != "risk_at_least" {
		t.Fatalf("unexpected risk threshold preset: %#v", risk)
	}
	if len(risk.Params) == 0 || len(risk.Defaults) == 0 {
		t.Fatalf("expected Studio guidance fields, got %#v", risk)
	}
}

func TestPolicyRulePresetsFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
presets:
  - name: risk-threshold
    default_name: custom-risk-gate
    title: Custom Risk Gate
    description: override
    category: custom
    operator: risk_at_least
    reason: custom risk threshold failed
    defaults:
      ref: stages.security.outputs.risk
      minimum: critical
  - name: release-window
    default_name: release-window-gate
    title: Release Window Gate
    description: Require release-window approval.
    category: operations
    operator: ref_truthy
    defaults:
      ref: stages.ops.outputs.release_window_open
`), 0o644); err != nil {
		t.Fatalf("write custom policy preset: %v", err)
	}
	presets, err := PolicyRulePresetsFromDirs(dir)
	if err != nil {
		t.Fatalf("PolicyRulePresetsFromDirs: %v", err)
	}
	var risk, release PolicyRulePreset
	for _, preset := range presets {
		switch preset.Name {
		case "risk-threshold":
			risk = preset
		case "release-window":
			release = preset
		}
	}
	if risk.Title != "Custom Risk Gate" || risk.DefaultName != "custom-risk-gate" || risk.Defaults["minimum"] != "critical" {
		t.Fatalf("expected custom risk override, got %#v", risk)
	}
	if release.Name != "release-window" || release.Operator != "ref_truthy" {
		t.Fatalf("expected custom release-window preset, got %#v", release)
	}
}
