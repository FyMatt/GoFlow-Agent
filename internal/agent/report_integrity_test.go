package agent

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestReportHashesRequireSuccessfulObservedEvidence(t *testing.T) {
	hash := strings.Repeat("a", 64)
	observed := schema.ToolResult{Content: `{"sha256":"` + hash + `"}`}
	pin := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	for _, test := range []struct {
		name, text string
		results    []schema.ToolResult
		wantError  bool
	}{
		{"correct", hash, []schema.ToolResult{observed}, false},
		{"missing three characters", hash[:61], []schema.ToolResult{observed}, true},
		{"short labeled value", "SHA-256: `abc123`", []schema.ToolResult{observed}, true},
		{"short labeled value with markdown", "**SHA256**: abc123", []schema.ToolResult{observed}, true},
		{"non hexadecimal labeled value", "SHA256: " + strings.Repeat("z", 64), []schema.ToolResult{observed}, true},
		{"explicitly unknown value", "SHA256: unknown", nil, false},
		{"observed SSH fingerprint", "指纹 SHA256:" + pin, []schema.ToolResult{{Content: `{"host_key_sha256":"SHA256:` + pin + `"}`}}, false},
		{"unobserved SSH fingerprint", "指纹 SHA256:" + pin, nil, true},
		{"unknown", strings.Repeat("b", 64), []schema.ToolResult{observed}, true},
		{"failed read", hash, []schema.ToolResult{{Content: observed.Content, IsError: true}}, true},
		{"quoted fake hash in file body", hash, []schema.ToolResult{{Content: `{"content":"` + hash + `"}`}}, true},
		{"index reference", "证据值见 evidence/index.md，不在正文重抄。", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateReportHashes(test.text, test.results); (err != nil) != test.wantError {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestReportHashFailureRetriesOnlyTheReportAndRemainsBounded(t *testing.T) {
	for _, repair := range []bool{true, false} {
		t.Run(map[bool]string{true: "repair", false: "exhausted"}[repair], func(t *testing.T) {
			home := t.TempDir()
			writeWorkflowGraphYAML(t, home, "report-integrity", `
name: report-integrity
stages:
  - name: report
    agent: planner
    skill: execution-plan
    params:
      disable_tools: "true"
      verify_evidence_hashes: "true"
    retry:
      max_attempts: 2
`)
			bad := strings.Repeat("a", 61)
			second := bad
			if repair {
				second = "## 核对报告\n完整值见 evidence/index.md；没有重跑采集。"
			}
			model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: bad}}, {Message: schema.Message{Content: second}}}}
			mcp := &stubRuntimeMCP{}
			runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"planner": model})
			result, err := runtime.WorkflowRunner().Run(t.Context(), "report-integrity", "仅校验报告", true, nil)
			if repair && (err != nil || result.Status != "completed") {
				t.Fatalf("repair failed: %s %v", result.Status, err)
			}
			if !repair && err == nil {
				t.Fatal("unresolved report was accepted")
			}
			if len(model.requests) != 2 || mcp.calls != 0 {
				t.Fatalf("unbounded retry or tool replay: requests=%d tools=%d", len(model.requests), mcp.calls)
			}
			messages := ""
			for _, m := range model.requests[1].Messages {
				messages += m.Content
			}
			if !strings.Contains(messages, reportIntegrityError) {
				t.Fatal("retry omitted the actual validation failure")
			}
		})
	}
}

func TestReportHashesUseOnlyTheCurrentRunsPersistedObservations(t *testing.T) {
	state := session.New(8)
	store := session.NewArtifactObjectStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	state.SetArtifactObjectStore(store)
	first := state.StartWorkflowRun("first", "collect evidence")
	firstHash, secondHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	artifact := state.AddArtifact(session.SessionArtifactSnapshot{Kind: "tool_result", Content: `{"sha256":"` + firstHash + `","content":"` + strings.Repeat("x", 20000) + `"}`})
	state.CompleteWorkflowRun(first, "completed", "", "", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "collect", Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "read-first", ToolName: "read_file", Content: "[externalized]", ArtifactRef: artifact.ArtifactRef}}}}})
	second := state.StartWorkflowRun("second", "other evidence")
	state.CompleteWorkflowRun(second, "completed", "", "", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "collect", Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "read-second", ToolName: "read_file", Content: `{"sha256":"` + secondHash + `"}`}}}}})
	runner := &WorkflowRunner{runtime: &Runtime{session: state}, runID: first}
	stage := workflowGraphStage{Params: map[string]string{"verify_evidence_hashes": "true"}}
	if err := runner.validateReportEvidence(stage, schema.AgentResult{Output: firstHash}); err != nil {
		t.Fatalf("persisted evidence was not hydrated: %v", err)
	}
	if err := runner.validateReportEvidence(stage, schema.AgentResult{Output: secondHash}); err == nil {
		t.Fatal("another run's evidence was accepted")
	}
	if err := runner.validateReportEvidence(workflowGraphStage{}, schema.AgentResult{Output: secondHash}); err != nil {
		t.Fatal("validation changed an opt-out legacy resource")
	}
}
