package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentpkg "github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"gopkg.in/yaml.v3"
)

func handleNewToolCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /new-tool python <name>")
		return true
	}
	language := strings.ToLower(strings.TrimSpace(fields[1]))
	if language != "python" {
		fmt.Fprintf(os.Stderr, "new tool error: unsupported language %q; currently supported: python\n", fields[1])
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "mcp_servers", name+".py")
	configPath := filepath.Join(root, "configs", "mcp_servers", name+".yaml")
	content, err := scaffold.RenderPythonMCPToolCode(root, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	if err := validatePythonMCPScaffold(content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool validation error: %v\n", err)
		return true
	}
	configContent, err := scaffold.RenderPythonMCPServerConfig(root, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	if err := writeNewFile(configPath, configContent); err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("tool", fmt.Sprintf("created validated python MCP scaffold at %s", path)))
	fmt.Println(formatCommandSuccess("tool", fmt.Sprintf("created modular MCP config at %s", configPath)))
	fmt.Println(styleMuted("  next: review tool permissions, restart GoFlow or run /reload-tools after rebootstrap, then run /tools"))
	return true
}

func handleNewAgentCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-agent <name>")
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[1:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "configs", "agents", name+".yaml")
	content, err := scaffold.RenderAgentConfigTemplate(root, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	if err := validateAgentScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new agent validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("agent", fmt.Sprintf("created validated config snippet at %s", path)))
	fmt.Println(styleMuted("  next: review permissions, restart GoFlow so configs/agents/*.yaml is loaded, then run /agents"))
	return true
}

func handleNewProviderCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-provider <name>")
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[1:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "configs", "providers", name+".yaml")
	content, err := scaffold.RenderProviderConfigTemplate(root, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	if err := validateProviderScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new provider validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("provider", fmt.Sprintf("created validated config snippet at %s", path)))
	fmt.Println(styleMuted("  next: fill base_url/api_key/model, restart GoFlow so configs/providers/*.yaml is loaded, then assign agents to this provider"))
	return true
}

func handleNewKitCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-kit <preset> <name> [--materialize|--full]\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(kitScaffoldPresetNames(), ", "))
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	presets, err := kitScaffoldPresetsFromRuntimeHome(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := kitScaffoldPresetByNameFromList(presets, presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new kit error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(kitScaffoldPresetNamesFromList(presets), ", "))
		return true
	}
	materialize := false
	nameFields := make([]string, 0, len(fields)-2)
	for _, field := range fields[2:] {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "--materialize", "--full":
			materialize = true
		default:
			nameFields = append(nameFields, field)
		}
	}
	name, err := normalizeNewSkillName(strings.Join(nameFields, " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	if materialize {
		if err := createMaterializedKitScaffold(root, name, preset); err != nil {
			fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
			return true
		}
		return true
	}
	path := filepath.Join(root, "kits", name, "kit.yaml")
	content := renderKitYAMLTemplate(name, preset)
	if err := validateKitScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new kit validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("kit", fmt.Sprintf("created %s kit manifest at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: review referenced agents/skills/workflows, then open Studio resources or commit the kit.yaml"))
	return true
}

func handleNewPolicyRuleCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-policy-rule <preset> <name>\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(policyRuleScaffoldPresetNamesForSkillManager(skillManager), ", "))
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	presets, err := policyRuleScaffoldPresetsFromRuntimeHome(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := policyRuleScaffoldPresetByNameFromList(presets, presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new policy rule error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(policyRuleScaffoldPresetNamesFromList(presets), ", "))
		return true
	}
	path := filepath.Join(root, "policies", "workflow_rules", name+".yaml")
	content, err := renderPolicyRuleYAMLTemplate(name, preset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	if err := validatePolicyRuleScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("policy-rule", fmt.Sprintf("created %s rule at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: use /policy-rules to confirm it is loaded, then select it from workflow policy_guard nodes"))
	return true
}

func handleNewTeamCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-team <preset> <name>\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(teamScaffoldPresetNamesForSkillManager(skillManager), ", "))
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	presets, err := teamScaffoldPresetsFromRuntimeHome(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := teamScaffoldPresetByNameFromList(presets, presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new team error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(teamScaffoldPresetNamesFromList(presets), ", "))
		return true
	}
	path := filepath.Join(root, "templates", "teams", name+".yaml")
	content, err := renderTeamTemplateYAMLTemplate(name, preset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	if err := validateTeamTemplateScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new team validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("team", fmt.Sprintf("created %s team template at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: run /teams to inspect it, then reference it from workflow team nodes"))
	return true
}

func handleNewWorkflowCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-workflow <name> [--template <template>]")
		return true
	}
	nameInput, templateName, err := parseNewWorkflowArgs(fields[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	name, err := normalizeNewSkillName(nameInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	dir := filepath.Join(root, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	yamlPath := filepath.Join(dir, "workflow.yaml")
	docPath := filepath.Join(dir, "WORKFLOW.md")
	yamlContent, err := renderWorkflowYAMLTemplate(name, templateName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	if err := validateWorkflowScaffold(name, yamlContent); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(yamlPath, yamlContent); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	if err := writeNewFile(docPath, renderWorkflowMarkdownTemplate(name)); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("workflow", fmt.Sprintf("created blueprint at %s", dir)))
	fmt.Println(formatCommandSuccess("workflow", fmt.Sprintf("template=%s", templateName)))
	fmt.Println(styleMuted(fmt.Sprintf("  next: run /workflow %s <request> after adjusting stage skills and approvals", name)))
	return true
}

func handleNewWorkflowTemplateCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /new-workflow-template <source-template> <name>")
		fmt.Fprintf(os.Stderr, "available sources: %s\n", strings.Join(workflowTemplateScaffoldSourceNames(), ", "))
		return true
	}
	source := strings.TrimSpace(fields[1])
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "templates", "workflows", name+".yaml")
	content, err := renderWorkflowTemplateResourceYAML(name, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	if err := validateWorkflowTemplateScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("workflow-template", fmt.Sprintf("forked %s into %s", source, path)))
	fmt.Println(styleMuted(fmt.Sprintf("  next: run /workflow-templates %s, then create workflows with /new-workflow <name> --template %s", name, name)))
	return true
}

func parseNewWorkflowArgs(fields []string) (string, string, error) {
	templateName := "plan-fix-audit"
	nameFields := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := strings.TrimSpace(fields[i])
		switch field {
		case "--template", "-t":
			if i+1 >= len(fields) {
				return "", "", fmt.Errorf("%s requires a template name", field)
			}
			templateName = strings.TrimSpace(fields[i+1])
			i++
		default:
			nameFields = append(nameFields, field)
		}
	}
	name := strings.TrimSpace(strings.Join(nameFields, " "))
	if name == "" {
		return "", "", fmt.Errorf("workflow name is required")
	}
	return name, templateName, nil
}

func validatePythonMCPScaffold(content string) error {
	required := []string{
		"GOFLOW_WORKSPACE_ROOT",
		"tools/list",
		"tools/call",
		"additionalProperties",
		"resolve_path",
		"relative_path",
		"is_error",
		"escapes workspace root",
		"resolves outside workspace root",
		"write_text",
	}
	return requireScaffoldContent("python MCP scaffold", content, required)
}

func validateBinaryAnalysisPythonMCPScaffold(content string) error {
	required := []string{
		"GOFLOW_WORKSPACE_ROOT",
		"tools/list",
		"tools/call",
		"additionalProperties",
		"resolve_path",
		"relative_path",
		"is_error",
		"escapes workspace root",
		"resolves outside workspace root",
		"binary_file_info",
		"binary_strings",
		"hex_preview",
		"MAX_BINARY_BYTES",
	}
	return requireScaffoldContent("binary analysis python MCP scaffold", content, required)
}

func validateAgentScaffold(name, content string) error {
	required := []string{
		name + ":",
		"provider:",
		"mode:",
		"tool_policy:",
		"allowed_tool_kinds:",
		"max_iterations:",
	}
	return requireScaffoldContent("agent scaffold", content, required)
}

func validateProviderScaffold(name, content string) error {
	required := []string{
		name + ":",
		"provider:",
		"base_url:",
		"api_key:",
		"model:",
		"timeout:",
		"retry_count:",
	}
	return requireScaffoldContent("provider scaffold", content, required)
}

func validateWorkflowScaffold(name, content string) error {
	required := []string{
		"name: " + name,
		"stages:",
		"agent:",
		"skill:",
		"node_type:",
	}
	return requireScaffoldContent("workflow scaffold", content, required)
}

func validateWorkflowTemplateScaffold(name, content string) error {
	var template agentpkg.WorkflowTemplate
	if err := yaml.Unmarshal([]byte(content), &template); err != nil {
		return fmt.Errorf("parse workflow template scaffold: %w", err)
	}
	if template.Name != name {
		return fmt.Errorf("workflow template scaffold name %q does not match %q", template.Name, name)
	}
	if strings.TrimSpace(template.Graph.Name) != name {
		return fmt.Errorf("workflow template graph name %q does not match %q", template.Graph.Name, name)
	}
	if len(template.Graph.Stages) == 0 {
		return fmt.Errorf("workflow template scaffold requires at least one stage")
	}
	required := []string{
		"kind: goflow.workflow_template_resource",
		"version: 2",
		"name: " + name,
		"graph:",
		"stages:",
	}
	return requireScaffoldContent("workflow template scaffold", content, required)
}

func validateKitScaffold(name, content string) error {
	required := []string{
		"kind: goflow.kit",
		"version: 1",
		"name: " + name,
		"title:",
		"description:",
		"agents:",
		"skills:",
		"examples:",
	}
	return requireScaffoldContent("kit scaffold", content, required)
}

func validatePolicyRuleScaffold(name, content string) error {
	var definition agentpkg.WorkflowPolicyRuleDefinition
	if err := yaml.Unmarshal([]byte(content), &definition); err != nil {
		return fmt.Errorf("parse policy rule scaffold: %w", err)
	}
	if definition.Name != name {
		return fmt.Errorf("policy rule scaffold name %q does not match %q", definition.Name, name)
	}
	if err := agentpkg.ValidateWorkflowPolicyRuleDefinition(definition); err != nil {
		return err
	}
	required := []string{
		"kind: goflow.workflow_policy_rule",
		"version: 2",
		"name: " + name,
		"operator:",
	}
	return requireScaffoldContent("policy rule scaffold", content, required)
}

func validateTeamTemplateScaffold(name, content string) error {
	var template agentpkg.TeamTemplate
	if err := yaml.Unmarshal([]byte(content), &template); err != nil {
		return fmt.Errorf("parse team template scaffold: %w", err)
	}
	if template.Name != name {
		return fmt.Errorf("team template scaffold name %q does not match %q", template.Name, name)
	}
	if _, err := (&agentpkg.WorkflowRunner{}).ValidateTeamTemplateResource(name, template); err != nil {
		return err
	}
	required := []string{
		"kind: goflow.team_template_resource",
		"version: 2",
		"name: " + name,
		"role_templates:",
	}
	return requireScaffoldContent("team template scaffold", content, required)
}

func requireScaffoldContent(kind, content string, required []string) error {
	for _, needle := range required {
		if !strings.Contains(content, needle) {
			return fmt.Errorf("%s missing %q", kind, needle)
		}
	}
	return nil
}

func runtimeHomeFromSkillManager(skillManager *skillpkg.Manager) (string, error) {
	if skillManager == nil {
		return "", fmt.Errorf("skill manager is unavailable")
	}
	root := strings.TrimSpace(skillManager.Root())
	if root == "" {
		return "", fmt.Errorf("skill root is empty")
	}
	return filepath.Dir(root), nil
}

func writeNewFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

type kitScaffoldPresetDefinition struct {
	Name              string
	Title             string
	Description       string
	Category          string
	Tags              []string
	Agents            []string
	Providers         []string
	Skills            []string
	Tools             []string
	Workflows         []string
	WorkflowTemplates []string
	TeamTemplates     []string
	PolicyRules       []string
	RequiredEnv       []string
	ExampleRequest    string
	ExampleWorkflow   string
	ExampleAgent      string
}

func kitScaffoldPresetByName(name string) (kitScaffoldPresetDefinition, bool) {
	return kitScaffoldPresetByNameFromList(kitScaffoldPresets(), name)
}

func kitScaffoldPresetByNameFromList(presets []kitScaffoldPresetDefinition, name string) (kitScaffoldPresetDefinition, bool) {
	for _, preset := range presets {
		if strings.EqualFold(preset.Name, name) {
			return preset, true
		}
	}
	return kitScaffoldPresetDefinition{}, false
}

func kitScaffoldPresetNames() []string {
	return kitScaffoldPresetNamesFromList(kitScaffoldPresets())
}

func kitScaffoldPresetNamesFromList(presets []kitScaffoldPresetDefinition) []string {
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func kitScaffoldPresets() []kitScaffoldPresetDefinition {
	presets, err := kitScaffoldPresetsFromRuntimeHome("")
	if err != nil {
		panic(err)
	}
	return presets
}

func kitScaffoldPresetsFromRuntimeHome(runtimeHome string) ([]kitScaffoldPresetDefinition, error) {
	var (
		presets []scaffold.KitPreset
		err     error
	)
	if strings.TrimSpace(runtimeHome) == "" {
		presets = scaffold.BuiltInKitPresets()
	} else {
		presets, err = scaffold.KitPresetsFromDirs(filepath.Join(runtimeHome, "templates", "kits", "scaffolds"))
		if err != nil {
			return nil, err
		}
	}
	out := make([]kitScaffoldPresetDefinition, 0, len(presets))
	for _, preset := range presets {
		out = append(out, kitScaffoldPresetDefinitionFromShared(preset))
	}
	return out, nil
}

func kitScaffoldPresetDefinitionFromShared(preset scaffold.KitPreset) kitScaffoldPresetDefinition {
	exampleRequest := ""
	exampleWorkflow := ""
	exampleAgent := ""
	if len(preset.Examples) > 0 {
		exampleRequest = preset.Examples[0].Request
		exampleWorkflow = preset.Examples[0].Workflow
		exampleAgent = preset.Examples[0].Agent
	}
	if exampleWorkflow == "" {
		exampleWorkflow = preset.RecommendedWorkflow
	}
	if exampleAgent == "" {
		exampleAgent = preset.RecommendedAgent
	}
	return kitScaffoldPresetDefinition{
		Name:              preset.Name,
		Title:             preset.Title,
		Description:       preset.Description,
		Category:          preset.Category,
		Tags:              append([]string(nil), preset.Tags...),
		Agents:            append([]string(nil), preset.Agents...),
		Providers:         append([]string(nil), preset.Providers...),
		Skills:            append([]string(nil), preset.Skills...),
		Tools:             append([]string(nil), preset.Tools...),
		Workflows:         append([]string(nil), preset.Workflows...),
		WorkflowTemplates: append([]string(nil), preset.WorkflowTemplates...),
		TeamTemplates:     append([]string(nil), preset.TeamTemplates...),
		PolicyRules:       append([]string(nil), preset.PolicyRules...),
		RequiredEnv:       append([]string(nil), preset.RequiredEnv...),
		ExampleRequest:    exampleRequest,
		ExampleWorkflow:   exampleWorkflow,
		ExampleAgent:      exampleAgent,
	}
}

func renderKitYAMLTemplate(name string, preset kitScaffoldPresetDefinition) string {
	title := preset.Title
	if title == "" {
		title = strings.ReplaceAll(name, "-", " ") + " Kit"
	}
	workflow := firstNonEmptyString(preset.ExampleWorkflow, firstString(preset.Workflows), firstString(preset.WorkflowTemplates))
	agent := firstNonEmptyString(preset.ExampleAgent, firstString(preset.Agents), "chat")
	var b strings.Builder
	b.WriteString("kind: goflow.kit\n")
	b.WriteString("version: 1\n")
	b.WriteString("min_supported_version: 1\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "title: %q\n", title)
	fmt.Fprintf(&b, "description: %q\n", preset.Description)
	fmt.Fprintf(&b, "category: %s\n", firstNonEmptyString(preset.Category, "custom"))
	writeYAMLStringList(&b, "tags", preset.Tags)
	writeYAMLStringList(&b, "providers", preset.Providers)
	writeYAMLStringList(&b, "agents", preset.Agents)
	writeYAMLStringList(&b, "skills", preset.Skills)
	writeYAMLStringList(&b, "tools", preset.Tools)
	writeYAMLStringList(&b, "workflows", preset.Workflows)
	writeYAMLStringList(&b, "workflow_templates", preset.WorkflowTemplates)
	writeYAMLStringList(&b, "team_templates", preset.TeamTemplates)
	writeYAMLStringList(&b, "policy_rules", preset.PolicyRules)
	writeYAMLStringList(&b, "required_env", preset.RequiredEnv)
	b.WriteString("examples:\n")
	fmt.Fprintf(&b, "  - title: %q\n", "Try the kit")
	fmt.Fprintf(&b, "    request: %q\n", firstNonEmptyString(preset.ExampleRequest, "Describe the task this kit should handle."))
	if workflow != "" {
		fmt.Fprintf(&b, "    workflow: %s\n", workflow)
	}
	if agent != "" {
		fmt.Fprintf(&b, "    agent: %s\n", agent)
	}
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  scaffold_preset: %s\n", preset.Name)
	b.WriteString("  owner: local\n")
	return b.String()
}

type cliMaterializedKitNames struct {
	Kit              string
	Agent            string
	Skill            string
	Tool             string
	Workflow         string
	WorkflowTemplate string
	TeamTemplate     string
	PolicyRule       string
}

func createMaterializedKitScaffold(root, name string, preset kitScaffoldPresetDefinition) error {
	names := cliMaterializedKitResourceNames(name)
	toolContent, err := scaffold.RenderPythonMCPToolCode(root, names.Tool)
	if err != nil {
		return err
	}
	if preset.Name == "binary-analysis" {
		toolContent, err = scaffold.RenderBinaryAnalysisPythonMCPToolCode(root, names.Tool)
		if err != nil {
			return err
		}
	}
	kitContent, err := renderMaterializedKitYAMLTemplate(root, names, preset)
	if err != nil {
		return err
	}
	agentContent, err := renderMaterializedAgentConfigTemplate(root, names, preset)
	if err != nil {
		return err
	}
	skillContent, err := renderMaterializedSkillTemplate(root, names, preset)
	if err != nil {
		return err
	}
	workflowContent, err := renderMaterializedWorkflowYAML(root, names, preset)
	if err != nil {
		return err
	}
	workflowTemplateContent, err := renderMaterializedWorkflowTemplateYAML(root, names, preset)
	if err != nil {
		return err
	}
	teamContent, err := renderMaterializedTeamTemplateYAML(root, names, preset)
	if err != nil {
		return err
	}
	policyContent, err := renderMaterializedPolicyRuleYAML(root, names, preset)
	if err != nil {
		return err
	}
	workflowDocContent, err := renderMaterializedWorkflowMarkdown(root, names, preset)
	if err != nil {
		return err
	}
	toolConfigContent, err := renderMaterializedToolConfigYAML(root, names, preset)
	if err != nil {
		return err
	}
	files := []struct {
		kind    string
		path    string
		content string
	}{
		{"kit", filepath.Join(root, "kits", names.Kit, "kit.yaml"), kitContent},
		{"agent", filepath.Join(root, "configs", "agents", names.Agent+".yaml"), agentContent},
		{"skill", filepath.Join(root, "skills", names.Skill, "SKILL.md"), skillContent},
		{"tool", filepath.Join(root, "mcp_servers", names.Tool+".py"), toolContent},
		{"tool config", filepath.Join(root, "configs", "mcp_servers", names.Tool+".yaml"), toolConfigContent},
		{"workflow", filepath.Join(root, "workflows", names.Workflow, "workflow.yaml"), workflowContent},
		{"workflow doc", filepath.Join(root, "workflows", names.Workflow, "WORKFLOW.md"), workflowDocContent},
		{"workflow template", filepath.Join(root, "templates", "workflows", names.WorkflowTemplate+".yaml"), workflowTemplateContent},
		{"team template", filepath.Join(root, "templates", "teams", names.TeamTemplate+".yaml"), teamContent},
		{"policy rule", filepath.Join(root, "policies", "workflow_rules", names.PolicyRule+".yaml"), policyContent},
	}
	if err := validateAgentScaffold(names.Agent, files[1].content); err != nil {
		return fmt.Errorf("validate materialized agent scaffold: %w", err)
	}
	if preset.Name == "binary-analysis" {
		if err := validateBinaryAnalysisPythonMCPScaffold(files[3].content); err != nil {
			return fmt.Errorf("validate materialized tool scaffold: %w", err)
		}
	} else if err := validatePythonMCPScaffold(files[3].content); err != nil {
		return fmt.Errorf("validate materialized tool scaffold: %w", err)
	}
	if err := validateKitScaffold(names.Kit, files[0].content); err != nil {
		return fmt.Errorf("validate materialized kit scaffold: %w", err)
	}
	for _, file := range files {
		if err := writeNewFile(file.path, file.content); err != nil {
			return fmt.Errorf("create %s: %w", file.kind, err)
		}
		fmt.Println(formatCommandSuccess("kit", fmt.Sprintf("created %s at %s", file.kind, file.path)))
	}
	fmt.Println(styleMuted("  next: restart GoFlow so generated agent/tool modules are loaded, then run the generated workflow in Studio"))
	fmt.Println(styleMuted("  sandbox: generated helper uses Docker/Podman container isolation with a read-only workspace mount"))
	return nil
}

func cliMaterializedKitResourceNames(name string) cliMaterializedKitNames {
	base := normalizeResourceNameForCLI(name)
	return cliMaterializedKitNames{
		Kit:              base,
		Agent:            base + "-agent",
		Skill:            base + "-skill",
		Tool:             base + "-helper",
		Workflow:         base + "-workflow",
		WorkflowTemplate: base + "-template",
		TeamTemplate:     base + "-team",
		PolicyRule:       base + "-gate",
	}
}

func normalizeResourceNameForCLI(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}

func cliMaterializedNamesForTemplate(names cliMaterializedKitNames) scaffold.MaterializedKitNames {
	return scaffold.MaterializedKitNames{
		Kit:              names.Kit,
		Agent:            names.Agent,
		Skill:            names.Skill,
		Tool:             names.Tool,
		Workflow:         names.Workflow,
		WorkflowTemplate: names.WorkflowTemplate,
		TeamTemplate:     names.TeamTemplate,
		PolicyRule:       names.PolicyRule,
	}
}

func cliKitPresetToShared(preset kitScaffoldPresetDefinition) scaffold.KitPreset {
	return scaffold.KitPreset{
		Name:              preset.Name,
		Title:             firstNonEmptyString(preset.Title, strings.ReplaceAll(preset.Name, "-", " ")+" Kit"),
		Description:       firstNonEmptyString(preset.Description, "Linked materialized starter kit."),
		Category:          firstNonEmptyString(preset.Category, "custom"),
		Tags:              append(append([]string(nil), preset.Tags...), "materialized", "starter", "linked"),
		Providers:         append([]string(nil), preset.Providers...),
		Agents:            append([]string(nil), preset.Agents...),
		Skills:            append([]string(nil), preset.Skills...),
		Tools:             append([]string(nil), preset.Tools...),
		Workflows:         append([]string(nil), preset.Workflows...),
		WorkflowTemplates: append([]string(nil), preset.WorkflowTemplates...),
		TeamTemplates:     append([]string(nil), preset.TeamTemplates...),
		PolicyRules:       append([]string(nil), preset.PolicyRules...),
		RequiredEnv:       append([]string(nil), preset.RequiredEnv...),
		Examples: []scaffold.KitExample{{
			Title:    "Run the linked starter workflow",
			Request:  firstNonEmptyString(preset.ExampleRequest, "Describe the task, scope, expected output, and acceptance criteria."),
			Workflow: firstNonEmptyString(preset.ExampleWorkflow, firstString(preset.Workflows), firstString(preset.WorkflowTemplates)),
			Agent:    firstNonEmptyString(preset.ExampleAgent, firstString(preset.Agents), "chat"),
		}},
	}
}

func renderMaterializedKitYAMLTemplate(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedKitYAML(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedAgentConfigTemplate(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedAgentConfig(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedSkillTemplate(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedSkillMarkdown(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedToolConfigYAML(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedToolConfig(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedWorkflowYAML(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedWorkflowYAML(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedWorkflowTemplateYAML(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedWorkflowTemplateYAML(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func materializedWorkflowGraph(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition, graphName string) agentpkg.WorkflowGraphDocument {
	title := firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit")
	return agentpkg.WorkflowGraphDocument{
		Name:        graphName,
		Description: "Linked starter workflow. Earlier node outputs are passed into later node inputs.",
		Stages: []agentpkg.WorkflowGraphStageDocument{
			{Name: "start", NodeType: "start", Next: []string{"team"}, Position: agentpkg.WorkflowGraphPosition{X: 80, Y: 260}},
			{Name: "team", NodeType: "team", Agent: names.Agent, Params: map[string]string{"team": names.TeamTemplate, "execute": "false"}, Outputs: map[string]string{"team": "result.structured", "roles": "result.roles"}, Next: []string{"plan"}, Position: agentpkg.WorkflowGraphPosition{X: 340, Y: 260}},
			{Name: "plan", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "params.request", "scope": "params.scope", "team": "stages.team.outputs.team"}, Outputs: map[string]string{"summary": "result.summary", "plan": "result.output", "findings": "result.findings"}, Artifacts: []agentpkg.WorkflowGraphArtifactDocument{{Name: "starter-plan", Kind: "plan", Ref: "result.output", Title: title + " Plan"}}, Next: []string{"gate"}, Position: agentpkg.WorkflowGraphPosition{X: 620, Y: 260}},
			{Name: "gate", NodeType: "policy_guard", Params: map[string]string{"rule": names.PolicyRule, "ref": "stages.plan.outputs.summary", "reason": "The planning stage did not produce a usable handoff summary."}, Routes: map[string]string{"allow": "report", "deny": "revise"}, Position: agentpkg.WorkflowGraphPosition{X: 900, Y: 260}},
			{Name: "report", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "Build the final handoff from the approved plan.", "upstream": "stages.plan.outputs.plan"}, Outputs: map[string]string{"final_report": "result.output", "summary": "result.summary"}, Artifacts: []agentpkg.WorkflowGraphArtifactDocument{{Name: "starter-final-report", Kind: "report", Ref: "result.output", Title: title + " Final Report"}}, Next: []string{"end"}, Position: agentpkg.WorkflowGraphPosition{X: 1190, Y: 180}},
			{Name: "revise", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "Revise the workflow handoff because the policy gate denied the previous output.", "upstream": "stages.gate.outputs.reason"}, Outputs: map[string]string{"revision_plan": "result.output", "summary": "result.summary"}, Next: []string{"end"}, Position: agentpkg.WorkflowGraphPosition{X: 1190, Y: 360}},
			{Name: "end", NodeType: "end", Position: agentpkg.WorkflowGraphPosition{X: 1500, Y: 260}},
		},
	}
}

func renderMaterializedTeamTemplateYAML(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedTeamTemplateYAML(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedPolicyRuleYAML(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedPolicyRuleYAML(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

func renderMaterializedWorkflowMarkdown(root string, names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) (string, error) {
	return scaffold.RenderMaterializedWorkflowMarkdown(root, cliKitPresetToShared(preset), cliMaterializedNamesForTemplate(names))
}

type policyRuleScaffoldPresetDefinition struct {
	Name        string
	DefaultName string
	Title       string
	Description string
	Category    string
	Tags        []string
	Operator    string
	Expression  string
	Reason      string
	Defaults    map[string]string
	Params      []agentpkg.WorkflowNodeFieldOption
}

func policyRuleScaffoldPresetByName(name string) (policyRuleScaffoldPresetDefinition, bool) {
	return policyRuleScaffoldPresetByNameFromList(policyRuleScaffoldPresets(), name)
}

func policyRuleScaffoldPresetByNameFromList(presets []policyRuleScaffoldPresetDefinition, name string) (policyRuleScaffoldPresetDefinition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, preset := range presets {
		if preset.Name == name {
			return preset, true
		}
	}
	return policyRuleScaffoldPresetDefinition{}, false
}

func policyRuleScaffoldPresetNames() []string {
	return policyRuleScaffoldPresetNamesFromList(policyRuleScaffoldPresets())
}

func policyRuleScaffoldPresetNamesFromList(presets []policyRuleScaffoldPresetDefinition) []string {
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func policyRuleScaffoldPresets() []policyRuleScaffoldPresetDefinition {
	presets, err := policyRuleScaffoldPresetsFromRuntimeHome("")
	if err != nil {
		return nil
	}
	return presets
}

func policyRuleScaffoldPresetNamesForSkillManager(skillManager *skillpkg.Manager) []string {
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		return policyRuleScaffoldPresetNames()
	}
	presets, err := policyRuleScaffoldPresetsFromRuntimeHome(root)
	if err != nil {
		return policyRuleScaffoldPresetNames()
	}
	return policyRuleScaffoldPresetNamesFromList(presets)
}

func policyRuleScaffoldPresetsFromRuntimeHome(runtimeHome string) ([]policyRuleScaffoldPresetDefinition, error) {
	var (
		presets []scaffold.PolicyRulePreset
		err     error
	)
	if strings.TrimSpace(runtimeHome) == "" {
		presets = scaffold.BuiltInPolicyRulePresets()
	} else {
		presets, err = scaffold.PolicyRulePresetsFromDirs(filepath.Join(runtimeHome, "templates", "policies", "scaffolds"))
	}
	if err != nil {
		return nil, err
	}
	out := make([]policyRuleScaffoldPresetDefinition, 0, len(presets))
	for _, preset := range presets {
		out = append(out, policyRuleScaffoldPresetDefinitionFromShared(preset))
	}
	return out, nil
}

func policyRuleScaffoldPresetDefinitionFromShared(preset scaffold.PolicyRulePreset) policyRuleScaffoldPresetDefinition {
	return policyRuleScaffoldPresetDefinition{
		Name:        preset.Name,
		DefaultName: preset.DefaultName,
		Title:       preset.Title,
		Description: preset.Description,
		Category:    preset.Category,
		Tags:        append([]string(nil), preset.Tags...),
		Operator:    preset.Operator,
		Expression:  preset.Expression,
		Reason:      preset.Reason,
		Defaults:    copyStringMap(preset.Defaults),
		Params:      workflowNodeFieldsFromPolicyPresetFields(preset.Params),
	}
}

func workflowNodeFieldsFromPolicyPresetFields(fields []scaffold.PolicyRuleFieldOption) []agentpkg.WorkflowNodeFieldOption {
	out := make([]agentpkg.WorkflowNodeFieldOption, 0, len(fields))
	for _, field := range fields {
		out = append(out, agentpkg.WorkflowNodeFieldOption{
			Name:        field.Name,
			Label:       field.Label,
			Type:        field.Type,
			Description: field.Description,
			Placeholder: field.Placeholder,
			Default:     field.Default,
			Required:    field.Required,
			Options:     append([]string(nil), field.Options...),
			Examples:    append([]string(nil), field.Examples...),
			Hints:       append([]string(nil), field.Hints...),
		})
	}
	return out
}

func renderPolicyRuleYAMLTemplate(name string, preset policyRuleScaffoldPresetDefinition) (string, error) {
	definition := agentpkg.WorkflowPolicyRuleDefinition{
		Name:        name,
		Label:       preset.Title,
		Description: preset.Description,
		Operator:    preset.Operator,
		Expression:  preset.Expression,
		Reason:      preset.Reason,
		Defaults:    copyStringMap(preset.Defaults),
		Params:      append([]agentpkg.WorkflowNodeFieldOption(nil), preset.Params...),
	}
	definition = agentpkg.MigrateWorkflowPolicyRuleDefinition(definition)
	data, err := yaml.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("render policy rule scaffold: %w", err)
	}
	return "# Loaded automatically from policies/workflow_rules/*.yaml.\n" + string(data), nil
}

type teamScaffoldPresetDefinition struct {
	Name                  string
	DefaultName           string
	Title                 string
	Description           string
	Category              string
	Tags                  []string
	BaseTemplate          string
	RecommendedWorkflow   string
	RecommendedEntryAgent string
}

func teamScaffoldPresetByName(name string) (teamScaffoldPresetDefinition, bool) {
	return teamScaffoldPresetByNameFromList(teamScaffoldPresets(), name)
}

func teamScaffoldPresetByNameFromList(presets []teamScaffoldPresetDefinition, name string) (teamScaffoldPresetDefinition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, preset := range presets {
		if preset.Name == name {
			return preset, true
		}
	}
	return teamScaffoldPresetDefinition{}, false
}

func teamScaffoldPresetNames() []string {
	return teamScaffoldPresetNamesFromList(teamScaffoldPresets())
}

func teamScaffoldPresetNamesFromList(presets []teamScaffoldPresetDefinition) []string {
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func teamScaffoldPresets() []teamScaffoldPresetDefinition {
	presets, err := teamScaffoldPresetsFromRuntimeHome("")
	if err != nil {
		return nil
	}
	return presets
}

func teamScaffoldPresetNamesForSkillManager(skillManager *skillpkg.Manager) []string {
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		return teamScaffoldPresetNames()
	}
	presets, err := teamScaffoldPresetsFromRuntimeHome(root)
	if err != nil {
		return teamScaffoldPresetNames()
	}
	return teamScaffoldPresetNamesFromList(presets)
}

func teamScaffoldPresetsFromRuntimeHome(runtimeHome string) ([]teamScaffoldPresetDefinition, error) {
	var (
		presets []scaffold.TeamPreset
		err     error
	)
	if strings.TrimSpace(runtimeHome) == "" {
		presets = scaffold.BuiltInTeamPresets()
	} else {
		presets, err = scaffold.TeamPresetsFromDirs(filepath.Join(runtimeHome, "templates", "teams", "scaffolds"))
	}
	if err != nil {
		return nil, err
	}
	out := make([]teamScaffoldPresetDefinition, 0, len(presets))
	for _, preset := range presets {
		out = append(out, teamScaffoldPresetDefinitionFromShared(preset))
	}
	return out, nil
}

func teamScaffoldPresetDefinitionFromShared(preset scaffold.TeamPreset) teamScaffoldPresetDefinition {
	return teamScaffoldPresetDefinition{
		Name:                  preset.Name,
		DefaultName:           preset.DefaultName,
		Title:                 preset.Title,
		Description:           preset.Description,
		Category:              preset.Category,
		Tags:                  append([]string(nil), preset.Tags...),
		BaseTemplate:          preset.BaseTemplate,
		RecommendedWorkflow:   preset.RecommendedWorkflow,
		RecommendedEntryAgent: preset.RecommendedEntryAgent,
	}
}

func renderTeamTemplateYAMLTemplate(name string, preset teamScaffoldPresetDefinition) (string, error) {
	template, ok := agentpkg.LoadTeamTemplate(preset.BaseTemplate)
	if !ok {
		return "", fmt.Errorf("base team template %q is unavailable", preset.BaseTemplate)
	}
	template.Name = name
	template.Title = firstNonEmptyString(preset.Title, template.Title)
	template.Description = firstNonEmptyString(preset.Description, template.Description)
	template.Category = firstNonEmptyString(preset.Category, template.Category)
	if len(preset.Tags) > 0 {
		template.Tags = append([]string(nil), preset.Tags...)
	}
	template.RecommendedWorkflow = firstNonEmptyString(preset.RecommendedWorkflow, template.RecommendedWorkflow)
	template.RecommendedEntryAgent = firstNonEmptyString(preset.RecommendedEntryAgent, template.RecommendedEntryAgent)
	template.Source = ""
	template.Path = ""
	template.Custom = false
	template.Kind = ""
	template.Version = 0
	template.MinVersion = 0
	template.MigratedFromVersion = 0
	if len(template.QuorumPresets) == 0 {
		template.QuorumPresets = defaultTeamTemplateQuorumPresets(template.RoleTemplates)
	}
	template, err := (&agentpkg.WorkflowRunner{}).ValidateTeamTemplateResource(name, template)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("render team template scaffold: %w", err)
	}
	return "# Loaded automatically from templates/teams/*.yaml.\n" + string(data), nil
}

func defaultTeamTemplateQuorumPresets(roles []agentpkg.TeamRoleTemplate) []agentpkg.TeamQuorumPreset {
	if len(roles) == 0 {
		return nil
	}
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(role.Name) != "" {
			roleNames = append(roleNames, role.Name)
		}
	}
	if len(roleNames) == 0 {
		return nil
	}
	required := 1
	if len(roleNames) >= 3 {
		required = 2
	}
	return []agentpkg.TeamQuorumPreset{{
		Name:         "default-review",
		Title:        "Default review quorum",
		Description:  "Reusable review gate preset generated from this team scaffold.",
		Required:     required,
		Roles:        roleNames,
		RejectBlocks: true,
		Default:      true,
	}}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func writeYAMLStringList(b *strings.Builder, key string, values []string) {
	if b == nil || len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", key)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fmt.Fprintf(b, "  - %s\n", value)
	}
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func renderWorkflowYAMLTemplate(name, templateName string) (string, error) {
	return agentpkg.RenderWorkflowTemplateYAML(name, templateName)
}

func workflowTemplateScaffoldSourceNames() []string {
	rows := (&agentpkg.WorkflowRunner{}).WorkflowTemplates()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	sort.Strings(names)
	return names
}

func renderWorkflowTemplateResourceYAML(name, source string) (string, error) {
	template, ok := (&agentpkg.WorkflowRunner{}).WorkflowTemplate(source)
	if !ok {
		return "", fmt.Errorf("unknown workflow template: %s", source)
	}
	template.Name = name
	template.Graph.Name = name
	template.Title = firstNonEmptyString(template.Title, name)
	template.Description = firstNonEmptyString(template.Description, fmt.Sprintf("Forked from workflow template %s.", source))
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	if !workflowTemplateStringListContains(template.Tags, "fork") {
		template.Tags = append(append([]string(nil), template.Tags...), "fork")
	}
	template.Kind = "goflow.workflow_template_resource"
	template.Version = 2
	template.MinVersion = 1
	template.MigratedFromVersion = 0
	template.Source = ""
	template.Path = ""
	template.Custom = false
	template.Stages = len(template.Graph.Stages)
	if strings.TrimSpace(template.Graph.Description) == "" {
		template.Graph.Description = template.Description
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("render workflow template scaffold: %w", err)
	}
	return "# Loaded automatically from templates/workflows/*.yaml.\n" + string(data), nil
}

func workflowTemplateStringListContains(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func renderWorkflowMarkdownTemplate(name string) string {
	return fmt.Sprintf(`# %s Workflow

This workflow is executable with:

`+"```text"+`
/workflow %s <request>
`+"```"+`

Edit `+"`workflow.yaml`"+` to declare staged agent/skill orchestration.

## Stages

1. Plan: collect context and produce executable steps.
2. Implement: apply approved changes with the fixer agent.
3. Audit: verify results and report residual risk.

## Approval Boundaries

- `+"`approval: true`"+` pauses before a stage starts.
- Tool approval is separate. A stage can start, then pause again if the selected agent calls a confirm-policy tool such as `+"`file_tools/write_file`"+`.
- After tool approval, GoFlow resumes the suspended stage with the approved tool result and then continues to the next stage.

For dynamic branching, have the planning stage produce one of:

- Next skill: code-writing
- Next skills: code-writing, code-audit

## Implementation Notes

- Keep each stage tied to one agent permission boundary.
- Use skill names to keep stage prompts reusable.
- Add explicit approval boundaries before write or exec stages.
- See `+"`docs/workflows.md`"+` for branch selection, stage approval, and approval-resume examples.
`, name, name)
}
