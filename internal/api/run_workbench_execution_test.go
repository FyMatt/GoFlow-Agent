package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestWorkflowRunWorkbenchExecutionIsBoundedAndPreservesBlockers(t *testing.T) {
	run := session.WorkflowRunSnapshot{
		ID:             "run-1",
		Name:           "复杂工程交付",
		Status:         "awaiting_approval",
		Request:        strings.Repeat("需求", 500),
		NextStage:      "verify",
		TargetID:       "local",
		ApprovalPrompt: "批准写回工作区",
	}
	for index := 0; index < 15; index++ {
		status := "pending"
		blocked := ""
		if index == 2 {
			status = "blocked"
			blocked = "等待安全审批"
		}
		run.ChangeSlices = append(run.ChangeSlices, session.WorkflowChangeSliceSnapshot{
			ID:                   fmt.Sprintf("slice-%02d", index),
			Title:                fmt.Sprintf("变更分片 %d", index),
			ExecutionMode:        "write",
			Status:               status,
			BlockedReason:        blocked,
			DependsOn:            []string{"slice-00"},
			TargetFiles:          []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go", "omitted.go"},
			ChangedFiles:         []string{"a.go", "b.go"},
			VerificationCommands: []string{"go test ./..."},
			EvidenceStages:       []string{"test"},
			InputHashes:          map[string]string{"secret.txt": "internal-hash-must-not-leak"},
			ContractHash:         "internal-contract-hash-must-not-leak",
		})
	}
	delivery := runDeliveryManifest{}
	for index := 0; index < 8; index++ {
		delivery.Acceptance = append(delivery.Acceptance, runDeliveryCheck{Name: fmt.Sprintf("acceptance-%d", index), Status: "passed"})
		delivery.Verification = append(delivery.Verification, runDeliveryCheck{Name: fmt.Sprintf("verification-%d", index), Status: "passed"})
	}
	for index := 0; index < 14; index++ {
		delivery.Artifacts = append(delivery.Artifacts, runDeliveryArtifact{
			ID: fmt.Sprintf("artifact-%d", index), Title: fmt.Sprintf("产物 %d", index),
			ArtifactRef: fmt.Sprintf("artifact://%d", index), Summary: strings.Repeat("摘要", 200),
		})
	}

	execution := workflowRunWorkbenchExecution(run, delivery)
	if execution == nil || execution.CurrentSlice == nil || execution.CurrentSlice.ID != "slice-02" {
		t.Fatalf("expected blocked current slice, got %#v", execution)
	}
	if len(execution.ChangeSlices) != runWorkbenchSliceLimit || execution.OmittedSlices != 3 {
		t.Fatalf("expected bounded slices, got returned=%d omitted=%d", len(execution.ChangeSlices), execution.OmittedSlices)
	}
	if len(execution.CurrentSlice.TargetFiles) != runWorkbenchSliceListLimit {
		t.Fatalf("expected bounded target files, got %#v", execution.CurrentSlice.TargetFiles)
	}
	if len(execution.CurrentSlice.ChangedFiles) != 2 || execution.CurrentSlice.ChangedFiles[0] != "a.go" {
		t.Fatalf("expected changed file evidence, got %#v", execution.CurrentSlice.ChangedFiles)
	}
	if len(execution.Blockers) != 2 || execution.Blockers[0] != "等待安全审批" || execution.Blockers[1] != "批准写回工作区" {
		t.Fatalf("expected slice and approval blockers, got %#v", execution.Blockers)
	}
	if len(execution.Verification) != runWorkbenchCheckLimit || execution.OmittedChecks != 4 {
		t.Fatalf("expected bounded verification, got returned=%d omitted=%d", len(execution.Verification), execution.OmittedChecks)
	}
	if len(execution.Artifacts) != runWorkbenchArtifactLimit || execution.OmittedArtifacts != 14-runWorkbenchArtifactLimit {
		t.Fatalf("expected bounded artifacts, got returned=%d omitted=%d", len(execution.Artifacts), execution.OmittedArtifacts)
	}
	encoded, err := json.Marshal(execution)
	if err != nil {
		t.Fatalf("marshal execution summary: %v", err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"input_hashes", "contract_hash", "internal-hash-must-not-leak", "internal-contract-hash-must-not-leak", "omitted.go"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("bounded workbench leaked %q: %s", forbidden, body)
		}
	}
	if len([]rune(execution.Contract.Request)) > runWorkbenchRequestTextLimit {
		t.Fatalf("request was not bounded: %d", len([]rune(execution.Contract.Request)))
	}
}

func TestHistoricalCompletedWorkflowUsesStructuredBlockedDecision(t *testing.T) {
	run := session.WorkflowRunSnapshot{
		ID: "legacy-blocked", Name: "skill-chain", Status: "completed",
		CompletedStages: []session.WorkflowRunStageSnapshot{{
			Stage: "consolidate", Status: "completed", OutputValues: map[string]any{"decision": "blocked"},
		}},
	}
	if got := effectiveWorkflowRunStatus(run); got != "blocked" {
		t.Fatalf("effective status = %q", got)
	}
	if got := summarizeWorkflowRunForAPI(run).Status; got != "blocked" {
		t.Fatalf("summary status = %q", got)
	}
}

func TestAgentRunWorkbenchExecutionIncludesContractBlockersVerificationAndArtifacts(t *testing.T) {
	run := session.AgentRunSnapshot{
		ID:               "agent-1",
		AgentID:          "software-engineer",
		TargetID:         "local",
		Request:          "修复并验证项目",
		Status:           "awaiting_tool_approval",
		PendingApprovals: []session.PendingApprovalSnapshot{{ToolName: "apply_patch", Stage: "implement"}},
	}
	delivery := runDeliveryManifest{
		Acceptance:   []runDeliveryCheck{{Name: "范围检查", Status: "passed"}},
		Verification: []runDeliveryCheck{{Name: "go test", Status: "passed", Detail: "ok"}},
		Artifacts:    []runDeliveryArtifact{{ID: "artifact-1", Kind: "test", Title: "测试结果", Summary: "通过", ArtifactRef: "goflow://artifact-1"}},
	}
	execution := agentRunWorkbenchExecution(run, delivery)
	if execution == nil || execution.Contract.Request != run.Request || execution.Contract.Workflow != run.AgentID {
		t.Fatalf("expected agent contract in workbench execution, got %#v", execution)
	}
	if len(execution.Blockers) != 1 || execution.Blockers[0] != "apply_patch" {
		t.Fatalf("expected approval blocker, got %#v", execution.Blockers)
	}
	if len(execution.Verification) != 2 || len(execution.Artifacts) != 1 {
		t.Fatalf("expected bounded agent evidence, got %#v", execution)
	}
}

func TestAgentRunWorkbenchExecutionPrioritizesOutcomeOverReadNoise(t *testing.T) {
	run := session.AgentRunSnapshot{ID: "agent-noise", AgentID: "software-engineer", Request: "修改并测试", Status: "completed"}
	delivery := runDeliveryManifest{
		Conclusion: "failed",
		Changes: []runDiffItem{
			{Path: "web/app.py", Status: "modified"},
			{Path: "write_file", StatusCode: "M", IsError: true},
		},
		Verification: []runDeliveryCheck{
			{Kind: "response", Status: "failed", Detail: strings.Repeat("模型输出", 80)},
			{Kind: "tool", Status: "passed", Detail: `read_file: {"path":"web/app.py"}`},
			{Kind: "tool", Status: "failed", Detail: `run_command: {"program":"python","args":["-m","pytest"],"exit_code":1,"duration_ms":25,"stderr":"No module named pytest"}`},
			{Kind: "verifier", Status: "failed", Detail: "缺少自动化测试"},
		},
		Artifacts: []runDeliveryArtifact{
			{Kind: "tool_result", Title: "read_file", Summary: "raw file", IsError: false},
			{Kind: "verification", Title: "质量审查", Summary: "失败", IsError: true},
			{Kind: "output", Title: "最终输出", Summary: "完成"},
		},
		Unverified:     []string{"缺少验证"},
		RemainingRisks: []string{"仍有风险"},
		Metrics:        runDeliveryMetrics{DurationMs: 1200, LLMCalls: 2, ToolCalls: 8, Changes: 2},
		Quality:        runDeliveryQualitySummary{Score: 40},
	}
	execution := agentRunWorkbenchExecution(run, delivery)
	if execution.Outcome.Conclusion != "failed" || execution.Outcome.ChangeCount != 1 || len(execution.Outcome.ChangedFiles) != 1 || execution.Outcome.ChangedFiles[0] != "web/app.py" {
		t.Fatalf("unexpected outcome summary: %#v", execution.Outcome)
	}
	if len(execution.Verification) != 2 || execution.Verification[0].Kind != "verifier" || execution.Verification[1].Name != "run_command" || strings.Contains(execution.Verification[1].Detail, `{"program"`) {
		t.Fatalf("verification noise was not reduced: %#v", execution.Verification)
	}
	if len(execution.Artifacts) != 2 || execution.Artifacts[0].Kind != "verification" || execution.Artifacts[1].Kind != "output" {
		t.Fatalf("artifact noise was not reduced: %#v", execution.Artifacts)
	}
}

func TestWorkbenchCheckDetailSummarizesTruncatedCommandJSON(t *testing.T) {
	detail := workbenchCheckDetail(runDeliveryCheck{
		Kind:   "tool",
		Status: "failed",
		Detail: `run_command: {"args":["-m","pytest","-q"],"cancelled":false,"classification":"test","cwd":"D:\\workspace","exit_c…`,
	})
	if !strings.Contains(detail, "python -m pytest -q") || !strings.Contains(detail, "failed") || strings.Contains(detail, `{"args"`) {
		t.Fatalf("truncated command detail was not normalized: %q", detail)
	}
}
