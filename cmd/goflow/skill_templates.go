package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
)

type skillTemplateDefinition struct {
	Name             string
	Description      string
	Mode             string
	PreferredAgent   string
	AllowedToolKinds []string
	OutputKind       string
	NextSkills       []string
	Tools            []skillTemplateTool
	Keywords         []string
	ExampleRequest   string
	Body             string
}

type skillTemplateTool struct {
	Name     string
	Required bool
}

var skillTemplates = map[string]skillTemplateDefinition{
	"code-writing": {
		Name:             "code-writing",
		Description:      "Implement, refactor, or extend workspace code with scoped edits and verification.",
		Mode:             "fix",
		PreferredAgent:   "fixer",
		AllowedToolKinds: []string{"read", "write", "exec"},
		OutputKind:       "changes",
		NextSkills:       []string{"code-audit"},
		Tools: []skillTemplateTool{
			{Name: "file_tools/list_tree"},
			{Name: "file_tools/search_files"},
			{Name: "file_tools/read_file", Required: true},
			{Name: "file_tools/write_file", Required: true},
		},
		Keywords:       []string{"implement", "fix", "refactor", "write code", "add feature", "实现", "修复", "重构"},
		ExampleRequest: `Add request validation to the config loader and run the focused tests.`,
		Body: `## Role

You are a coding specialist for scoped workspace changes.

## Workflow

1. Inspect the smallest useful set of files before editing.
2. State the concrete change plan when the task is non-trivial.
3. Modify only files needed for the requested behavior.
4. Run focused verification when available.
5. Summarize changed files, verification, and remaining risk.

## Rules

- Keep reads and writes inside the workspace.
- Prefer existing project style and local helpers.
- Do not claim tests passed unless a tool result confirms it.`,
	},
	"code-audit": {
		Name:             "code-audit",
		Description:      "Review source code for security, correctness, and maintainability risks.",
		Mode:             "audit",
		PreferredAgent:   "auditor",
		AllowedToolKinds: []string{"read"},
		OutputKind:       "findings",
		Tools: []skillTemplateTool{
			{Name: "file_tools/list_tree"},
			{Name: "file_tools/search_files"},
			{Name: "file_tools/read_file", Required: true},
		},
		Keywords:       []string{"audit", "review", "security review", "代码审计", "漏洞", "风险"},
		ExampleRequest: `Review the authentication changes for correctness and security regressions.`,
		Body: `## Role

You are a code auditor. Prioritize bugs, exploitability, regressions, and missing verification.

## Workflow

1. Identify the requested review scope.
2. Inspect relevant source and tests.
3. Report findings first, ordered by severity.
4. Include file paths, evidence, and concrete remediation guidance.

## Output

- Findings first.
- Then open questions or assumptions.
- Then brief test gaps or residual risk.`,
	},
	"web-vulnerability-research": {
		Name:             "web-vulnerability-research",
		Description:      "Collect web page source and linked assets for defensive vulnerability analysis.",
		Mode:             "audit",
		PreferredAgent:   "auditor",
		AllowedToolKinds: []string{"read", "network"},
		OutputKind:       "security-findings",
		Tools: []skillTemplateTool{
			{Name: "web_tools/fetch_page_assets", Required: true},
			{Name: "web_tools/fetch_url"},
			{Name: "web_tools/web_search"},
		},
		Keywords:       []string{"web vulnerability", "web漏洞", "网站漏洞", "xss", "csrf", "前端漏洞"},
		ExampleRequest: `Assess https://example.com/login for client-side vulnerability indicators from page source and linked assets.`,
		Body: `## Role

You perform defensive web vulnerability research from fetched source and assets.

## Workflow

1. Fetch the target page and linked JavaScript/CSS assets.
2. Identify exposed routes, client-side trust boundaries, tokens, sinks, and risky patterns.
3. Separate confirmed evidence from hypotheses.
4. Avoid intrusive scanning unless the operator explicitly provides authorization and tools.

## Output

- Target and collected assets.
- Findings with evidence snippets and affected assets.
- Exploitability assumptions.
- Defensive remediation steps.`,
	},
	"binary-vulnerability-research": {
		Name:             "binary-vulnerability-research",
		Description:      "Perform static binary triage for defensive vulnerability research.",
		Mode:             "audit",
		PreferredAgent:   "auditor",
		AllowedToolKinds: []string{"read"},
		OutputKind:       "security-findings",
		Tools: []skillTemplateTool{
			{Name: "python_notes/binary_file_info", Required: true},
			{Name: "python_notes/binary_strings", Required: true},
			{Name: "python_notes/hex_preview"},
		},
		Keywords:       []string{"binary vulnerability", "binary audit", "reverse", "\u4e8c\u8fdb\u5236\u6f0f\u6d1e", "\u9006\u5411\u6f0f\u6d1e", "\u56fa\u4ef6\u6f0f\u6d1e"},
		ExampleRequest: `Triage @bin/sample.exe for suspicious strings, metadata, and static vulnerability indicators.`,
		Body: `## Role

You perform static binary triage for defensive vulnerability research.

## Workflow

1. Identify file type, size, hashes, and obvious packing/encryption signs.
2. Extract strings and inspect suspicious imports, paths, commands, URLs, and format strings.
3. Use hex previews only for targeted evidence.
4. State when deeper disassembly, debugging, or fuzzing is required.

## Output

- Binary metadata.
- Suspicious indicators.
- Potential vulnerability hypotheses with confidence.
- Recommended next analysis steps.`,
	},
	"execution-plan": {
		Name:             "execution-plan",
		Description:      "Turn a broad request into an ordered, verifiable execution plan.",
		Mode:             "plan",
		PreferredAgent:   "planner",
		AllowedToolKinds: []string{"read", "network"},
		OutputKind:       "plan",
		NextSkills:       []string{"code-writing", "code-audit"},
		Tools: []skillTemplateTool{
			{Name: "file_tools/list_tree"},
			{Name: "file_tools/search_files"},
			{Name: "file_tools/read_file"},
			{Name: "web_tools/web_search"},
		},
		Keywords:       []string{"plan", "roadmap", "execution plan", "\u5236\u5b9a\u8ba1\u5212", "\u6267\u884c\u8ba1\u5212", "\u8ba1\u5212", "\u65b9\u6848"},
		ExampleRequest: `Compare the current code against MASTER-PLAN.md and propose the next executable task.`,
		Body: `## Role

You create execution plans that another agent can implement.

## Workflow

1. Clarify scope from available project context.
2. Split the work into ordered phases with verification gates.
3. Identify dependencies, risks, and decisions.
4. Keep tasks concrete enough for a fixer or auditor to execute.

## Output

- Current state.
- Remaining work.
- Next task.
- Verification checklist.`,
	},
}

func handleSkillTemplatesCommand() bool {
	names := skillTemplateNames()
	rows := make([]skillTemplateDisplayRow, 0, len(names))
	for _, name := range names {
		template := skillTemplates[name]
		rows = append(rows, skillTemplateDisplayRow{
			Name:           template.Name,
			Description:    template.Description,
			Mode:           template.Mode,
			PreferredAgent: template.PreferredAgent,
			OutputKind:     template.OutputKind,
		})
	}
	fmt.Print(formatSkillTemplatesOutput(rows))
	return true
}

func handleNewSkillCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /new-skill <template> <name>")
		return true
	}
	if skillManager == nil {
		fmt.Fprintln(os.Stderr, "new skill error: skill manager is unavailable")
		return true
	}
	templateName := strings.ToLower(strings.TrimSpace(fields[1]))
	template, ok := skillTemplates[templateName]
	if !ok {
		fmt.Fprintf(os.Stderr, "new skill error: unknown template %q\n", fields[1])
		fmt.Print(formatSkillTemplateNames())
		return true
	}
	skillName, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new skill error: %v\n", err)
		return true
	}
	path, err := createSkillFromTemplate(skillManager.Root(), skillName, template)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new skill error: %v\n", err)
		return true
	}
	if _, err := skillpkg.ParseFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "new skill validation error: %v\n", err)
		return true
	}
	if err := skillManager.Reload(); err != nil {
		fmt.Fprintf(os.Stderr, "reload skills error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("skill", fmt.Sprintf("created %s from %s at %s", skillName, templateName, path)))
	return true
}

type skillTemplateDisplayRow struct {
	Name           string
	Description    string
	Mode           string
	PreferredAgent string
	OutputKind     string
}

func formatSkillTemplatesOutput(rows []skillTemplateDisplayRow) string {
	var b strings.Builder
	b.WriteString(styleHeader("Skill templates"))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(styleMuted("  none available\n"))
		return b.String()
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "- %s", styleStatus(row.Name, "ready"))
		if row.Mode != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("mode"), row.Mode)
		}
		if row.PreferredAgent != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("agent"), row.PreferredAgent)
		}
		if row.OutputKind != "" {
			fmt.Fprintf(&b, "  %s=%s", styleLabel("output"), row.OutputKind)
		}
		b.WriteString("\n")
		if row.Description != "" {
			fmt.Fprintf(&b, "  %s\n", row.Description)
		}
	}
	return b.String()
}

func formatSkillTemplateNames() string {
	return fmt.Sprintf("available templates: %s\n", strings.Join(skillTemplateNames(), ", "))
}

func skillTemplateNames() []string {
	names := make([]string, 0, len(skillTemplates))
	for name := range skillTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeNewSkillName(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("skill name is required")
	}
	var b strings.Builder
	lastDash := false
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			return "", fmt.Errorf("skill name %q contains unsupported character %q; use letters, numbers, spaces, hyphen, or underscore", input, r)
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "", fmt.Errorf("skill name %q does not contain letters or numbers", input)
	}
	return name, nil
}

func createSkillFromTemplate(root, skillName string, template skillTemplateDefinition) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("skill root is empty")
	}
	dir := filepath.Join(root, skillName)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("skill already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	content := renderSkillTemplate(skillName, template)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func renderSkillTemplate(skillName string, template skillTemplateDefinition) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", skillName)
	fmt.Fprintf(&b, "description: %s\n", template.Description)
	b.WriteString("version: 0.1.0\n")
	b.WriteString("author: GoFlow\n")
	if len(template.Tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range template.Tools {
			fmt.Fprintf(&b, "  - name: %s\n", tool.Name)
			fmt.Fprintf(&b, "    required: %t\n", tool.Required)
		}
	}
	b.WriteString("activation:\n")
	fmt.Fprintf(&b, "  keywords: [%s]\n", quotedYAMLList(append([]string{skillName}, template.Keywords...)))
	if template.Mode != "" {
		fmt.Fprintf(&b, "mode: %s\n", template.Mode)
	}
	if template.PreferredAgent != "" {
		fmt.Fprintf(&b, "preferred_agent: %s\n", template.PreferredAgent)
	}
	if len(template.AllowedToolKinds) > 0 {
		fmt.Fprintf(&b, "allowed_tool_kinds: [%s]\n", strings.Join(template.AllowedToolKinds, ", "))
	}
	if template.OutputKind != "" {
		fmt.Fprintf(&b, "output_kind: %s\n", template.OutputKind)
	}
	if len(template.NextSkills) > 0 {
		fmt.Fprintf(&b, "next_skills: [%s]\n", strings.Join(template.NextSkills, ", "))
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(template.Body))
	b.WriteString("\n")
	if template.ExampleRequest != "" {
		b.WriteString("\n## Example Request\n\n")
		fmt.Fprintf(&b, "> %s\n", template.ExampleRequest)
	}
	b.WriteString("\n## Extension Points\n\n")
	b.WriteString("- Adjust `activation.keywords` to match real user phrasing.\n")
	b.WriteString("- Keep `preferred_agent` aligned with the permission boundary this skill needs.\n")
	b.WriteString("- Keep `tools` limited to the evidence or actions the workflow actually requires.\n")
	b.WriteString("- Add `next_skills` only when a downstream stage should naturally follow this skill.\n")
	return b.String()
}

func quotedYAMLList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		quoted = append(quoted, `"`+value+`"`)
	}
	return strings.Join(quoted, ", ")
}
