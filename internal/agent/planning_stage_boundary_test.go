package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestShippedPlanningBoundariesMatchTemplateAndLiveWorkflow(t *testing.T) {
	for _, path := range []string{
		filepath.Join("templates", "workflows", "plan-implement-audit.yaml"),
		filepath.Join("..", "..", "workflows", "plan-implement-audit", "workflow.yaml"),
	} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var resource struct {
				Graph workflowGraph `yaml:"graph"`
			}
			if err := yaml.Unmarshal(data, &resource); err != nil {
				t.Fatal(err)
			}
			graph := resource.Graph
			if graph.Name == "" {
				if err := yaml.Unmarshal(data, &graph); err != nil {
					t.Fatal(err)
				}
			}
			stages := map[string]workflowGraphStage{}
			for _, stage := range graph.Stages {
				stages[stage.Name] = stage
			}
			plan := stages["plan"]
			if plan.Retry.MaxAttempts != 2 || !strings.Contains(plan.Params["output_contract"], "mode must be read or write") {
				t.Fatal("loaded planning resource lost bounded contract repair or mode instructions")
			}
			profile := applyWorkflowStageAllowedTools(config.AgentProfile{}, workflowGraphStageAllowedTools(plan))
			for _, name := range []string{"command_tools/run_command", "file_tools/write_file", "file_tools/apply_patch"} {
				if policy.EnforceToolPolicy(profile, schema.Tool{Name: name}) == nil {
					t.Fatalf("planning unexpectedly exposes %s", name)
				}
			}
			if err := policy.EnforceToolPolicy(profile, schema.Tool{Name: "file_tools/read_file", Kind: "read"}); err != nil {
				t.Fatalf("planning cannot inspect source: %v", err)
			}
			report := stages["final-report"]
			if !workflowTruthy(report.Params["disable_tools"]) || report.Params["max_iterations"] != "1" {
				t.Fatal("final summary may start a new investigation")
			}
		})
	}
}

func TestPlanningContinuationKeepsStageBoundary(t *testing.T) {
	prompt := workflowGraphContinueOutputPrompt("Produce a plan only", WorkflowStageResult{
		Stage: "plan", Result: schema.AgentResult{Output: "Partial plan", Incomplete: true},
	})
	if !strings.Contains(prompt, "a planning stage produces its plan without implementing it") ||
		!strings.Contains(prompt, "this stage's tool boundary") || strings.Contains(prompt, "do not stop at a plan") {
		t.Fatal("continuation promotes a planning stage into an implementation stage")
	}
}
