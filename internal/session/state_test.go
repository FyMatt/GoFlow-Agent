package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestStateSaveAndLoadRoundTripsSnapshot(t *testing.T) {
	state := New(3)
	state.SetActiveAgent("planner")
	state.SetMode("audit")
	state.AddPrompt("first")
	state.AddPrompt("second")
	state.AddToolSummary("read_file: ok")
	state.SetLastSkill(&schema.Skill{Name: "security-audit"})
	state.SetLastRouting(RoutingSnapshot{Request: "review this change", SourceAgent: "chat", TargetAgent: "auditor", TargetMode: "audit", Outcome: "rerouted", Reason: "security review"})
	state.SetTaskStage(TaskStageSnapshot{Stage: "inspect", AgentID: "planner", Mode: "audit", Detail: "reading project context"})
	state.SetPromptBudget(schema.PromptBudget{EstimatedPromptTokens: 42, PromptPrefixHash: "prefix-1"})
	state.AddTokenUsage(schema.TokenUsageSample{AgentID: "planner", Mode: "audit", PromptTokens: 10, OutputTokens: 4, CachedTokens: 2})
	state.AddCollaborationMessage(CollaborationMessageSnapshot{RunID: "wf-1", Stage: "plan", FromAgent: "planner", ToAgent: "auditor", Kind: "handoff", Subject: "review", Content: "please review the plan"})
	state.UpsertBlackboardEntry(BlackboardEntrySnapshot{RunID: "wf-1", Stage: "plan", AgentID: "planner", Kind: "decision", Title: "Approach", Content: "use staged implementation", Tags: []string{"plan"}})
	state.AddArtifact(SessionArtifactSnapshot{Kind: "tool_result", ToolName: "read_file", Content: "large result"})
	state.RememberApprovedTool("C:/repo", "write_file")

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := New(3)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := loaded.Snapshot(), state.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot mismatch\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestStateArtifactsFilterAndPersist(t *testing.T) {
	state := New(3)
	artifact := state.AddArtifact(SessionArtifactSnapshot{Kind: "tool_result", ToolName: "read_file", AgentID: "planner", Content: strings.Repeat("x", maxSessionArtifactSummaryBytes+20)})
	if artifact.ID == "" || artifact.Ref == "" || artifact.Summary == "" {
		t.Fatalf("expected normalized artifact, got %#v", artifact)
	}
	if got := state.Artifacts(SessionArtifactFilter{Tool: "read_file"}); len(got) != 1 || got[0].Content != "" {
		t.Fatalf("expected listed artifact without content, got %#v", got)
	}
	if got, ok := state.Artifact(artifact.Ref); !ok || got.Content == "" {
		t.Fatalf("expected artifact lookup by ref with content, got %#v ok=%t", got, ok)
	}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := New(3)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, ok := loaded.Artifact(artifact.ID); !ok || got.Ref != artifact.Ref {
		t.Fatalf("expected persisted artifact, got %#v ok=%t", got, ok)
	}
}

func TestStateArtifactsUseContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	first := state.AddArtifact(SessionArtifactSnapshot{Kind: "tool_result", ToolName: "read_file", Content: strings.Repeat("x", 2048)})
	second := state.AddArtifact(SessionArtifactSnapshot{Kind: "tool_result", ToolName: "read_file", Content: strings.Repeat("x", 2048)})
	if first.ArtifactRef == "" || first.Hash == "" || first.Content != "" {
		t.Fatalf("expected externalized first artifact, got %#v", first)
	}
	if second.Hash != first.Hash || !second.Deduplicated {
		t.Fatalf("expected second artifact to deduplicate by hash, first=%#v second=%#v", first, second)
	}
	if got, ok := state.Artifact(first.Ref); !ok || got.Content != strings.Repeat("x", 2048) {
		t.Fatalf("expected hydrated artifact by session ref, got %#v ok=%t", got, ok)
	}
	if got, ok := state.Artifact(first.ArtifactRef); !ok || got.Content != strings.Repeat("x", 2048) {
		t.Fatalf("expected hydrated artifact by hash ref, got %#v ok=%t", got, ok)
	}
	object, ok, err := store.Get(first.Hash)
	if err != nil || !ok || object.Content != strings.Repeat("x", 2048) {
		t.Fatalf("expected stored object, got %#v ok=%t err=%v", object, ok, err)
	}
}

func TestStateWorkflowRunArtifactsUseContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	content := strings.Repeat("workflow artifact body ", 180)
	runID := state.StartWorkflowRun("artifact-flow", "collect workflow artifacts")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []WorkflowRunStageSnapshot{{
		Stage:   "collect",
		AgentID: "planner",
		Result: schema.AgentResult{
			Output: "stage summary",
		},
		Artifacts: []WorkflowRunArtifact{{
			ID:       "declared-report",
			Stage:    "collect",
			Kind:     "report",
			Title:    "Report",
			Summary:  "report summary",
			Content:  content,
			Metadata: map[string]string{"declared": "true"},
		}},
	}})
	snapshot := state.Snapshot()
	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].Artifacts) == 0 {
		t.Fatalf("expected workflow artifacts, got %#v", snapshot.WorkflowRuns)
	}
	artifact := snapshot.WorkflowRuns[0].Artifacts[0]
	if artifact.Content != "" || artifact.ArtifactRef == "" || artifact.Hash == "" || !artifact.Externalized || artifact.ContentBytes != len([]byte(content)) {
		t.Fatalf("expected externalized workflow artifact, got %#v", artifact)
	}
	if artifact.Metadata["artifact_ref"] != artifact.ArtifactRef || artifact.Metadata["hash"] != artifact.Hash || artifact.Metadata["externalized"] != "true" {
		t.Fatalf("expected workflow artifact ref metadata, got %#v", artifact.Metadata)
	}
	stageArtifact := snapshot.WorkflowRuns[0].CompletedStages[0].Artifacts[0]
	if stageArtifact.ArtifactRef != artifact.ArtifactRef || stageArtifact.Content != "" {
		t.Fatalf("expected stage artifact to share externalized ref, got %#v", stageArtifact)
	}
	object, ok, err := store.Get(artifact.Hash)
	if err != nil || !ok || object.Content != content {
		t.Fatalf("expected stored workflow artifact object, got %#v ok=%t err=%v", object, ok, err)
	}
}

func TestStateWorkflowToolResultArtifactsPreserveFullExternalizedContent(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	content := strings.Repeat("workflow tool artifact body ", 480)
	runID := state.StartWorkflowRun("tool-artifact-flow", "collect externalized tool artifacts")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []WorkflowRunStageSnapshot{{
		Stage:   "collect",
		AgentID: "researcher",
		Result: schema.AgentResult{
			ToolResults: []schema.ToolResult{{
				CallID:   "call-fetch-1",
				ToolName: "fetch_url",
				Content:  content,
			}},
		},
	}})
	snapshot := state.Snapshot()
	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].CompletedStages) != 1 {
		t.Fatalf("expected persisted workflow stage, got %#v", snapshot.WorkflowRuns)
	}
	var toolArtifact WorkflowRunArtifact
	for _, artifact := range snapshot.WorkflowRuns[0].CompletedStages[0].Artifacts {
		if artifact.Kind == "tool_result" {
			toolArtifact = artifact
			break
		}
	}
	if toolArtifact.Content != "" || toolArtifact.ArtifactRef == "" || toolArtifact.Hash == "" || !toolArtifact.Externalized || toolArtifact.ContentBytes != len([]byte(content)) {
		t.Fatalf("expected externalized workflow tool artifact, got %#v", toolArtifact)
	}
	object, ok, err := store.Get(toolArtifact.Hash)
	if err != nil || !ok || object.Content != content {
		t.Fatalf("expected stored workflow tool artifact body, got %#v ok=%t err=%v", object, ok, err)
	}
}

func TestStateWorkflowStageValuesUseContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	large := strings.Repeat("workflow stage payload ", 640)
	runID := state.StartWorkflowRun("value-flow", "collect typed stage values")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []WorkflowRunStageSnapshot{{
		Stage: "collect",
		InputValues: map[string]any{
			"target": "auth",
			"notes":  large,
			"meta":   map[string]any{"priority": "high"},
		},
		OutputValues: map[string]any{
			"report":  large,
			"metrics": map[string]any{"score": 7, "status": "ok"},
		},
		Result: schema.AgentResult{Output: "done"},
	}})

	snapshot := state.Snapshot()
	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].CompletedStages) != 1 {
		t.Fatalf("expected one persisted workflow stage, got %#v", snapshot.WorkflowRuns)
	}
	stage := snapshot.WorkflowRuns[0].CompletedStages[0]
	if len(stage.InputValues) != 0 || stage.InputValuesArtifactRef == "" || stage.InputValuesHash == "" || !stage.InputValuesExternalized || stage.InputValueCount != 3 || stage.InputValuesBytes <= maxWorkflowStageValuesInlineBytes {
		t.Fatalf("expected externalized input values metadata, got %#v", stage)
	}
	if len(stage.OutputValues) != 0 || stage.OutputValuesArtifactRef == "" || stage.OutputValuesHash == "" || !stage.OutputValuesExternalized || stage.OutputValueCount != 2 || stage.OutputValuesBytes <= maxWorkflowStageValuesInlineBytes {
		t.Fatalf("expected externalized output values metadata, got %#v", stage)
	}
	if object, ok, err := store.Get(stage.OutputValuesHash); err != nil || !ok || !strings.Contains(object.Content, large) {
		t.Fatalf("expected stored stage output object, got %#v ok=%t err=%v", object, ok, err)
	}

	hydrated := state.HydrateWorkflowRun(snapshot.WorkflowRuns[0])
	if len(hydrated.CompletedStages) != 1 {
		t.Fatalf("expected hydrated stage, got %#v", hydrated.CompletedStages)
	}
	hydratedStage := hydrated.CompletedStages[0]
	if got := hydratedStage.InputValues["notes"]; got != large {
		t.Fatalf("expected hydrated input notes, got %#v", got)
	}
	if got := hydratedStage.OutputValues["report"]; got != large {
		t.Fatalf("expected hydrated output report, got %#v", got)
	}
	metrics, ok := hydratedStage.OutputValues["metrics"].(map[string]any)
	if !ok || metrics["score"] != float64(7) || metrics["status"] != "ok" {
		t.Fatalf("expected hydrated metrics map, got %#v", hydratedStage.OutputValues["metrics"])
	}

	sessionPath := filepath.Join(root, "session.json")
	if err := state.Save(sessionPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := New(3)
	loaded.SetArtifactObjectStore(store)
	if err := loaded.Load(sessionPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	restored, ok := loaded.WorkflowRun(runID)
	if !ok || len(restored.CompletedStages) != 1 {
		t.Fatalf("expected loaded workflow run, got %#v ok=%t", restored, ok)
	}
	if got := restored.CompletedStages[0].InputValues["notes"]; got != large {
		t.Fatalf("expected restored input notes, got %#v", got)
	}
	if got := restored.CompletedStages[0].OutputValues["report"]; got != large {
		t.Fatalf("expected restored output report, got %#v", got)
	}
}

func TestStateRunEventContentUsesContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	large := strings.Repeat("event payload ", 900)

	agentRunID := state.StartAgentRun("collect agent events")
	state.AppendAgentRunEvent(agentRunID, AgentRunEventSnapshot{
		Type:    string(schema.StreamEventToolResult),
		Content: large,
	})

	workflowRunID := state.StartWorkflowRun("event-flow", "collect workflow events")
	state.AppendWorkflowRunEvent(workflowRunID, WorkflowRunEventSnapshot{
		Type:    string(schema.StreamEventToolResult),
		Stage:   "collect",
		Content: large,
	})

	snapshot := state.Snapshot()
	if len(snapshot.AgentRuns) != 1 || len(snapshot.AgentRuns[0].Events) < 2 {
		t.Fatalf("expected persisted agent events, got %#v", snapshot.AgentRuns)
	}
	agentEvent := snapshot.AgentRuns[0].Events[len(snapshot.AgentRuns[0].Events)-1]
	if agentEvent.ContentArtifactRef == "" || agentEvent.ContentHash == "" || !agentEvent.ContentExternalized || agentEvent.ContentBytes <= maxRunEventContentInlineBytes {
		t.Fatalf("expected externalized agent event content metadata, got %#v", agentEvent)
	}
	if agentEvent.Content == large {
		t.Fatalf("expected compact agent event content summary, got full payload")
	}

	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].Events) < 2 {
		t.Fatalf("expected persisted workflow events, got %#v", snapshot.WorkflowRuns)
	}
	workflowEvent := snapshot.WorkflowRuns[0].Events[len(snapshot.WorkflowRuns[0].Events)-1]
	if workflowEvent.ContentArtifactRef == "" || workflowEvent.ContentHash == "" || !workflowEvent.ContentExternalized || workflowEvent.ContentBytes <= maxRunEventContentInlineBytes {
		t.Fatalf("expected externalized workflow event content metadata, got %#v", workflowEvent)
	}
	if workflowEvent.Content == large {
		t.Fatalf("expected compact workflow event content summary, got full payload")
	}

	agentRun, ok := state.AgentRun(agentRunID)
	if !ok || len(agentRun.Events) < 2 || agentRun.Events[len(agentRun.Events)-1].Content != large {
		t.Fatalf("expected hydrated agent event content, got %#v ok=%t", agentRun.Events, ok)
	}
	workflowRun, ok := state.WorkflowRun(workflowRunID)
	if !ok || len(workflowRun.Events) < 2 || workflowRun.Events[len(workflowRun.Events)-1].Content != large {
		t.Fatalf("expected hydrated workflow event content, got %#v ok=%t", workflowRun.Events, ok)
	}
}

func TestStatePendingApprovalArgumentsUseContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	content := strings.Repeat("externalized approval payload ", 420)
	args := `{"path":"demo.txt","content":"` + content + `"}`

	state.SetPendingApprovals([]PendingApprovalSnapshot{{
		CallID:           "call-1",
		ToolName:         "write_file",
		AgentID:          "fixer",
		ArgumentsSummary: "write demo file",
		Arguments:        args,
		AgentRunID:       "agent-run-1",
	}})
	state.SetWorkflow(WorkflowSnapshot{
		RunID:            "workflow-run-1",
		Name:             "plan-fix-audit",
		Status:           "awaiting_tool_approval",
		NextStage:        "fix",
		PendingCallID:    "call-1",
		PendingToolName:  "write_file",
		PendingAgentID:   "fixer",
		PendingArguments: args,
	})

	snapshot := state.Snapshot()
	if len(snapshot.PendingApprovals) != 1 {
		t.Fatalf("expected one pending approval, got %#v", snapshot.PendingApprovals)
	}
	pending := snapshot.PendingApprovals[0]
	if pending.Arguments != "" || pending.ArgumentsArtifactRef == "" || pending.ArgumentsHash == "" || !pending.ArgumentsExternalized || pending.ArgumentsBytes != len([]byte(args)) {
		t.Fatalf("expected externalized pending approval arguments, got %#v", pending)
	}
	if snapshot.Workflow.PendingArguments != "" || snapshot.Workflow.PendingArgumentsArtifactRef == "" || snapshot.Workflow.PendingArgumentsHash == "" || !snapshot.Workflow.PendingArgumentsExternalized {
		t.Fatalf("expected workflow pending arguments to externalize, got %#v", snapshot.Workflow)
	}
	hydrated := state.HydratePendingApproval(pending)
	if hydrated.Arguments != args {
		t.Fatalf("expected hydrated pending approval arguments, got %d bytes", len([]byte(hydrated.Arguments)))
	}
	hydratedWorkflow := state.HydrateWorkflowPendingArguments(snapshot.Workflow)
	if hydratedWorkflow.PendingArguments != args {
		t.Fatalf("expected hydrated workflow pending arguments, got %d bytes", len([]byte(hydratedWorkflow.PendingArguments)))
	}
	object, ok, err := store.Get(pending.ArgumentsHash)
	if err != nil || !ok || object.Content != args {
		t.Fatalf("expected stored pending approval object, got %#v ok=%t err=%v", object, ok, err)
	}

	path := filepath.Join(root, "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), content) {
		t.Fatalf("expected compact session index to omit raw pending approval arguments")
	}
}

func TestStateAgentRunResumeContextUsesContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	runID := state.StartAgentRun("resume large approval context")
	messageContent := strings.Repeat("resume message body ", 700)
	resultContent := strings.Repeat("resume tool result ", 700)
	callContent := strings.Repeat("resume suspended call ", 500)
	args := `{"path":"demo.txt","content":"` + callContent + `"}`

	_, ok := state.SetAgentRunResumeContext(runID, AgentRunResumeContextSnapshot{
		AgentID:      "chat",
		Mode:         "chat",
		SystemPrompt: "system",
		Messages: []schema.Message{{
			Role:    "assistant",
			Content: messageContent,
			ProviderFields: map[string]json.RawMessage{
				"reasoning_content": json.RawMessage(`"private reasoning"`),
			},
		}},
		SuspendedCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(args),
		}},
		CollectedResults: []schema.ToolResult{{
			CallID:   "call-read-1",
			ToolName: "read_file",
			Content:  resultContent,
		}},
	})
	if !ok {
		t.Fatalf("expected resume context to persist")
	}
	snapshot := state.Snapshot()
	if len(snapshot.AgentRuns) != 1 || snapshot.AgentRuns[0].ResumeContext == nil {
		t.Fatalf("expected persisted run resume context, got %#v", snapshot.AgentRuns)
	}
	context := *snapshot.AgentRuns[0].ResumeContext
	if len(context.Messages) != 0 || context.MessagesArtifactRef == "" || context.MessagesHash == "" || !context.MessagesExternalized || context.MessagesCount != 1 {
		t.Fatalf("expected externalized resume messages, got %#v", context)
	}
	if len(context.SuspendedCalls) != 0 || context.SuspendedCallsArtifactRef == "" || context.SuspendedCallsHash == "" || !context.SuspendedCallsExternalized || context.SuspendedCallCount != 1 {
		t.Fatalf("expected externalized suspended calls, got %#v", context)
	}
	if len(context.CollectedResults) != 0 || context.CollectedResultsArtifactRef == "" || context.CollectedResultsHash == "" || !context.CollectedResultsExternalized || context.CollectedResultCount != 1 {
		t.Fatalf("expected externalized collected results, got %#v", context)
	}
	run, ok := state.AgentRun(runID)
	if !ok || run.ResumeContext == nil {
		t.Fatalf("expected hydrated run resume context, got %#v ok=%t", run, ok)
	}
	hydrated := *run.ResumeContext
	if len(hydrated.Messages) != 1 || hydrated.Messages[0].Content != messageContent {
		t.Fatalf("expected hydrated resume messages, got %#v", hydrated.Messages)
	}
	if got := string(hydrated.Messages[0].ProviderFields["reasoning_content"]); got != `"private reasoning"` {
		t.Fatalf("expected hydrated provider fields, got %#v", hydrated.Messages[0].ProviderFields)
	}
	if len(hydrated.SuspendedCalls) != 1 || string(hydrated.SuspendedCalls[0].Arguments) != args {
		t.Fatalf("expected hydrated suspended calls, got %#v", hydrated.SuspendedCalls)
	}
	if len(hydrated.CollectedResults) != 1 || hydrated.CollectedResults[0].Content != resultContent {
		t.Fatalf("expected hydrated collected results, got %#v", hydrated.CollectedResults)
	}

	path := filepath.Join(root, "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if strings.Contains(text, messageContent) || strings.Contains(text, resultContent) || strings.Contains(text, callContent) {
		t.Fatalf("expected compact session index to omit raw resume context payloads")
	}
}

func TestStateAgentRunArtifactsUseContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	content := strings.Repeat("agent artifact body ", 180)
	runID := state.StartAgentRun("collect agent artifacts")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "final summary",
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-1",
			ToolName: "read_file",
			Content:  content,
		}},
		Findings: []schema.Finding{{
			Severity: "high",
			Summary:  "finding summary",
			Files:    []string{"src/app.go"},
		}},
	})
	snapshot := state.Snapshot()
	if len(snapshot.AgentRuns) != 1 || len(snapshot.AgentRuns[0].Artifacts) < 3 {
		t.Fatalf("expected agent artifacts, got %#v", snapshot.AgentRuns)
	}
	var toolArtifact AgentRunArtifactSnapshot
	for _, artifact := range snapshot.AgentRuns[0].Artifacts {
		if artifact.Kind == "tool_result" {
			toolArtifact = artifact
			break
		}
	}
	if toolArtifact.Content != "" || toolArtifact.ArtifactRef == "" || toolArtifact.Hash == "" || toolArtifact.ContentBytes != len([]byte(content)) {
		t.Fatalf("expected externalized agent tool artifact, got %#v", toolArtifact)
	}
	if toolArtifact.Metadata["artifact_ref"] != toolArtifact.ArtifactRef || toolArtifact.Metadata["hash"] != toolArtifact.Hash || toolArtifact.Metadata["externalized"] != "true" {
		t.Fatalf("expected agent artifact ref metadata, got %#v", toolArtifact.Metadata)
	}
	object, ok, err := store.Get(toolArtifact.Hash)
	if err != nil || !ok || object.Content != content {
		t.Fatalf("expected stored agent artifact object, got %#v ok=%t err=%v", object, ok, err)
	}
}

func TestStateStoresLargeRunPayloadsInCompressedArchive(t *testing.T) {
	state := New(3)
	large := strings.Repeat("x", maxWorkflowRunText*3)

	agentRunID := state.StartAgentRun("inspect large output")
	state.CompleteAgentRun(agentRunID, "completed", schema.AgentResult{
		Output: large,
		ToolResults: []schema.ToolResult{{
			CallID:   "call-1",
			ToolName: "read_file",
			Content:  large,
		}},
	})
	workflowRunID := state.StartWorkflowRun("large-flow", "inspect large output")
	state.CompleteWorkflowRun(workflowRunID, "completed", large, "", "", nil, []WorkflowRunStageSnapshot{{
		Stage: "inspect",
		Result: schema.AgentResult{
			Output: large,
			ToolResults: []schema.ToolResult{{
				CallID:   "call-2",
				ToolName: "read_file",
				Content:  large,
			}},
		},
	}})
	state.AddArtifact(SessionArtifactSnapshot{Kind: "tool_result", ToolName: "read_file", Content: large})

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) > 220_000 {
		t.Fatalf("expected compact session index, got %d bytes", len(data))
	}
	archiveInfo, err := os.Stat(fullSessionArchivePath(path))
	if err != nil {
		t.Fatalf("expected full compressed session archive: %v", err)
	}
	if archiveInfo.Size() <= 0 || archiveInfo.Size() >= int64(len(data)) {
		t.Fatalf("expected compressed archive to be smaller than JSON index for repeated content, archive=%d index=%d", archiveInfo.Size(), len(data))
	}

	loaded := New(3)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snapshot := loaded.Snapshot()
	if got := snapshot.AgentRuns[0].Result.ToolResults[0].Content; got != large {
		t.Fatalf("expected complete agent tool result after archive load, got %d bytes", len([]byte(got)))
	}
	if got := snapshot.WorkflowRuns[0].CompletedStages[0].Result.ToolResults[0].Content; got != large {
		t.Fatalf("expected complete workflow tool result after archive load, got %d bytes", len([]byte(got)))
	}
	if got := snapshot.Artifacts[0].Content; got != large {
		t.Fatalf("expected complete session artifact after archive load, got %d bytes", len([]byte(got)))
	}
}

func TestStateLoadMigratesOversizedSessionIntoCompressedArchive(t *testing.T) {
	large := strings.Repeat("x", maxWorkflowRunText*3)
	raw := Snapshot{
		AgentRuns: []AgentRunSnapshot{{
			ID:     "agent-1",
			Status: "completed",
			Result: &schema.AgentResult{ToolResults: []schema.ToolResult{{
				CallID:   "call-1",
				ToolName: "read_file",
				Content:  large,
			}}},
			Events: []AgentRunEventSnapshot{{Content: large}},
		}},
		Artifacts: []SessionArtifactSnapshot{{ID: "artifact-1", Content: large, Summary: large}},
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	state := New(3)
	if err := state.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rewritten) >= len(data) {
		t.Fatalf("expected session load to rewrite compacted file, before=%d after=%d", len(data), len(rewritten))
	}
	if _, err := os.Stat(fullSessionArchivePath(path)); err != nil {
		t.Fatalf("expected full compressed session archive after migration: %v", err)
	}
	snapshot := state.Snapshot()
	if got := snapshot.AgentRuns[0].Result.ToolResults[0].Content; got != large {
		t.Fatalf("expected complete loaded tool result, got %d bytes", len([]byte(got)))
	}
	if got := snapshot.Artifacts[0].Content; got != large {
		t.Fatalf("expected complete loaded artifact content, got %d bytes", len([]byte(got)))
	}
}

func TestStatePromptBudgetHistoryIsBounded(t *testing.T) {
	state := New(3)
	for i := 0; i < maxPromptBudgetHistory+5; i++ {
		state.SetPromptBudget(schema.PromptBudget{EstimatedPromptTokens: i + 1, PromptPrefixHash: "prefix"})
	}
	snapshot := state.Snapshot()
	if len(snapshot.PromptBudgets) != maxPromptBudgetHistory {
		t.Fatalf("expected bounded prompt budget history, got %d", len(snapshot.PromptBudgets))
	}
	if snapshot.PromptBudgets[0].EstimatedPromptTokens != 6 {
		t.Fatalf("expected oldest retained sample to be 6, got %#v", snapshot.PromptBudgets[0])
	}
	if snapshot.PromptBudget == nil || snapshot.PromptBudget.EstimatedPromptTokens != maxPromptBudgetHistory+5 {
		t.Fatalf("expected latest prompt budget retained, got %#v", snapshot.PromptBudget)
	}
}

func TestStateTokenUsageHistoryIsBounded(t *testing.T) {
	state := New(3)
	for i := 0; i < maxTokenUsageHistory+5; i++ {
		state.AddTokenUsage(schema.TokenUsageSample{AgentID: "agent", Mode: "chat", PromptTokens: i + 1, OutputTokens: 1})
	}
	snapshot := state.Snapshot()
	if len(snapshot.TokenUsages) != maxTokenUsageHistory {
		t.Fatalf("expected bounded token usage history, got %d", len(snapshot.TokenUsages))
	}
	if snapshot.TokenUsages[0].PromptTokens != 6 {
		t.Fatalf("expected oldest retained sample to be 6, got %#v", snapshot.TokenUsages[0])
	}
	if got := snapshot.TokenUsages[len(snapshot.TokenUsages)-1].TotalTokens; got != maxTokenUsageHistory+6 {
		t.Fatalf("expected total tokens to be filled, got %d", got)
	}
}

func TestStateWorkflowSchemaCatalogTracksTypedOutputsAndPersists(t *testing.T) {
	state := New(3)
	runID := state.StartWorkflowRun("expression-flow", "inspect target")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []WorkflowRunStageSnapshot{{
		Stage: "collect",
		OutputValues: map[string]any{
			"payload": map[string]any{"risk": "high", "score": 9},
			"tags":    []any{"api", "auth"},
		},
	}})

	catalog, ok := state.WorkflowSchema("expression-flow")
	if !ok {
		t.Fatalf("expected workflow schema catalog entry")
	}
	collect := catalog.Stages["collect"]
	if got := collect.Outputs["payload"].Fields["risk"].Type; got != "string" {
		t.Fatalf("expected payload.risk string schema, got %#v", collect.Outputs["payload"])
	}
	if got := collect.Outputs["payload"].Fields["score"].Type; got != "number" {
		t.Fatalf("expected payload.score number schema, got %#v", collect.Outputs["payload"])
	}
	if collect.Outputs["tags"].Items == nil || collect.Outputs["tags"].Items.Type != "string" {
		t.Fatalf("expected tags array item schema, got %#v", collect.Outputs["tags"])
	}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := New(3)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if catalog, ok := loaded.WorkflowSchema("expression-flow"); !ok || catalog.Stages["collect"].Outputs["payload"].Fields["risk"].Type != "string" {
		t.Fatalf("expected persisted workflow schema, got %#v ok=%t", catalog, ok)
	}
	if !loaded.ClearWorkflowSchema("expression-flow") {
		t.Fatalf("expected clear workflow schema to succeed")
	}
	if _, ok := loaded.WorkflowSchema("expression-flow"); ok {
		t.Fatalf("expected workflow schema to be cleared")
	}
	if rebuilt := loaded.RebuildWorkflowSchemas(); len(rebuilt) != 1 || rebuilt[0].Stages["collect"].Outputs["payload"].Fields["risk"].Type != "string" {
		t.Fatalf("expected rebuild from retained workflow runs, got %#v", rebuilt)
	}
}

func TestStateWorkflowSchemaImportMergesAndReplaces(t *testing.T) {
	state := New(3)
	_, err := state.ImportWorkflowSchema(WorkflowSchemaSnapshot{
		Workflow: "shared-flow",
		RunIDs:   []string{"external-1"},
		Stages: map[string]WorkflowStageSchemaSnapshot{
			"collect": {
				Stage: "collect",
				Outputs: map[string]WorkflowValueSchemaSnapshot{
					"payload": {
						Type: "object",
						Fields: map[string]WorkflowValueSchemaSnapshot{
							"risk": {Type: "string"},
						},
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("ImportWorkflowSchema: %v", err)
	}
	_, err = state.ImportWorkflowSchema(WorkflowSchemaSnapshot{
		Workflow: "shared-flow",
		RunIDs:   []string{"external-2", "external-1"},
		Stages: map[string]WorkflowStageSchemaSnapshot{
			"collect": {
				Stage: "collect",
				Outputs: map[string]WorkflowValueSchemaSnapshot{
					"payload": {
						Type: "object",
						Fields: map[string]WorkflowValueSchemaSnapshot{
							"score": {Type: "number"},
						},
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("merge ImportWorkflowSchema: %v", err)
	}
	merged, ok := state.WorkflowSchema("shared-flow")
	if !ok {
		t.Fatalf("expected imported workflow schema")
	}
	payload := merged.Stages["collect"].Outputs["payload"]
	if payload.Fields["risk"].Type != "string" || payload.Fields["score"].Type != "number" {
		t.Fatalf("expected merged payload fields, got %#v", payload)
	}
	if len(merged.RunIDs) != 2 || merged.RunIDs[0] != "external-2" || merged.RunIDs[1] != "external-1" {
		t.Fatalf("expected deduplicated imported run ids, got %#v", merged.RunIDs)
	}
	replaced, err := state.ImportWorkflowSchema(WorkflowSchemaSnapshot{
		Workflow: "shared-flow",
		Stages: map[string]WorkflowStageSchemaSnapshot{
			"report": {
				Outputs: map[string]WorkflowValueSchemaSnapshot{
					"summary": {Type: "string"},
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("replace ImportWorkflowSchema: %v", err)
	}
	if _, ok := replaced.Stages["collect"]; ok || replaced.Stages["report"].Outputs["summary"].Type != "string" {
		t.Fatalf("expected replacement schema, got %#v", replaced)
	}
	if _, err := state.ImportWorkflowSchema(WorkflowSchemaSnapshot{Workflow: "empty"}, true); err == nil {
		t.Fatalf("expected empty schema import to fail")
	}
}

func TestStateCollaborationMessagesFilterAndPersist(t *testing.T) {
	state := New(3)
	first := state.AddCollaborationMessage(CollaborationMessageSnapshot{RunID: "wf-1", Stage: "plan", FromAgent: "planner", ToAgent: "fixer", Kind: "handoff", Content: "implement this"})
	state.AddCollaborationMessage(CollaborationMessageSnapshot{RunID: "wf-2", Stage: "audit", FromAgent: "auditor", Kind: "critique", Content: "review this"})

	messages := state.CollaborationMessages(CollaborationFilter{RunID: "wf-1", Agent: "fixer"})
	if len(messages) != 1 || messages[0].ID != first.ID || messages[0].Kind != "handoff" {
		t.Fatalf("unexpected filtered messages: %#v", messages)
	}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := New(3)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.CollaborationMessages(CollaborationFilter{RunID: "wf-1"}); len(got) != 1 || got[0].Content != "implement this" {
		t.Fatalf("expected persisted collaboration message, got %#v", got)
	}
}

func TestStateCollaborationContentUsesContentAddressedStore(t *testing.T) {
	root := t.TempDir()
	store := NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	state := New(3)
	state.SetArtifactObjectStore(store)
	largeMessage := strings.Repeat("collaboration message payload ", 480)
	largeEntry := strings.Repeat("blackboard entry payload ", 480)

	message := state.AddCollaborationMessage(CollaborationMessageSnapshot{RunID: "wf-1", Stage: "plan", FromAgent: "planner", ToAgent: "fixer", Kind: "handoff", Subject: "Large handoff", Content: largeMessage})
	if message.ContentArtifactRef == "" || message.ContentHash == "" || !message.ContentExternalized || message.ContentBytes != len([]byte(largeMessage)) || message.Content == largeMessage {
		t.Fatalf("expected externalized collaboration message, got %#v", message)
	}
	entry := state.UpsertBlackboardEntry(BlackboardEntrySnapshot{Scope: "workflow", RunID: "wf-1", Stage: "plan", AgentID: "planner", Kind: "decision", Title: "Large decision", Content: largeEntry})
	if entry.ContentArtifactRef == "" || entry.ContentHash == "" || !entry.ContentExternalized || entry.ContentBytes != len([]byte(largeEntry)) || entry.Content == largeEntry {
		t.Fatalf("expected externalized blackboard entry, got %#v", entry)
	}

	summaryMessages := state.CollaborationMessages(CollaborationFilter{RunID: "wf-1"})
	if len(summaryMessages) != 1 || summaryMessages[0].Content == largeMessage || summaryMessages[0].ContentArtifactRef == "" {
		t.Fatalf("expected summary-first collaboration messages, got %#v", summaryMessages)
	}
	fullMessages := state.CollaborationMessages(CollaborationFilter{RunID: "wf-1", Content: true})
	if len(fullMessages) != 1 || fullMessages[0].Content != largeMessage {
		t.Fatalf("expected hydrated collaboration message content, got %#v", fullMessages)
	}
	summaryEntries := state.BlackboardEntries(CollaborationFilter{RunID: "wf-1"})
	if len(summaryEntries) != 1 || summaryEntries[0].Content == largeEntry || summaryEntries[0].ContentArtifactRef == "" {
		t.Fatalf("expected summary-first blackboard entries, got %#v", summaryEntries)
	}
	fullEntry, ok := state.BlackboardEntry(entry.ID)
	if !ok || fullEntry.Content != largeEntry {
		t.Fatalf("expected hydrated blackboard entry content, got %#v ok=%t", fullEntry, ok)
	}

	sessionPath := filepath.Join(root, "session.json")
	if err := state.Save(sessionPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), largeMessage) || strings.Contains(string(data), largeEntry) {
		t.Fatalf("expected compact session index to omit raw collaboration payloads")
	}

	loaded := New(3)
	loaded.SetArtifactObjectStore(store)
	if err := loaded.Load(sessionPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	restoredMessages := loaded.CollaborationMessages(CollaborationFilter{RunID: "wf-1", Content: true})
	if len(restoredMessages) != 1 || restoredMessages[0].Content != largeMessage {
		t.Fatalf("expected loaded collaboration message content, got %#v", restoredMessages)
	}
	restoredEntry, ok := loaded.BlackboardEntry(entry.ID)
	if !ok || restoredEntry.Content != largeEntry {
		t.Fatalf("expected loaded blackboard content, got %#v ok=%t", restoredEntry, ok)
	}
}

func TestStateBlackboardUpsertFilterAndDelete(t *testing.T) {
	state := New(3)
	entry := state.UpsertBlackboardEntry(BlackboardEntrySnapshot{Scope: "workflow", RunID: "wf-1", Stage: "plan", AgentID: "planner", Kind: "decision", Title: "Architecture", Content: "split backend and frontend", Tags: []string{"plan", "plan"}})
	if entry.ID == "" || entry.Scope != "workflow" || len(entry.Tags) != 1 {
		t.Fatalf("unexpected normalized entry: %#v", entry)
	}

	entry.Content = "backend first"
	updated := state.UpsertBlackboardEntry(entry)
	if updated.ID != entry.ID || updated.CreatedAt == "" || updated.UpdatedAt == "" || updated.Content != "backend first" {
		t.Fatalf("unexpected updated entry: %#v", updated)
	}

	entries := state.BlackboardEntries(CollaborationFilter{RunID: "wf-1", Kind: "decision", Agent: "planner"})
	if len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("unexpected blackboard entries: %#v", entries)
	}
	if !state.DeleteBlackboardEntry(entry.ID) {
		t.Fatalf("expected delete to succeed")
	}
	if _, ok := state.BlackboardEntry(entry.ID); ok {
		t.Fatalf("expected deleted entry to be missing")
	}
}

func TestStateRemembersApprovedToolByWorkspace(t *testing.T) {
	state := New(2)
	state.RememberApprovedTool("C:/repo", "write_file")

	if !state.HasApprovedTool("C:/repo", "write_file") {
		t.Fatal("expected remembered approval for matching workspace and tool")
	}
	if state.HasApprovedTool("C:/repo", "edit_file") {
		t.Fatal("did not expect approval for different tool")
	}
	if state.HasApprovedTool("C:/other", "write_file") {
		t.Fatal("did not expect approval for different workspace")
	}
}

func TestStatePersistsLastSkillMatchDiagnostic(t *testing.T) {
	state := New(2)
	state.SetLastSkillMatch(&schema.Skill{Name: "code-audit"}, schema.SkillMatchDiagnostic{
		Score:       3,
		KeywordHits: []string{"audit"},
		Reason:      "keywords:audit",
	})

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := New(2)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snapshot := loaded.Snapshot()
	if snapshot.LastSkill != "code-audit" || snapshot.LastSkillMatch == nil {
		t.Fatalf("expected persisted skill match, got %#v", snapshot)
	}
	if snapshot.LastSkillMatch.SkillName != "code-audit" || snapshot.LastSkillMatch.Score != 3 {
		t.Fatalf("unexpected skill diagnostic: %#v", snapshot.LastSkillMatch)
	}
}

func TestStateRemembersApprovedToolScopeByWorkspace(t *testing.T) {
	state := New(2)
	state.RememberApprovedToolScope("C:/repo", "write", "write_file")

	if !state.HasApprovedToolScope("C:/repo", "write", "write_file") {
		t.Fatal("expected remembered approval for matching workspace, kind, and tool")
	}
	if state.HasApprovedToolScope("C:/repo", "read", "write_file") {
		t.Fatal("did not expect approval for different tool kind")
	}
	if state.HasApprovedToolScope("C:/repo", "write", "edit_file") {
		t.Fatal("did not expect approval for different tool name")
	}
	if state.HasApprovedToolScope("C:/other", "write", "write_file") {
		t.Fatal("did not expect approval for different workspace")
	}

	scopes := state.ApprovedToolScopesForWorkspace("C:/repo")
	if len(scopes) != 1 || scopes[0].Kind != "write" || scopes[0].Name != "write_file" {
		t.Fatalf("unexpected approved tool scopes: %#v", scopes)
	}
}

func TestStateApprovedToolScopesIgnoreLegacyToolNames(t *testing.T) {
	state := New(2)
	state.RememberApprovedTool("C:/repo", "write_file")
	state.RememberApprovedToolScope("C:/repo", "write", "edit_file")

	scopes := state.ApprovedToolScopesForWorkspace("C:/repo")
	if len(scopes) != 1 || scopes[0].Kind != "write" || scopes[0].Name != "edit_file" {
		t.Fatalf("expected only scoped approvals, got %#v", scopes)
	}
}

func TestStateLoadKeepsDefaultsWhenFileMissing(t *testing.T) {
	state := New(2)
	path := filepath.Join(t.TempDir(), "missing.json")
	if err := state.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Mode() != "chat" {
		t.Fatalf("expected default chat mode, got %q", state.Mode())
	}
}

func TestStateSaveCreatesParentDirectory(t *testing.T) {
	state := New(2)
	state.AddPrompt("persist me")
	path := filepath.Join(t.TempDir(), "nested", "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected saved file, got %v", err)
	}
}
