package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/skill"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestShippedPlanningSkillExposesDeclaredRepositoryMap(t *testing.T) {
	resource, err := skill.ParseFile(filepath.Join("..", "..", "skills", "execution-plan", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	profile := config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow}
	profile = applyWorkflowStageAllowedTools(profile, []string{"file_tools/repository_map", "file_tools/read_file"})
	profile = applySkillToProfile(profile, resource)
	tool := schema.Tool{Name: "repository_map", Server: "file_tools", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}
	execution := ExecutionContext{Profile: profile, Tools: []schema.Tool{tool}, WorkspaceRoot: t.TempDir()}
	if _, err := execution.validateToolCall(schema.ToolCall{Name: "repository_map", Arguments: []byte(`{"path":"."}`)}); err != nil {
		t.Fatalf("stage-declared repository map was removed by the shipped skill boundary: %v", err)
	}
}
