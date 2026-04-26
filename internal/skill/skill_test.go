package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestParseFileValidatesFolderAndMetadata(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "code_audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := `---
name: code_audit
description: Audit source code for security risks.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
activation:
  keywords: ["audit"]
  embedding_description: audit source code
---

## Workflow

1. Inspect the target.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skill, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "code_audit" {
		t.Fatalf("unexpected skill name: %s", skill.Name)
	}
}

func TestParseFileAcceptsHyphenatedSkillNameWhenFolderMatches(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "code-audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := `---
name: code-audit
description: Audit source code for security risks.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
activation:
  keywords: ["audit"]
  embedding_description: audit source code
---

## Workflow

1. Inspect the target.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skill, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "code-audit" {
		t.Fatalf("unexpected skill name: %s", skill.Name)
	}
}

func TestParseFileParsesExtendedMetadata(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "code-audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := `---
name: code-audit
description: Audit source code for security risks.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
activation:
  keywords: ["audit"]
  embedding_description: audit source code
mode: audit
preferred_agent: auditor
allowed_tool_kinds: [read, unknown]
output_kind: findings
priority: 7
max_iterations: 4
next_skills: [fix]
metadata:
  team: security
---

## Workflow

1. Inspect the target.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skill, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Mode != "audit" {
		t.Fatalf("unexpected mode: %s", skill.Mode)
	}
	if skill.PreferredAgent != "auditor" {
		t.Fatalf("unexpected preferred agent: %s", skill.PreferredAgent)
	}
	if skill.OutputKind != "findings" {
		t.Fatalf("unexpected output kind: %s", skill.OutputKind)
	}
	if len(skill.AllowedToolKinds) != 2 || skill.AllowedToolKinds[0] != "read" || skill.AllowedToolKinds[1] != "unknown" {
		t.Fatalf("unexpected allowed tool kinds: %#v", skill.AllowedToolKinds)
	}
	if skill.Priority != 7 {
		t.Fatalf("unexpected priority: %d", skill.Priority)
	}
	if skill.MaxIterations != 4 {
		t.Fatalf("unexpected max iterations: %d", skill.MaxIterations)
	}
	if len(skill.NextSkills) != 1 || skill.NextSkills[0] != "fix" {
		t.Fatalf("unexpected next skills: %#v", skill.NextSkills)
	}
	if skill.Metadata["team"] != "security" {
		t.Fatalf("unexpected metadata: %#v", skill.Metadata)
	}
}

func TestMatchUsesKeywordsNameAndDescription(t *testing.T) {
	skills := []schema.Skill{
		{Name: "code_audit", Description: "Audit source code for risks", Activation: schema.Activation{Keywords: []string{"audit", "审计"}}},
		{Name: "reverse_engineering", Description: "Reverse engineer binaries", Activation: schema.Activation{Keywords: []string{"reverse", "逆向"}}},
	}

	matched, ok := Match("请帮我做代码审计", skills)
	if !ok {
		t.Fatal("expected a skill match")
	}
	if matched.Name != "code_audit" {
		t.Fatalf("unexpected match: %s", matched.Name)
	}
}

func TestMatchBreaksTiesBySkillName(t *testing.T) {
	skills := []schema.Skill{
		{Name: "zeta", Description: "General audit helper", Activation: schema.Activation{Keywords: []string{"inspect"}}},
		{Name: "alpha", Description: "General audit helper", Activation: schema.Activation{Keywords: []string{"inspect"}}},
	}

	matched, ok := Match("inspect this code", skills)
	if !ok {
		t.Fatal("expected a skill match")
	}
	if matched.Name != "alpha" {
		t.Fatalf("expected deterministic tie-break to pick alpha, got %s", matched.Name)
	}
}

func TestMatchWithDiagnosticsExplainsSelectedSkill(t *testing.T) {
	skills := []schema.Skill{
		{Name: "code-audit", Description: "Review source code", Activation: schema.Activation{Keywords: []string{"audit", "security review"}}},
	}

	matched, diagnostic, ok := MatchWithDiagnostics("please audit this code", skills)
	if !ok {
		t.Fatal("expected a skill match")
	}
	if matched.Name != "code-audit" || diagnostic.SkillName != "code-audit" {
		t.Fatalf("unexpected diagnostic match: skill=%#v diagnostic=%#v", matched, diagnostic)
	}
	if diagnostic.Score <= 0 || len(diagnostic.KeywordHits) != 1 || diagnostic.KeywordHits[0] != "audit" {
		t.Fatalf("expected keyword score diagnostic, got %#v", diagnostic)
	}
	if !strings.Contains(diagnostic.Reason, "keywords:audit") {
		t.Fatalf("expected keyword reason, got %q", diagnostic.Reason)
	}
}

func TestBuiltInSecuritySkillsLoadAndMatch(t *testing.T) {
	manager, err := NewManager(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatalf("load built-in skills: %v", err)
	}

	webSkill, ok := manager.Match("�?https://example.com �?web漏洞 挖掘，重点看前端漏洞")
	if !ok || webSkill.Name != "web-vulnerability-research" {
		t.Fatalf("expected web vulnerability skill, got ok=%t skill=%#v", ok, webSkill)
	}

	binarySkill, ok := manager.Match("分析这个固件里的二进制漏洞和系统软件漏洞")
	if !ok || binarySkill.Name != "binary-vulnerability-research" {
		t.Fatalf("expected binary vulnerability skill, got ok=%t skill=%#v", ok, binarySkill)
	}
}

func TestBuiltInSecuritySkillsMatchEnglishAndDeclareDiagnostics(t *testing.T) {
	manager, err := NewManager(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatalf("load built-in skills: %v", err)
	}

	tests := []struct {
		name         string
		query        string
		wantSkill    string
		wantKeywords []string
	}{
		{
			name:         "web asset security",
			query:        "fetch https://example.com and inspect loaded JavaScript for XSS, CSRF, and client-side security issues",
			wantSkill:    "web-vulnerability-research",
			wantKeywords: []string{"xss", "csrf", "client-side security"},
		},
		{
			name:         "binary memory safety",
			query:        "triage this firmware binary for memory corruption and binary vulnerability signals",
			wantSkill:    "binary-vulnerability-research",
			wantKeywords: []string{"binary vulnerability", "memory corruption"},
		},
		{
			name:         "generic dependency vulnerability",
			query:        "investigate CVE exploitability and attack surface in this dependency",
			wantSkill:    "vulnerability-research",
			wantKeywords: []string{"exploitability", "cve", "attack surface"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, diagnostic, ok := manager.MatchWithDiagnostics(tt.query)
			if !ok || matched.Name != tt.wantSkill {
				t.Fatalf("expected %s, got ok=%t skill=%#v diagnostic=%#v", tt.wantSkill, ok, matched, diagnostic)
			}
			for _, keyword := range tt.wantKeywords {
				if !stringSliceContains(diagnostic.KeywordHits, keyword) {
					t.Fatalf("expected keyword hit %q in %#v", keyword, diagnostic.KeywordHits)
				}
			}
			if diagnostic.Score <= 0 || !strings.Contains(diagnostic.Reason, "keywords:") {
				t.Fatalf("expected positive keyword diagnostic, got %#v", diagnostic)
			}
		})
	}
}

func TestBuiltInWebAndBinarySecuritySkillsDeclareExpectedTools(t *testing.T) {
	manager, err := NewManager(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatalf("load built-in skills: %v", err)
	}

	webSkill := requireBuiltInSkill(t, manager, "web-vulnerability-research")
	if webSkill.Mode != "audit" || webSkill.PreferredAgent != "auditor" || webSkill.OutputKind != "findings" {
		t.Fatalf("unexpected web skill execution metadata: %#v", webSkill)
	}
	if !stringSliceContains(webSkill.AllowedToolKinds, "network") || stringSliceContains(webSkill.AllowedToolKinds, "write") {
		t.Fatalf("expected web skill to be network-only for tool kinds, got %#v", webSkill.AllowedToolKinds)
	}
	requireSkillTool(t, webSkill, "web_tools/fetch_page_assets", true)
	requireSkillTool(t, webSkill, "web_tools/fetch_url", false)
	requireSkillTool(t, webSkill, "web_tools/web_search", false)

	binarySkill := requireBuiltInSkill(t, manager, "binary-vulnerability-research")
	if binarySkill.Mode != "audit" || binarySkill.PreferredAgent != "auditor" || binarySkill.OutputKind != "findings" {
		t.Fatalf("unexpected binary skill execution metadata: %#v", binarySkill)
	}
	if !stringSliceContains(binarySkill.AllowedToolKinds, "read") || stringSliceContains(binarySkill.AllowedToolKinds, "write") || stringSliceContains(binarySkill.AllowedToolKinds, "network") {
		t.Fatalf("expected binary skill to stay static/read oriented, got %#v", binarySkill.AllowedToolKinds)
	}
	requireSkillTool(t, binarySkill, "python_notes/binary_file_info", true)
	requireSkillTool(t, binarySkill, "python_notes/binary_strings", true)
	requireSkillTool(t, binarySkill, "python_notes/hex_preview", false)
	requireSkillTool(t, binarySkill, "file_tools/file_info", false)
}

func requireBuiltInSkill(t *testing.T, manager *Manager, name string) schema.Skill {
	t.Helper()
	for _, skill := range manager.List() {
		if skill.Name == name {
			return skill
		}
	}
	t.Fatalf("built-in skill %s not found", name)
	return schema.Skill{}
}

func requireSkillTool(t *testing.T, skill schema.Skill, name string, required bool) {
	t.Helper()
	for _, tool := range skill.Tools {
		if tool.Name == name {
			if tool.Required != required {
				t.Fatalf("skill %s tool %s required=%t, want %t", skill.Name, name, tool.Required, required)
			}
			return
		}
	}
	t.Fatalf("skill %s missing tool %s; tools=%#v", skill.Name, name, skill.Tools)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
