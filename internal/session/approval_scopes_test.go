package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestApprovalScopeFingerprintCanonicalizesArgumentsAndCommandCwd(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	base := ApprovalScopeInput{
		Workspace:    `C:\repo`,
		Target:       "local",
		AgentID:      "fixer",
		ToolKind:     "exec",
		ToolName:     "run_command",
		ResourcePins: []ResourcePin{{Kind: "agent", Name: "fixer", Version: "sha256:v1"}},
		Arguments:    json.RawMessage(`{"program":"git","args":["status","--short"],"cwd":"."}`),
	}
	first, err := NewApprovalScope(base, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	base.Arguments = json.RawMessage(`{ "cwd": "C:/repo", "args": ["status", "--short"], "program": "git" }`)
	second, err := NewApprovalScope(base, now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("equivalent normalized calls must share a fingerprint: %s != %s", first.Fingerprint, second.Fingerprint)
	}
	if first.Cwd != "C:/repo" || first.Program != "git" {
		t.Fatalf("unexpected normalized command details: %#v", first)
	}
}

func TestApprovalScopeFingerprintBindsTrustedContext(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	base := ApprovalScopeInput{
		Workspace:     "C:/repo",
		Target:        "local",
		AgentID:       "fixer",
		WorkflowName:  "delivery",
		WorkflowStage: "implement",
		ToolKind:      "exec",
		ToolName:      "run_command",
		ResourcePins:  []ResourcePin{{Kind: "workflow", Name: "delivery", Version: "sha256:v1"}},
		Arguments:     json.RawMessage(`{"program":"git","args":["status"],"cwd":"."}`),
	}
	fingerprint := approvalFingerprintForTest(t, base, now)
	variants := []ApprovalScopeInput{
		approvalInputWith(base, func(value *ApprovalScopeInput) {
			value.Arguments = json.RawMessage(`{"program":"git","args":["diff"],"cwd":"."}`)
		}),
		approvalInputWith(base, func(value *ApprovalScopeInput) {
			value.Arguments = json.RawMessage(`{"program":"git","args":["status"],"cwd":"subdir"}`)
		}),
		approvalInputWith(base, func(value *ApprovalScopeInput) { value.Workspace = "C:/other" }),
		approvalInputWith(base, func(value *ApprovalScopeInput) { value.Target = "server-a" }),
		approvalInputWith(base, func(value *ApprovalScopeInput) { value.AgentID = "auditor" }),
		approvalInputWith(base, func(value *ApprovalScopeInput) { value.WorkflowStage = "verify" }),
		approvalInputWith(base, func(value *ApprovalScopeInput) {
			value.ResourcePins = []ResourcePin{{Kind: "workflow", Name: "delivery", Version: "sha256:v2"}}
		}),
	}
	for i, variant := range variants {
		if got := approvalFingerprintForTest(t, variant, now); got == fingerprint {
			t.Fatalf("variant %d unexpectedly retained the trusted-context fingerprint", i)
		}
	}
}

func TestApprovalScopeExpiryAndSessionPersistence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	input := ApprovalScopeInput{
		Workspace: "C:/repo",
		Target:    "local",
		AgentID:   "fixer",
		ToolKind:  "write",
		ToolName:  "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}
	active, err := NewApprovalScope(input, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := NewApprovalScope(approvalInputWith(input, func(value *ApprovalScopeInput) {
		value.Arguments = json.RawMessage(`{"path":"old.txt","content":"old"}`)
	}), now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	state := New(4)
	state.RememberApprovalScope(active)
	state.RememberApprovalScope(expired)
	if !state.HasApprovalScope(active.Fingerprint, now) {
		t.Fatal("expected active exact scope")
	}
	if state.HasApprovalScope(active.Fingerprint, active.ExpiresAt) || state.HasApprovalScope(expired.Fingerprint, now) {
		t.Fatal("expired approval scope must not authorize a call")
	}

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded := New(4)
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if !loaded.HasApprovalScope(active.Fingerprint, now) {
		t.Fatal("expected exact scope to survive session save/load")
	}
	if scopes := loaded.ApprovalScopes(now); len(scopes) != 1 || scopes[0].Fingerprint != active.Fingerprint {
		t.Fatalf("unexpected restored scopes: %#v", scopes)
	}
}

func approvalFingerprintForTest(t *testing.T, input ApprovalScopeInput, now time.Time) string {
	t.Helper()
	scope, err := NewApprovalScope(input, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return scope.Fingerprint
}

func approvalInputWith(input ApprovalScopeInput, mutate func(*ApprovalScopeInput)) ApprovalScopeInput {
	input.ResourcePins = CopyResourcePins(input.ResourcePins)
	input.Arguments = append(json.RawMessage(nil), input.Arguments...)
	mutate(&input)
	return input
}
