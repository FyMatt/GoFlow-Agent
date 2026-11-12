package api

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRunDiffToolNameIncludesExactPatch(t *testing.T) {
	for _, name := range []string{"apply_patch", "file_tools/apply_patch"} {
		if !runDiffToolName(name) {
			t.Fatalf("expected %q to be recognized as a file diff tool", name)
		}
	}
}

func TestRunDiffPayloadCarriesRestoreEvidence(t *testing.T) {
	item, ok := runDiffFromToolPayload("run-1", "agent", "", "fixer", "fix", "event", "file_tools/write_file", "call-1", "", `{"relative_path":"main.go","status":"modified","change_id":"0123456789abcdef01234567","reversible":true,"recovery_scope":"file_only","diff_preview":"- old\n+ new"}`, false, false)
	if !ok {
		t.Fatal("expected file-change payload to become a diff item")
	}
	if item.ChangeID == "" || !item.Reversible || item.RecoveryScope != "file_only" {
		t.Fatalf("expected restore metadata, got %#v", item)
	}
}

func TestWorkflowRunDiffsRecoverFileChangesFromAuditTrail(t *testing.T) {
	run := session.WorkflowRunSnapshot{
		ID: "wf-1",
		CompletedStages: []session.WorkflowRunStageSnapshot{{
			Stage: "implement",
			Result: schema.AgentResult{AuditTrail: []schema.AuditEntry{{
				ToolName: "file_tools/write_file",
				Outcome:  "approved",
				Detail:   `{"relative_path":"ttlcache/cache.go","status":"modified","change_id":"change-1","diff_preview":"- old\n+ new"}`,
			}}},
		}},
	}
	diffs := workflowRunDiffs(run, workflowRunQuery{})
	if len(diffs) != 1 {
		t.Fatalf("expected one audit-backed workflow diff, got %#v", diffs)
	}
	if diffs[0].Path != "ttlcache/cache.go" || diffs[0].ChangeID != "change-1" || diffs[0].Source != "audit" {
		t.Fatalf("expected durable audit diff metadata, got %#v", diffs[0])
	}
}

func TestRunDiffDedupKeyCollapsesPersistedRepresentationsOfSameChange(t *testing.T) {
	event := runDiffItem{
		RunID: "wf-1", Stage: "implement", Source: "event", ToolName: "file_tools/apply_patch",
		ToolCallID: "patch-1", Path: "main.go", Status: "modified", ChangeID: "change-1", DiffPreview: "- old\n+ new",
	}
	audit := event
	audit.Source = "audit"
	audit.Stage = "verify"
	audit.ToolCallID = ""
	audit.Status = "changed"
	audit.DiffPreview = "truncated copy"
	if runDiffDedupKey(event) != runDiffDedupKey(audit) {
		t.Fatalf("expected event and audit representations of one change to share a key: %q != %q", runDiffDedupKey(event), runDiffDedupKey(audit))
	}

	other := event
	other.ChangeID = "change-2"
	if runDiffDedupKey(event) == runDiffDedupKey(other) {
		t.Fatal("independent changes must retain distinct diff keys")
	}
}

func TestRunDiffFromArtifactRejectsModelChangeSummaryWithoutPath(t *testing.T) {
	artifact := session.WorkflowRunArtifact{
		ID:      "change-summary",
		Kind:    "change",
		Title:   "Change",
		Summary: "Implemented and verified the requested behavior.",
	}
	if item, ok := runDiffFromArtifact("wf-1", "implement", artifact); ok {
		t.Fatalf("expected unstructured change summary to stay out of file diffs, got %#v", item)
	}
}
