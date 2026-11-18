package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestComplexDeliveryRoutesFailedVerificationToRepairBeforeAudit(t *testing.T) {
	data, err := os.ReadFile("templates/workflows/complex-project-delivery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var resource struct {
		Graph workflowGraph `yaml:"graph"`
	}
	if err := yaml.Unmarshal(data, &resource); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ verify, route, pass, fail string }{{"verify", "verification-route", "audit", "quality"}, {"recovery-verify", "recovery-verification-route", "recovery-audit", "final-quality"}} {
		verify := resource.Graph.Stages[workflowGraphStageIndexByName(resource.Graph, pair.verify)]
		for _, status := range []string{"passed", "failed", "unknown", "missing-command", "failure-text"} {
			checkStatus := status
			if status == "missing-command" || status == "failure-text" {
				checkStatus = "passed"
			}
			agentResult := schema.AgentResult{
				Output:       "总体结论：通过\nVerification Result: 检查完成\nCommands Run: go test ./...\nFailure Evidence: 无\nRecovery Verification Result: 检查完成\nRegression Evidence: 回归测试已执行\nFull Test Result: 已记录",
				Verification: []schema.Verification{{Kind: "tests", Status: checkStatus}},
			}
			if status != "missing-command" {
				agentResult.ToolResults = []schema.ToolResult{{CallID: "test-1", ToolName: "command_tools/run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`}}
			}
			if status == "failure-text" {
				agentResult.Output = strings.Replace(agentResult.Output, "总体结论：通过", "总体结论：未通过", 1)
				agentResult.Output += "\n页面测试未通过"
			}
			result := workflowGraphStageResult(verify, agentResult, nil, nil, 1)
			decision := (&WorkflowRunner{}).controlWorkflowGraphStageDecision(resource.Graph, workflowGraphStageIndexByName(resource.Graph, pair.route), "", []WorkflowStageResult{result}, nil, nil)
			target := decision.Target
			expected := pair.fail
			if status == "passed" {
				expected = pair.pass
			}
			if target != expected {
				t.Fatalf("%s status %s routed to %s, want %s; decision=%#v", pair.verify, status, target, expected, decision)
			}
		}
	}
}

func TestComplexDeliveryRepairCanPatchPreviouslyVerifiedSource(t *testing.T) {
	data, err := os.ReadFile("templates/workflows/complex-project-delivery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var resource struct {
		Graph workflowGraph `yaml:"graph"`
	}
	if err := yaml.Unmarshal(data, &resource); err != nil {
		t.Fatal(err)
	}
	stage := resource.Graph.Stages[workflowGraphStageIndexByName(resource.Graph, "repair")]
	if !workflowGraphStageApprovedPlanScope(resource.Graph, stage) || stage.Input["plan"] == "" {
		t.Fatal("repair must retain task approval and the approved plan")
	}
	state := session.New(4)
	id := state.StartWorkflowRun(resource.Graph.Name, "repair the audited source")
	if err := state.UpdateWorkflowRunChangeSlicesDurable(id, []session.WorkflowChangeSliceSnapshot{{ID: "source", ExecutionMode: "write", TargetFiles: []string{"internal/api/status.go"}, Status: "verified", ContractHash: "source"}}); err != nil {
		t.Fatal(err)
	}
	tool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execution := ExecutionContext{
		Profile: config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:   []schema.Tool{tool}, Session: state, WorkflowRunID: id, WorkflowName: resource.Graph.Name, WorkflowStage: stage.Name,
		WorkspaceRoot: t.TempDir(), ApprovedPlanScope: true,
		ChangeSliceEnforcement: workflowGraphStageUsesChangeSliceScheduler(resource.Graph, stage),
	}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"internal/api/status.go","content":"package api"}`)}
	if _, err := execution.validateToolCall(call); err != nil {
		t.Fatalf("approved corrective patch was blocked by a closed initial slice: %v", err)
	}
	execution.ChangeSliceEnforcement = true
	if _, err := execution.validateToolCall(call); err == nil {
		t.Fatal("initial implementation must still reject replay of verified slices")
	}
}
