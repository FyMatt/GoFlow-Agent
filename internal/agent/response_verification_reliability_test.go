package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestResponseVerificationDoesNotTreatRollbackPlanAsExecutionFailure(t *testing.T) {
	for _, output := range []string{
		"## 回滚计划\n如果测试失败，停止变更并恢复原备份。尚未执行。",
		"## Rollback Plan\nIf tests failed after a change, restore the backup. Do not proceed with blockers.",
		"## 验证结果\n未执行测试；失败条件和停止条件仅为计划。",
	} {
		_, _, checks := buildStructuredArtifacts(output, nil, "plan")
		if len(checks) == 0 || checks[0].Kind != "response" || checks[0].Status != "passed" {
			t.Fatalf("plan prose must not become a failed response: %#v", checks)
		}
		// Capturing a response is not evidence that its proposed checks ran.
		if workflowGraphQualityVerificationCounts(checks[0]) || workflowGraphQualityHasExecutedCheck([]WorkflowStageResult{{Result: schema.AgentResult{Output: output, Verification: checks}}}) {
			t.Fatalf("plan must not count as executed verification: %#v", checks)
		}
	}
}

func TestResponseVerificationRetainsActualToolFailureDespitePlanProse(t *testing.T) {
	for _, tc := range []struct {
		tool schema.ToolResult
		want string
	}{
		{schema.ToolResult{ToolName: "run_command", IsError: true, Content: "test command failed"}, "failed"},
		{schema.ToolResult{ToolName: "run_command", Denied: true, Content: "operator denied command"}, "blocked"},
	} {
		_, _, checks := buildStructuredArtifacts("## 回滚计划\n若测试失败，恢复备份。", []schema.ToolResult{tc.tool}, "plan")
		if len(checks) == 0 || checks[0].Status != tc.want {
			t.Fatalf("actual tool failure or denial must remain visible: %#v", checks)
		}
		found := false
		for _, check := range checks {
			if check.Kind == "tool" && check.Status == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("actual tool evidence must be preserved: %#v", checks)
		}
	}
}
