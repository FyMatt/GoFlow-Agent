package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestPublicTestFailureSourceContextIncludesImplementationAndTest(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/cache/cache.go", "package cache\nfunc Get() {}\n")
	write("internal/cache/cache_test.go", "package cache\nfunc TestCopyAndExpiry(t *testing.T) { t.Fatal(\"Xalue true\") }\n")
	got := publicTestFailureSourceContext(root, []string{"internal/cache/cache.go"})
	if !strings.Contains(got, "internal/cache/cache.go") || !strings.Contains(got, "internal/cache/cache_test.go") {
		t.Fatalf("retry context must include implementation and public test: %s", got)
	}
	if !strings.Contains(got, "func Get()") || !strings.Contains(got, "TestCopyAndExpiry") {
		t.Fatalf("retry context must include source excerpts: %s", got)
	}
}

func TestWriteSliceTargetsMissingChangesFromFailedPublicTests(t *testing.T) {
	if got := implementationPathForPublicTest("internal/config/config_test.go"); got != "internal/config/config.go" {
		t.Fatalf("config_test.go should map to config.go, got %q", got)
	}
	if got := implementationPathForPublicTest("web/app.test.js"); got != "web/app.js" {
		t.Fatalf("app.test.js should map to app.js, got %q", got)
	}
	needed := publicTestFailureImplementationPaths([]schema.Verification{{
		Kind: "command", Status: "failed", Detail: "--- FAIL: TestLoadV1MigratesToCurrent\n    config_test.go:8: v1 not migrated\nFAIL\tinternal/config",
	}})
	if !containsPath(needed, "config.go") && !containsPath(needed, "internal/config") {
		t.Fatalf("expected implementation path from failing public test, got %#v", needed)
	}
	slices := []session.WorkflowChangeSliceSnapshot{{
		ID: "slice-internal-config", ExecutionMode: "write", TargetFiles: []string{"internal/config/config.go"},
	}, {
		ID: "slice-internal-cache", ExecutionMode: "write", TargetFiles: []string{"internal/cache/cache.go"},
	}}
	missing := writeSliceTargetsMissingChanges(slices, []string{"internal/cache/cache.go"}, []string{"config.go", "internal/cache/cache.go"})
	if len(missing) != 1 || missing[0] != "internal/config/config.go" {
		t.Fatalf("unchanged failing config.go must be retried, got %#v", missing)
	}
	stillFailing := writeSliceTargetsStillFailing(slices, []string{"config.go", "internal/cache/cache.go"})
	if len(stillFailing) != 2 {
		t.Fatalf("wrong implementations must still retry write-slice targets, got %#v", stillFailing)
	}
	redactNeeded := publicTestFailureImplementationPaths([]schema.Verification{{
		Kind: "command", Status: "failed",
		Detail: "--- FAIL: TestRedactsSecrets (0.00s)\n    redact_test.go:5: secret leaked: user=a token=secret-value\nFAIL\tp7.local/secureapp/internal/audit\t0.935s\n",
	}})
	if !containsPath(redactNeeded, "redact.go") && !containsPath(redactNeeded, "internal/audit") {
		t.Fatalf("redact public-test failure must map to audit implementation, got %#v", redactNeeded)
	}
	redactSlices := []session.WorkflowChangeSliceSnapshot{{
		ID: "slice-internal-audit", ExecutionMode: "write", TargetFiles: []string{"internal/audit/redact.go"},
	}}
	if got := writeSliceTargetsStillFailing(redactSlices, redactNeeded); len(got) != 1 || got[0] != "internal/audit/redact.go" {
		t.Fatalf("failing audit package must retry redact.go, got %#v from needed %#v", got, redactNeeded)
	}
	compileNeeded := publicTestFailureImplementationPaths([]schema.Verification{{
		Kind: "command", Status: "failed",
		Detail: "# p7.local/secureapp/internal/request [p7.local/secureapp/internal/request.test]\ninternal\\request\\header.go:7:14: undefined: errors\nFAIL\tp7.local/secureapp/internal/request [build failed]\n",
	}})
	if !containsPath(compileNeeded, "internal/request/header.go") {
		t.Fatalf("compile error must retry header.go, got %#v", compileNeeded)
	}
	unmatched := writeSliceTargetsStillFailing([]session.WorkflowChangeSliceSnapshot{{
		ID: "slice-docs", ExecutionMode: "write", TargetFiles: []string{"docs/runbook.md"},
	}}, []string{"internal/rollout/rollout.go"})
	if len(unmatched) != 1 || unmatched[0] != "internal/rollout/rollout.go" {
		t.Fatalf("failing public tests must retry implementation paths even when slices omit them, got %#v", unmatched)
	}
	detail := publicTestFailureRetryDetail([]schema.Verification{{
		Kind: "command", Status: "failed",
		Detail: "--- FAIL: TestStopsOnInjectedHealthFailure\nrollout.Result{RolledBack:[]string(nil), Failed:\"\"}",
	}})
	if !strings.Contains(detail, "RolledBack") {
		t.Fatalf("retry detail must include the public test failure output, got %q", detail)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "request"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "request", "header.go"), []byte("package request\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "request", "header_test.go"), []byte("package request\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expanded := expandImplementationWritePaths(root, []string{"internal/request"})
	if !containsPath(expanded, "internal/request/header.go") {
		t.Fatalf("package failure must expand to header.go, got %#v", expanded)
	}
	headerSlices := writeSlicesForImplementationPaths([]string{"internal/request/header.go"})
	if len(headerSlices) != 1 || headerSlices[0].ExecutionMode != "write" || headerSlices[0].TargetFiles[0] != "internal/request/header.go" {
		t.Fatalf("expected a write slice for header.go, got %#v", headerSlices)
	}
	merged := mergeWriteChangeSlices([]session.WorkflowChangeSliceSnapshot{{
		ID: "slice-docs", ExecutionMode: "write", TargetFiles: []string{"docs/runbook.md"},
	}}, headerSlices)
	if got := writeSliceTargetsStillFailing(merged, []string{"internal/request/header.go"}); len(got) != 1 {
		t.Fatalf("merged header slice must be retryable, got %#v from %#v", got, merged)
	}
}

func TestRequestedDeliverableContentValidRejectsWrongFormat(t *testing.T) {
	if requestedDeliverableContentValid("deliverables/evidence.json", "# 研究报告\n来源去重") {
		t.Fatal("markdown must not satisfy a json deliverable")
	}
	if !requestedDeliverableContentValid("deliverables/evidence.json", `{"sources":[]}`) {
		t.Fatal("object json must be accepted")
	}
	if requestedDeliverableContentValid("deliverables/claims.csv", `{"sources":[]}`) {
		t.Fatal("json must not satisfy a csv deliverable")
	}
	if !requestedDeliverableContentValid("deliverables/claims.csv", "claim_id,text,status") {
		t.Fatal("csv header must be accepted")
	}
	if !requestedDeliverableContentValid("docs/runbook.md", "# 运行手册") {
		t.Fatal("markdown deliverable should accept prose")
	}
	if requestedDeliverableContentValid("docs/upgrade.md", "# 升级说明\n\n待补充。\n") {
		t.Fatal("placeholder markdown must be rejected")
	}
	if requestedDeliverableContentValid("deliverables/report.docx", "short") {
		t.Fatal("tiny office payload is not a real document")
	}
	if !requestedDeliverableContentValid("deliverables/report.pdf", strings.Repeat("x", 40)) {
		t.Fatal("office/pdf deliverable must accept a non-trivial payload")
	}
}

func TestWorkflowGraphStageRecordsDiscoveredVerificationOnVerifyOnly(t *testing.T) {
	if workflowGraphStageRecordsDiscoveredVerification("implement") {
		t.Fatal("implement must not persist in-progress discovered tests")
	}
	if !workflowGraphStageUsesWorkspaceVerification("implement") {
		t.Fatal("implement still needs the discovered-command prompt")
	}
	for _, stage := range []string{"verify", "repair", "recovery-verify"} {
		if !workflowGraphStageRecordsDiscoveredVerification(stage) {
			t.Fatalf("%s should persist discovered verification", stage)
		}
	}
}

func TestDiscoverWorkspaceVerificationCommandsFindsGoAndBoundedNodeTests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.test.js"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "p7_hidden.test.js"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "skip.test.js"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := discoverWorkspaceVerificationCommands(root)
	joined := strings.Join(got, "\n")
	if !containsWorkspaceCommand(got, "go test ./...") || !containsWorkspaceCommand(got, "node web/app.test.js") {
		t.Fatalf("expected go and public node tests, got %#v", got)
	}
	if strings.Contains(joined, "p7_hidden") || strings.Contains(joined, "node_modules") {
		t.Fatalf("hidden or vendored tests must stay out of discovery: %#v", got)
	}
}

func TestWorkflowGraphPublicTestAcceptanceContextIncludesAssertedLiterals(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "api", "status_test.go"), []byte("func TestCurrent(t *testing.T){ if got.State!=\"ready\" { t.Fatal(got) } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.test.js"), []byte("if(!got.includes('就绪')) throw new Error(got)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "p7_hidden.test.js"), []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := workflowGraphPublicTestAcceptanceContext(root)
	if !strings.Contains(prompt, `got.State!="ready"`) || !strings.Contains(prompt, "就绪") || !strings.Contains(prompt, "status_test.go") {
		t.Fatalf("implement prompt should include public asserted literals, got %q", prompt)
	}
	if strings.Contains(prompt, "p7_hidden") {
		t.Fatalf("hidden tests must stay out of the implement prompt: %q", prompt)
	}
}

func TestWorkflowGraphWorkspaceVerificationPromptPreservesExpectedStrings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.test.js"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := workflowGraphWorkspaceVerificationPrompt(root)
	if !strings.Contains(prompt, "node web/app.test.js") || !strings.Contains(prompt, "expected strings") {
		t.Fatalf("prompt should require discovered tests and literal expected strings, got %q", prompt)
	}
}

func TestWorkflowGraphFailedPublicTestContextIncludesPublicTestFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "cache", "cache_test.go"), []byte("func TestCopyAndExpiry(t *testing.T){ t.Fatal(\"expired entries remain: 1\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := workflowGraphFailedPublicTestContext(root, []WorkflowStageResult{{
		Result: schema.AgentResult{Verification: []schema.Verification{{
			Kind: "command", Status: "failed", Detail: "go test ./...\n--- FAIL: TestCopyAndExpiry (0.00s)\n    cache_test.go:1: expired entries remain: 1\n",
		}}},
	}})
	if !strings.Contains(prompt, "expired entries remain") || !strings.Contains(prompt, "Public test") || !strings.Contains(prompt, "TestCopyAndExpiry") {
		t.Fatalf("repair context should include the failing public test, got %q", prompt)
	}
	if strings.Contains(prompt, "p7_hidden") {
		t.Fatalf("hidden tests must stay out of the prompt: %q", prompt)
	}
}

func TestRunDiscoveredWorkspaceVerificationPassesMatchingNodeTest(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute bounded workspace tests")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{\"type\":\"module\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.js"), []byte("export function renderStatus(){return '就绪 缓存 2';}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.test.js"), []byte("import {renderStatus} from './app.js'; const got=renderStatus(); if(!got.includes('就绪')) throw new Error('missing 就绪: '+got);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runDiscoveredWorkspaceVerification(root)
	if len(got) != 1 || got[0].Status != "passed" {
		t.Fatalf("matching public node test should pass, got %#v", got)
	}
}

func TestRunDiscoveredWorkspaceVerificationReportsFailingNodeTest(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute bounded workspace tests")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{\"type\":\"module\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.js"), []byte("export function renderStatus(){return '缓存 2';}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "app.test.js"), []byte("import {renderStatus} from './app.js'; const got=renderStatus(); if(!got.includes('就绪')) throw new Error('missing 就绪: '+got);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runDiscoveredWorkspaceVerification(root)
	if len(got) != 1 || got[0].Status != "failed" || !strings.Contains(got[0].Detail, "就绪") {
		t.Fatalf("discovered node test should fail with the public expected term, got %#v", got)
	}
}

func containsWorkspaceCommand(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestMissingRequestedTestCoverageFailsEmptyGoModule(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module campus-mvp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal.go"), []byte("package portal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := "提供 go test 覆盖：错误密码登录失败。写 internal/portal/portal_test.go。"
	got := missingRequestedTestCoverage(root, request)
	if len(got) != 1 || got[0] != "internal/portal/portal_test.go" {
		t.Fatalf("empty module must fail the requested-test gate, got %#v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal_test.go"), []byte("package portal\nimport \"testing\"\nfunc TestWrongPasswordLoginFails(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if missingRequestedTestCoverage(root, request) != nil {
		t.Fatal("existing public tests must satisfy the gate")
	}
	if missingRequestedTestCoverage(root, "implement the cache without mentioning go test") != nil {
		t.Fatal("requests that do not ask for go test coverage must not fail this gate")
	}
}

func TestMutationStagePublicTestRetryErrorWhenGoTestHasNoFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module campus-mvp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal.go"), []byte("package portal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := "提供 go test 覆盖错误密码登录失败，目标文件 internal/portal/portal_test.go"
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", request)
	runner := &WorkflowRunner{
		runtime: &Runtime{session: state, cfg: &config.Config{WorkspaceRoot: root}},
		runID:   runID,
	}
	err := runner.mutationStagePublicTestRetryError("implement")
	if err == nil || !strings.Contains(err.Error(), "no test files") || !strings.Contains(err.Error(), "internal/portal/portal_test.go") {
		t.Fatalf("implement must retry when go test has no files, got %v", err)
	}
}
