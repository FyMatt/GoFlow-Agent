package session

import (
	"os"
	"path/filepath"
	"reflect"
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
