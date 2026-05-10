package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInSkillTemplatesLoadFromYAML(t *testing.T) {
	templates := BuiltInSkillTemplates()
	if len(templates) < 5 {
		t.Fatalf("expected built-in skill templates, got %d", len(templates))
	}
	audit, ok := BuiltInSkillTemplateByName("code-audit")
	if !ok {
		t.Fatal("expected code-audit template")
	}
	if audit.Mode != "audit" || audit.PreferredAgent != "auditor" || audit.OutputKind != "findings" {
		t.Fatalf("unexpected code-audit template: %#v", audit)
	}
	if len(audit.Tools) == 0 || audit.Body == "" {
		t.Fatalf("expected tool declarations and body, got %#v", audit)
	}
}

func TestSkillTemplatesFromDirsAddsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
templates:
  - name: code-audit
    description: Custom code audit template.
    mode: audit
    preferred_agent: security-reviewer
    output_kind: custom-findings
    keywords: [custom audit]
    body: |
      ## Role

      Custom audit role.
  - name: data-analysis
    description: Analyze CSV or structured data.
    mode: audit
    preferred_agent: analyst
    allowed_tool_kinds: [read, exec]
    output_kind: report
    body: |
      ## Role

      Analyze structured data and produce a report.
`), 0o644); err != nil {
		t.Fatalf("write custom skill template: %v", err)
	}
	templates, err := SkillTemplatesFromDirs(dir)
	if err != nil {
		t.Fatalf("SkillTemplatesFromDirs: %v", err)
	}
	var audit, data SkillTemplate
	for _, template := range templates {
		switch template.Name {
		case "code-audit":
			audit = template
		case "data-analysis":
			data = template
		}
	}
	if audit.Description != "Custom code audit template." || audit.PreferredAgent != "security-reviewer" || audit.OutputKind != "custom-findings" {
		t.Fatalf("expected custom audit override, got %#v", audit)
	}
	if data.Name != "data-analysis" || data.Mode != "audit" || data.Body == "" {
		t.Fatalf("expected custom data-analysis template, got %#v", data)
	}
}
