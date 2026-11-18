package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
)

type skillTemplateDefinition = scaffold.SkillTemplate
type skillTemplateTool = scaffold.SkillTemplateTool

func handleSkillTemplatesCommand(skillManager *skillpkg.Manager) bool {
	templates, err := skillTemplatesForSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill templates error: %v\n", err)
		templates = scaffold.BuiltInSkillTemplates()
	}
	names := skillTemplateNamesFromList(templates)
	rows := make([]skillTemplateDisplayRow, 0, len(names))
	for _, name := range names {
		template, _ := skillTemplateByNameFromList(templates, name)
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
	templates, err := skillTemplatesForSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new skill error: %v\n", err)
		return true
	}
	templateName := strings.ToLower(strings.TrimSpace(fields[1]))
	template, ok := skillTemplateByNameFromList(templates, templateName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new skill error: unknown template %q\n", fields[1])
		fmt.Print(formatSkillTemplateNamesFromList(templates))
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
	return formatSkillTemplateNamesFromList(scaffold.BuiltInSkillTemplates())
}

func formatSkillTemplateNamesFromList(templates []skillTemplateDefinition) string {
	return fmt.Sprintf("available templates: %s\n", strings.Join(skillTemplateNamesFromList(templates), ", "))
}

func skillTemplateNames() []string {
	return skillTemplateNamesFromList(scaffold.BuiltInSkillTemplates())
}

func skillTemplateNamesFromList(templates []skillTemplateDefinition) []string {
	names := make([]string, 0, len(templates))
	for _, template := range templates {
		if strings.TrimSpace(template.Name) != "" {
			names = append(names, template.Name)
		}
	}
	sort.Strings(names)
	return names
}

func skillTemplateByNameFromList(templates []skillTemplateDefinition, name string) (skillTemplateDefinition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, template := range templates {
		if strings.EqualFold(template.Name, name) {
			return template, true
		}
	}
	return skillTemplateDefinition{}, false
}

func skillTemplatesForSkillManager(skillManager *skillpkg.Manager) ([]skillTemplateDefinition, error) {
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		return scaffold.BuiltInSkillTemplates(), nil
	}
	return scaffold.SkillTemplatesFromDirs(filepath.Join(root, "templates", "skills", "scaffolds"))
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
