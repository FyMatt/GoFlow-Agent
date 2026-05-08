package session

import (
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
