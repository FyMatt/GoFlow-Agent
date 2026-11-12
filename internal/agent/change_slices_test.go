package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestCompileWorkflowChangeSlicesBindsReadHashesAndDependencies(t *testing.T) {
	hash := strings.Repeat("a", 64)
	output := "Plan\n```goflow-change-slices\n[" +
		`{"id":"storage","title":"存储迁移","depends_on":[],"input_files":["store/store.go"],"target_files":["store/store.go","store/store_test.go"],"verification_commands":["go test ./store"]},` +
		`{"id":"api","title":"接口兼容","depends_on":["storage"],"input_files":["api/api.go"],"target_files":["api/api.go"],"verification_commands":["go test ./api"]}` +
		"]\n```"
	results := []schema.ToolResult{{ToolName: "file_tools/read_file", Content: `{"relative_path":"store/store.go","sha256":"` + hash + `"}`}}
	results = append(results, schema.ToolResult{ToolName: "file_tools/read_file", Content: `{"relative_path":"api/api.go","sha256":"` + hash + `"}`})

	slices := compileWorkflowChangeSlices(output, results)
	if len(slices) != 2 || slices[1].DependsOn[0] != "storage" {
		t.Fatalf("unexpected compiled slices: %#v", slices)
	}
	if slices[0].InputHashes["store/store.go"] != hash || len(slices[0].ContractHash) != 64 || slices[0].Status != "planned" {
		t.Fatalf("expected deterministic hash-bound slice, got %#v", slices[0])
	}
}

func TestExplicitPlannedWebFilesAreNotDroppedBySourcePreference(t *testing.T) {
	paths := []string{"app.py", "static/style.css", "templates/base.html", "templates/index.html", "tests/test_ui_smoke.py"}
	slices := synthesizeChangeSlicesFromPaths(paths)
	seen := map[string]bool{}
	for _, slice := range slices {
		for _, target := range slice.TargetFiles {
			seen[target] = true
		}
	}
	for _, path := range paths {
		if !seen[path] {
			t.Errorf("explicit planned target %s was discarded: %#v", path, slices)
		}
	}
}

func TestCompileWorkflowChangeSlicesRequiresInputHashesAndBindsThemToContract(t *testing.T) {
	output := "goflow-change-slices\n[" +
		`{"id":"slice","title":"Slice","depends_on":[],"input_files":["main.go"],"target_files":["main.go"],"verification_commands":["go test ./..."]}]`
	if slices := compileWorkflowChangeSlices(output, nil); len(slices) != 0 {
		t.Fatalf("missing input hash must reject the plan: %#v", slices)
	}
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	first := compileWorkflowChangeSlices(output, []schema.ToolResult{{ToolName: "file_tools/read_file", Content: `{"relative_path":"main.go","sha256":"` + hashA + `"}`}})
	second := compileWorkflowChangeSlices(output, []schema.ToolResult{{ToolName: "file_tools/read_file", Content: `{"relative_path":"main.go","sha256":"` + hashB + `"}`}})
	if len(first) != 1 || len(second) != 1 || first[0].ContractHash == second[0].ContractHash {
		t.Fatalf("input hash must be part of the contract hash: first=%#v second=%#v", first, second)
	}
}

func TestCompileWorkflowChangeSlicesRejectsCycleAndUnsafePath(t *testing.T) {
	cycle := "goflow-change-slices\n[" +
		`{"id":"a","title":"A","depends_on":["b"],"input_files":[],"target_files":["a.go"],"verification_commands":["go test ./..."]},` +
		`{"id":"b","title":"B","depends_on":["a"],"input_files":[],"target_files":["b.go"],"verification_commands":["go test ./..."]}` +
		"]"
	if slices := compileWorkflowChangeSlices(cycle, nil); len(slices) != 0 {
		t.Fatalf("dependency cycle must be rejected: %#v", slices)
	}
	unsafe := `goflow-change-slices [{"id":"a","title":"A","depends_on":[],"input_files":[],"target_files":["../outside.go"],"verification_commands":["go test ./..."]}]`
	if slices := compileWorkflowChangeSlices(unsafe, nil); len(slices) != 0 {
		t.Fatalf("unsafe target path must be rejected: %#v", slices)
	}
}

func TestCompileWorkflowChangeSlicesSynthesizesFromTreeWhenPlanOmitsJSON(t *testing.T) {
	results := []schema.ToolResult{{
		ToolName: "file_tools/list_tree",
		Content:  `{"entries":[{"relative_path":"internal/cache/cache.go","is_dir":false},{"relative_path":"internal/config/config.go","is_dir":false}]}`,
	}}
	slices := compileWorkflowChangeSlices("Delivery plan without a fenced JSON block", results)
	if len(slices) == 0 {
		t.Fatal("tree evidence should synthesize write slices when the plan omits JSON")
	}
	for _, slice := range slices {
		if slice.ExecutionMode != "write" || len(slice.TargetFiles) == 0 || len(slice.VerificationCommands) == 0 {
			t.Fatalf("synthesized slice is incomplete: %#v", slice)
		}
		if len(slice.DependsOn) != 0 {
			t.Fatalf("synthesized slices must be independently writable, got %#v", slice)
		}
		for _, target := range slice.TargetFiles {
			if !strings.HasSuffix(target, ".go") && !strings.HasSuffix(target, ".js") && !strings.HasSuffix(target, ".md") {
				t.Fatalf("synthesized targets must be source files, got %#v", slice.TargetFiles)
			}
		}
	}
}

func TestCompileWorkflowChangeSlicesUsesPythonVerificationForFlaskEvidence(t *testing.T) {
	results := []schema.ToolResult{{
		ToolName: "file_tools/list_tree",
		Content:  `{"entries":[{"relative_path":"app.py","is_dir":false},{"relative_path":"static/style.css","is_dir":false},{"relative_path":"templates/index.html","is_dir":false}]}`,
	}}
	slices := compileWorkflowChangeSlices("Implementation Plan: refine the existing Flask UI", results)
	if len(slices) == 0 {
		t.Fatal("Flask evidence should synthesize bounded change slices")
	}
	foundApp := false
	for _, slice := range slices {
		if containsPath(slice.TargetFiles, "app.py") {
			foundApp = true
		}
		if strings.Join(slice.VerificationCommands, " ") != "python -m py_compile app.py" {
			t.Fatalf("Flask slices must not inherit Go verification, got %#v", slice)
		}
	}
	if !foundApp {
		t.Fatalf("root Flask source must remain visible to synthesized slices, got %#v", slices)
	}
}

func TestPlannedWritePathsFromTextIncludeGreenfieldTargets(t *testing.T) {
	text := "Target Files\n- `go.mod`\n- `cmd/portal/main.go`\n- `internal/app/server.go`\n- `internal/portal/portal_test.go`\n- README.md\n"
	got := plannedWritePathsFromText(text)
	joined := strings.Join(got, ",")
	for _, want := range []string{"go.mod", "cmd/portal/main.go", "internal/app/server.go", "internal/portal/portal_test.go", "README.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %s in %#v", want, got)
		}
	}
	slices := synthesizeChangeSlicesFromPaths(got)
	if len(slices) == 0 {
		t.Fatal("greenfield planned files must become write slices")
	}
	covered := false
	testCovered := false
	for _, slice := range slices {
		for _, target := range slice.TargetFiles {
			if target == "cmd/portal/main.go" || strings.HasPrefix(target, "cmd/") {
				covered = true
			}
			if target == "internal/portal/portal_test.go" {
				testCovered = true
			}
		}
	}
	if !covered {
		t.Fatalf("cmd/portal must be a write target, got %#v", slices)
	}
	if !testCovered {
		t.Fatalf("planned new tests must be write targets, got %#v", slices)
	}
}

func TestPlannedWritePathsUsePositiveScopeAndExcludeDoNotModify(t *testing.T) {
	text := `## Files Or Modules
预计修改：
- static/style.css
- templates/base.html
- templates/admin_new.html

不应修改：
- app.py
- requirements.txt

## Verification Commands
- python -m py_compile app.py
`
	got := plannedWritePathsFromText(text)
	joined := strings.Join(got, ",")
	for _, want := range []string{"static/style.css", "templates/base.html", "templates/admin_new.html"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected positive target %s in %#v", want, got)
		}
	}
	if strings.Contains(joined, "app.py") || strings.Contains(joined, "requirements.txt") {
		t.Fatalf("negative and verification paths must not become approved write targets: %#v", got)
	}
}

func TestSeedPlanChangeSliceEvidencePrefersExplicitTargetsOverReadEvidence(t *testing.T) {
	root := t.TempDir()
	state := session.New(4)
	runID := state.StartWorkflowRun("plan-implement-audit", "美化现有页面")
	runner := &WorkflowRunner{runtime: &Runtime{session: state, cfg: &config.Config{WorkspaceRoot: root}}, runID: runID}
	result := schema.AgentResult{
		Output:      "## Files Or Modules\n预计修改：\n- `static/style.css`\n- `templates/base.html`\n\n不应修改：\n- `app.py`\n",
		ToolResults: []schema.ToolResult{{ToolName: "read_file", Content: `{"relative_path":"app.py","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`}},
	}
	runner.seedPlanChangeSliceEvidence(&result)
	run, ok := state.WorkflowRun(runID)
	if !ok {
		t.Fatal("workflow run missing")
	}
	targets := make([]string, 0)
	for _, slice := range run.ChangeSlices {
		targets = append(targets, slice.TargetFiles...)
	}
	joined := strings.Join(targets, ",")
	if !strings.Contains(joined, "static/style.css") || !strings.Contains(joined, "templates/base.html") || strings.Contains(joined, "app.py") {
		t.Fatalf("explicit positive plan targets must replace incidental read evidence, got %#v", run.ChangeSlices)
	}
}

func TestCompileWorkflowChangeSlicesAcceptsNullDependsOnForGreenfield(t *testing.T) {
	output := "```goflow-change-slices\n" +
		`[{"id":"mod","title":"module","mode":"write","depends_on":null,"input_files":null,"target_files":["go.mod"],"verification_commands":["go test ./..."]}]` +
		"\n```"
	slices := compileWorkflowChangeSlices(output, nil)
	if len(slices) != 1 || slices[0].TargetFiles[0] != "go.mod" {
		t.Fatalf("null depends_on must compile greenfield writes, got %#v", slices)
	}
}

func TestSeedPlanChangeSliceEvidencePersistsGreenfieldWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# 校园网\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "实现 go.mod、cmd/portal/main.go 与 internal/portal/portal_test.go")
	runner := &WorkflowRunner{
		runtime: &Runtime{session: state, cfg: &config.Config{WorkspaceRoot: root}},
		runID:   runID,
	}
	result := schema.AgentResult{Output: "### Target Files\n- `go.mod`\n- `cmd/portal/main.go`\n- `internal/portal/portal_test.go`\n\n```goflow-change-slices\n" +
		`[{"id":"docs","title":"docs","mode":"write","depends_on":null,"input_files":null,"target_files":["README.md"],"verification_commands":["go test ./..."]}]` +
		"\n```"}
	runner.seedPlanChangeSliceEvidence(&result)
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) == 0 {
		t.Fatal("greenfield plan slices must be persisted on the workflow run")
	}
	covered := false
	testCovered := false
	for _, slice := range run.ChangeSlices {
		for _, target := range slice.TargetFiles {
			if target == "cmd/portal/main.go" || strings.HasPrefix(target, "cmd/") {
				covered = true
			}
			if target == "internal/portal/portal_test.go" {
				testCovered = true
			}
		}
	}
	if !covered {
		t.Fatalf("persisted slices must include cmd/portal, got %#v", run.ChangeSlices)
	}
	if !testCovered {
		t.Fatalf("persisted slices must include planned tests, got %#v", run.ChangeSlices)
	}
	raw, ok := extractChangeSlicesJSON(result.Output)
	if !ok {
		t.Fatal("seeded plan must keep a goflow-change-slices block")
	}
	var plans []plannedChangeSlice
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatalf("seeded JSON: %v", err)
	}
	for _, plan := range plans {
		if plan.DependsOn == nil {
			t.Fatalf("seeded depends_on must be an array, got %#v", plans)
		}
	}
}

func TestMergeWriteChangeSlicesAddsMissingSourceTargets(t *testing.T) {
	base := []session.WorkflowChangeSliceSnapshot{{
		ID: "slice-internal-auth", ExecutionMode: "write", TargetFiles: []string{"internal/auth/auth.go"},
	}}
	extra := []session.WorkflowChangeSliceSnapshot{{
		ID: "slice-internal-audit", ExecutionMode: "write", TargetFiles: []string{"internal/audit/redact.go"},
	}}
	merged := mergeWriteChangeSlices(base, extra)
	if len(merged) != 2 || merged[1].TargetFiles[0] != "internal/audit/redact.go" {
		t.Fatalf("missing redact slice must be merged into the plan, got %#v", merged)
	}
}

func TestReopenWriteSlicesForTargetsResetsImplementedAndBlocked(t *testing.T) {
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "cache", ExecutionMode: "write", TargetFiles: []string{"internal/cache/cache.go"}, Status: "implemented"},
		{ID: "auth", ExecutionMode: "write", TargetFiles: []string{"internal/auth/auth.go"}, Status: "blocked", BlockedReason: "write failed"},
		{ID: "docs", ExecutionMode: "write", TargetFiles: []string{"docs/upgrade.md"}, Status: "implemented"},
	}
	got := reopenWriteSlicesForTargets(slices, []string{"internal/cache/cache.go", "internal/auth/auth.go"})
	if got[0].Status != "pending" || got[1].Status != "pending" || got[1].BlockedReason != "" {
		t.Fatalf("failing write slices must reopen for another patch: %#v", got)
	}
	if got[2].Status != "implemented" {
		t.Fatalf("unrelated implemented slice must stay locked: %#v", got[2])
	}
}

func TestCompileWorkflowChangeSlicesAllowsTreeEvidenceWithoutReadHash(t *testing.T) {
	output := "goflow-change-slices\n[" +
		`{"id":"cache","title":"cache","mode":"write","depends_on":[],"input_files":["internal/cache/cache.go"],"target_files":["internal/cache/cache.go"],"verification_commands":["go test ./internal/cache"]}]`
	results := []schema.ToolResult{{
		ToolName: "file_tools/list_tree",
		Content:  `{"entries":[{"relative_path":"internal/cache","is_dir":true},{"relative_path":"internal/cache/cache.go","is_dir":false}]}`,
	}}
	slices := compileWorkflowChangeSlices(output, results)
	if len(slices) != 1 || slices[0].TargetFiles[0] != "internal/cache/cache.go" {
		t.Fatalf("tree evidence should allow compiling slices without a prior read hash: %#v", slices)
	}
}

func TestCompileWorkflowChangeSlicesBindsPlaceholderTargetsFromTreeEvidence(t *testing.T) {
	output := "goflow-change-slices\n[" +
		`{"id":"config_migration","title":"v1 to v2 configuration migration","mode":"write","depends_on":[],"input_files":["go.mod"],"target_files":["<to-be-discovered config package files>"],"verification_commands":["go test ./..."]},` +
		`{"id":"ttl_cache_race_fix","title":"make TTL cache concurrency-safe","mode":"write","depends_on":["config_migration"],"input_files":["go.mod"],"target_files":["<to-be-discovered cache implementation>"],"verification_commands":["go test ./..."]},` +
		`{"id":"state_api_page_localization","title":"localize user-visible states in API and pages","mode":"write","depends_on":["ttl_cache_race_fix"],"input_files":["go.mod"],"target_files":["<to-be-discovered page files>"],"verification_commands":["go test ./..."]}` +
		"]"
	hash := strings.Repeat("a", 64)
	results := []schema.ToolResult{
		{ToolName: "file_tools/read_file", Content: `{"relative_path":"go.mod","sha256":"` + hash + `"}`},
		{ToolName: "file_tools/list_tree", Content: `{"entries":[{"relative_path":"internal/config","is_dir":true},{"relative_path":"internal/cache","is_dir":true},{"relative_path":"internal/api","is_dir":true},{"relative_path":"web","is_dir":true},{"relative_path":"web/app.js","is_dir":false},{"relative_path":"fixtures/config","is_dir":true}]}`},
	}
	slices := compileWorkflowChangeSlices(output, results)
	if len(slices) != 3 {
		t.Fatalf("placeholder targets should bind to repository evidence: %#v", slices)
	}
	if !containsPath(slices[0].TargetFiles, "internal/config") || containsPath(slices[0].TargetFiles, "fixtures/config") {
		t.Fatalf("config slice should bind the source package, not fixtures: %#v", slices[0].TargetFiles)
	}
	if !containsPath(slices[1].TargetFiles, "internal/cache") {
		t.Fatalf("cache slice should bind internal/cache: %#v", slices[1].TargetFiles)
	}
	if !containsPath(slices[2].TargetFiles, "web") && !containsPath(slices[2].TargetFiles, "web/app.js") && !containsPath(slices[2].TargetFiles, "internal/api") {
		t.Fatalf("page slice should bind web or api evidence: %#v", slices[2].TargetFiles)
	}
}

func TestCompileWorkflowChangeSlicesRejectsPlaceholderTargetsWithoutEvidence(t *testing.T) {
	output := "goflow-change-slices\n[" +
		`{"id":"config_migration","title":"configuration migration","mode":"write","depends_on":[],"input_files":[],"target_files":["<to-be-discovered config package files>"],"verification_commands":["go test ./..."]}]`
	if slices := compileWorkflowChangeSlices(output, nil); len(slices) != 0 {
		t.Fatalf("placeholder targets without repository evidence must not compile: %#v", slices)
	}
}

func containsPath(paths []string, wanted string) bool {
	for _, path := range paths {
		if path == wanted {
			return true
		}
	}
	return false
}
