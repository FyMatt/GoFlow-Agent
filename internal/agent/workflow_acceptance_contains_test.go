package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowAcceptanceContainsChineseReportSections(t *testing.T) {
	for _, tc := range []struct{ expected, output string }{
		{"Implementation Plan", "## 实施计划\n修改指定模块。"},
		{"Implementation Summary", "## 实现总结\n完成指定实现。"},
		{"Files Changed", "## 已变更文件\n- ttlcache/cache.go"},
		{"Verification Attempted", "## 验证尝试\n运行了实际测试。"},
		{"Verification Commands", "验证命令：go test ./..."},
		{"Verification Result", "### 验证结果\n未执行。"},
		{"Commands Run", "**已执行命令**：无。"},
		{"Audit Decision", "## 审查决定\n需返修。"},
		{"Findings", "### 安全发现\n未确认漏洞。"},
		{"Release Readiness", "发布就绪状态：阻塞。"},
		{"Release Readiness", "## 发布就绪度\n可发布。"},
		{"Done Criteria", "## 完成标准\n满足原始要求。"},
		{"Next Steps", "## 下一步\n无剩余实施工作。"},
		{"Final Status", "## 最终状态\n待验证。"},
		{"Verification Evidence", "## 验证证据\n暂无。"},
	} {
		t.Run(tc.expected, func(t *testing.T) {
			stage := workflowGraphStage{AcceptanceCriteria: []workflowGraphAcceptanceCriterion{{Contains: tc.expected, Ref: "result.output"}}}
			got := evaluateWorkflowGraphAcceptanceCriteria(stage, WorkflowStageResult{Result: schema.AgentResult{Output: tc.output}})
			if len(got) != 1 || got[0].Status != "passed" {
				t.Fatalf("known Chinese section must satisfy structural contains: %#v", got)
			}
		})
	}
}

func TestWorkflowAcceptanceContainsPreservesExactBusinessLiterals(t *testing.T) {
	for _, tc := range []struct {
		expected, output string
		passed           bool
	}{
		{"ready", "## 就绪\n可交付。", false},
		{"APPROVED", "approved", false},
		{"风险阈值=High", "风险阈值=high", false},
		{"Verification Result", "正文提及验证结果，但没有章节。", false},
		{"Implementation Summary", "正文只说实现总结，没有对应章节。", false},
		{"Files Changed", "```text\n## 已变更文件\n```", false},
		{"Verification Result", "## 验证结果说明\n缺少要求的标题。", false},
		{"Verification Result", "```text\n## 验证结果\n```", false},
		{"input_is_controlled", "输入是否可控未知", false},
		{"输入是否可控未知", "输入是否可控未知", true},
		{"business/Ready", "business/Ready", true},
		{"Verification Result", "Verification Result: blocked", true},
	} {
		t.Run(tc.expected+tc.output, func(t *testing.T) {
			stage := workflowGraphStage{AcceptanceCriteria: []workflowGraphAcceptanceCriterion{{Contains: tc.expected}}}
			got := evaluateWorkflowGraphAcceptanceCriteria(stage, WorkflowStageResult{Result: schema.AgentResult{Output: tc.output}})
			if len(got) != 1 || (got[0].Status == "passed") != tc.passed {
				t.Fatalf("business literals must retain exact contains semantics: %#v", got)
			}
		})
	}
}

func TestWorkflowAcceptanceAliasesDoNotTranslateBusinessFieldsOrEquals(t *testing.T) {
	stage := workflowGraphStage{AcceptanceCriteria: []workflowGraphAcceptanceCriterion{
		{Ref: "outputs.state", Contains: "Verification Result"},
		{Ref: "result.output", Equals: "Verification Result"},
	}}
	result := WorkflowStageResult{
		Result: schema.AgentResult{Output: "验证结果"},
		Output: WorkflowStageOutput{Variables: map[string]string{"state": "验证结果"}},
	}
	for _, check := range evaluateWorkflowGraphAcceptanceCriteria(stage, result) {
		if check.Status != "failed" {
			t.Fatalf("business field contains and equals must stay exact: %#v", check)
		}
	}
}
