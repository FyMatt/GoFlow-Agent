package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

var publicTestPathPattern = regexp.MustCompile(`(?i)(?:^|[^\w./\\])((?:[A-Za-z0-9._-]+[\\/])*[A-Za-z0-9._-]+(?:_test\.go|\.test\.js|\.spec\.js))`)
var publicTestPackageFailPattern = regexp.MustCompile(`(?m)^FAIL\t[^\s]*?((?:internal|cmd|pkg)/[A-Za-z0-9._/-]+)`)
var publicGoCompilePathPattern = regexp.MustCompile(`(?i)((?:[A-Za-z0-9._-]+[\\/])+[A-Za-z0-9._-]+\.go):\d+`)

func applyWorkflowVerificationPrerequisite(stage workflowGraphStage, completed []WorkflowStageResult, result *WorkflowStageResult) {
	if result == nil || result.Result.Incomplete {
		return
	}
	source := ""
	switch stage.Name {
	case "verify":
		source = "implement"
	case "recovery-verify":
		source = "repair"
	default:
		return
	}
	prior, ok := workflowGraphLatestStageResultByName(completed, source)
	if !ok || !workflowGraphHasUnappliedExactPatch(prior.Result) {
		return
	}
	reason := "验证前提未满足：前序精确补丁失败且没有成功写入。现有项目测试通过不能证明本次需求已实现。"
	result.Status = "warning"
	result.Result.Output = reason + "\n\n模型检查报告（仅供核对已有项目状态）：\n" + result.Result.Output
	check := schema.Verification{Kind: "implementation_prerequisite", Status: "failed", Detail: reason}
	result.Result.Verification = append(result.Result.Verification, check)
	result.Output.Verification = append(result.Output.Verification, check)
	result.Output.RawOutput = result.Result.Output
	result.Output.Summary = reason
	workflowStageSetOutputValue(&result.Output, "verification", result.Result.Output)
	workflowStageSetOutputValue(&result.Output, "summary", reason)
	workflowStageSetOutputValue(&result.Output, "decision", "blocked")
	applyWorkflowStageCanonicalOutputFields(&result.Output)
}

func discoverWorkspaceVerificationCommands(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	commands := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(command string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		if _, exists := seen[command]; exists {
			return
		}
		seen[command] = struct{}{}
		commands = append(commands, command)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		add("go test ./...")
	}
	pythonTests, unittestTests := false, false
	filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || len(commands) >= 8 {
			return filepath.SkipAll
		}
		name := entry.Name()
		if entry.IsDir() {
			switch strings.ToLower(name) {
			case ".git", ".goflow", ".cache", ".venv", "venv", "__pycache__", "node_modules", "vendor", "task-staging":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(strings.ToLower(rel), "p7_hidden") {
			return nil
		}
		if strings.HasSuffix(name, ".py") && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")) {
			pythonTests = true
			if info, err := entry.Info(); err == nil && info.Size() <= 65536 {
				if data, err := os.ReadFile(path); err == nil && (bytes.Contains(data, []byte("import unittest")) || bytes.Contains(data, []byte("from unittest import"))) {
					unittestTests = true
				}
			}
		}
		args := []string{rel}
		if commandpolicy.Classify("node", args).Class != commandpolicy.ClassTest {
			return nil
		}
		add("node " + rel)
		return nil
	})
	if pythonTests {
		_, pytestConfig := os.Stat(filepath.Join(root, "pytest.ini"))
		pyproject, _ := os.ReadFile(filepath.Join(root, "pyproject.toml"))
		if unittestTests && pytestConfig != nil && !bytes.Contains(pyproject, []byte("[tool.pytest")) {
			directory := "."
			if info, err := os.Stat(filepath.Join(root, "tests")); err == nil && info.IsDir() {
				directory = "tests"
			}
			add("python -m unittest discover -s " + directory + " -v")
		} else {
			add("python -m pytest -q")
		}
	}
	sort.Strings(commands)
	return commands
}

func workflowGraphWorkspaceVerificationPrompt(root string) string {
	commands := discoverWorkspaceVerificationCommands(root)
	if len(commands) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nWorkspace-discovered verification commands (run each once unless already passed with the same workspace fingerprint):\n")
	for _, command := range commands {
		builder.WriteString("- ")
		builder.WriteString(command)
		builder.WriteString("\n")
	}
	builder.WriteString("Existing tests encode acceptance terms; preserve their expected strings instead of substituting synonyms.\n")
	builder.WriteString("Python commands must use the existing project virtual environment (.venv or venv); the command tools resolve bare python to it. Use the discovered test runner, not an invented pytest dependency. If a module is missing, inspect the interpreter and existing environment before retrying; do not repeat the same failed command without changing its cause. Web layout checks must measure rendered overflow and affected elements, not require an exact CSS spelling such as minmax(0, 1fr).\n")
	builder.WriteString("User-visible Chinese belongs only in the page or renderer; keep API, struct, and JSON literals that public tests already assert.\n")
	builder.WriteString("If a public test loads a v1 fixture and requires CurrentVersion, the loader must upgrade Version and fill missing defaults; leaving the decoded v1 struct unchanged fails acceptance.\n")
	builder.WriteString("Implementation and repair stages must read the target files and mutate them with apply_patch or write_file before reporting; a narrative is not a workspace change.\n")
	builder.WriteString("If a public test reports a leaked secret, remove the original secret substrings entirely; prefixing [REDACTED] while leaving the secret text still fails.\n")
	builder.WriteString("Patches must compile: add required imports, and treat undefined names or build failed packages as blocking until go test ./... passes.\n")
	builder.WriteString("The goflow-change-slices fenced block is plan metadata, not a workspace file; do not create goflow-change-slices.md.\n")
	return builder.String()
}

func discoverPublicTestFiles(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	files := make([]string, 0, 4)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || len(files) >= 4 {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".goflow", ".cache", "node_modules", "vendor", "task-staging":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(strings.ToLower(rel), "p7_hidden") || !isExistingAcceptanceTestPath(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}

func workflowRequestRequiresTestCoverage(request string) bool {
	text := strings.ToLower(request)
	if strings.Contains(request, "单元测试") || strings.Contains(text, "unit test") || strings.Contains(text, "httptest") || strings.Contains(text, "_test.go") {
		return true
	}
	if !strings.Contains(text, "go test") {
		return false
	}
	return strings.Contains(request, "覆盖") ||
		strings.Contains(request, "错误密码") ||
		strings.Contains(text, "coverage")
}

func requestedTestWritePaths(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, path := range plannedWritePathsFromText(text) {
		if !skipChangeSliceTestPath(path) {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func missingRequestedTestCoverage(root, request string) []string {
	if !workflowRequestRequiresTestCoverage(request) {
		return nil
	}
	if len(discoverPublicTestFiles(root)) > 0 {
		return nil
	}
	if paths := requestedTestWritePaths(request); len(paths) > 0 {
		return paths
	}
	for _, path := range plannedWritePathsFromText(request) {
		if !strings.HasSuffix(strings.ToLower(path), ".go") || skipChangeSliceTestPath(path) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if dir == "." || dir == "" {
			return []string{base + "_test.go"}
		}
		return []string{dir + "/" + base + "_test.go"}
	}
	return []string{"internal/app/app_test.go"}
}

func workflowGraphPublicTestAcceptanceContext(root string) string {
	files := discoverPublicTestFiles(root)
	if len(files) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nExisting public tests. Keep every asserted literal; change the implementation that they compile against, not the tests:\n")
	remaining := 1600
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 || remaining <= 0 {
			continue
		}
		content := string(data)
		if len(content) > 500 {
			content = content[:500]
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		remaining -= len(content)
		builder.WriteString("\n")
		builder.WriteString(rel)
		builder.WriteString(":\n")
		builder.WriteString(content)
		builder.WriteString("\n")
	}
	return builder.String()
}

func workflowGraphFailedPublicTestContext(root string, completed []WorkflowStageResult) string {
	details := make([]string, 0, 4)
	files := make([]string, 0, 4)
	seenFile := make(map[string]struct{}, 4)
	for _, stage := range completed {
		for _, item := range append(append([]schema.Verification(nil), stage.Result.Verification...), stage.Output.Verification...) {
			if !strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
				continue
			}
			detail := strings.TrimSpace(item.Detail)
			if detail == "" {
				continue
			}
			if len(details) < 4 {
				details = append(details, truncateSummary(detail))
			}
			for _, match := range publicTestPathPattern.FindAllStringSubmatch(detail, 8) {
				rel := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(match[1], "\\", "/")))
				if rel == "" || strings.Contains(strings.ToLower(rel), "p7_hidden") {
					continue
				}
				if _, exists := seenFile[rel]; exists {
					continue
				}
				seenFile[rel] = struct{}{}
				files = append(files, rel)
			}
		}
	}
	if len(details) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nFailed workspace verification evidence. Fix the implementation so these public tests pass; do not delete, skip, or rewrite the tests:\n")
	for _, detail := range details {
		builder.WriteString("---\n")
		builder.WriteString(detail)
		builder.WriteString("\n")
	}
	for _, rel := range files {
		if len(rel) == 0 || root == "" {
			continue
		}
		path := resolvePublicTestFile(root, rel)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		if display, err := filepath.Rel(root, path); err == nil && display != "" {
			rel = filepath.ToSlash(display)
		}
		content := string(data)
		if len(content) > 1600 {
			content = content[:1600]
		}
		builder.WriteString("\nPublic test ")
		builder.WriteString(rel)
		builder.WriteString(":\n")
		builder.WriteString(content)
		builder.WriteString("\n")
	}
	return builder.String()
}

func resolvePublicTestFile(root, rel string) string {
	candidate := filepath.Join(root, filepath.FromSlash(rel))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	base := filepath.Base(rel)
	var found string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".goflow", ".cache", "node_modules", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !strings.EqualFold(entry.Name(), base) || strings.Contains(strings.ToLower(path), "p7_hidden") {
			return nil
		}
		found = path
		return filepath.SkipAll
	})
	return found
}

func workflowGraphStageUsesWorkspaceVerification(stageName string) bool {
	switch strings.ToLower(strings.TrimSpace(stageName)) {
	case "implement", "fix", "repair", "verify", "recovery-verify":
		return true
	default:
		return false
	}
}

func implementationPathForPublicTest(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." {
		return ""
	}
	base := filepath.Base(rel)
	dir := filepath.ToSlash(filepath.Dir(rel))
	switch {
	case strings.HasSuffix(strings.ToLower(base), "_test.go"):
		impl := strings.TrimSuffix(base, "_test.go") + ".go"
		if dir == "." {
			return impl
		}
		return dir + "/" + impl
	case strings.HasSuffix(strings.ToLower(base), ".test.js"):
		impl := strings.TrimSuffix(base, ".test.js") + ".js"
		if dir == "." {
			return impl
		}
		return dir + "/" + impl
	default:
		return rel
	}
}

func publicTestFailureImplementationPaths(verifications []schema.Verification) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, item := range verifications {
		if !strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
			continue
		}
		for _, match := range publicTestPathPattern.FindAllStringSubmatch(item.Detail, 8) {
			impl := implementationPathForPublicTest(match[1])
			if impl == "" || strings.Contains(strings.ToLower(impl), "p7_hidden") {
				continue
			}
			if _, exists := seen[impl]; exists {
				continue
			}
			seen[impl] = struct{}{}
			out = append(out, impl)
		}
		for _, match := range publicTestPackageFailPattern.FindAllStringSubmatch(item.Detail, 8) {
			impl := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(match[1]), "\\", "/")))
			if impl == "" || strings.Contains(strings.ToLower(impl), "p7_hidden") {
				continue
			}
			if _, exists := seen[impl]; exists {
				continue
			}
			seen[impl] = struct{}{}
			out = append(out, impl)
		}
		for _, match := range publicGoCompilePathPattern.FindAllStringSubmatch(item.Detail, 8) {
			impl := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(match[1]), "\\", "/")))
			if impl == "" || strings.Contains(strings.ToLower(impl), "p7_hidden") || strings.HasSuffix(strings.ToLower(impl), "_test.go") {
				continue
			}
			if _, exists := seen[impl]; exists {
				continue
			}
			seen[impl] = struct{}{}
			out = append(out, impl)
		}
	}
	sort.Strings(out)
	return out
}

func publicTestFailureSourceContext(root string, needed []string) string {
	root = strings.TrimSpace(root)
	if root == "" || len(needed) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Failing public tests and current implementation excerpts. Patch these files until the existing tests pass; do not rewrite the tests.\n")
	seen := make(map[string]struct{})
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
		if rel == "" || rel == "." {
			return
		}
		if _, exists := seen[rel]; exists {
			return
		}
		seen[rel] = struct{}{}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || len(data) == 0 {
			return
		}
		text := string(data)
		if len(text) > 1600 {
			text = text[:1600] + "\n..."
		}
		builder.WriteString("\n--- ")
		builder.WriteString(rel)
		builder.WriteString(" ---\n")
		builder.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			builder.WriteString("\n")
		}
	}
	for _, path := range needed {
		if len(seen) >= 8 {
			break
		}
		add(path)
		dir := filepath.ToSlash(filepath.Dir(path))
		base := filepath.Base(path)
		switch {
		case strings.HasSuffix(strings.ToLower(base), ".go") && !strings.HasSuffix(strings.ToLower(base), "_test.go"):
			test := strings.TrimSuffix(base, ".go") + "_test.go"
			if dir == "." {
				add(test)
			} else {
				add(dir + "/" + test)
			}
		case strings.HasSuffix(strings.ToLower(base), ".js") && !strings.Contains(strings.ToLower(base), ".test."):
			if dir == "." {
				add(strings.TrimSuffix(base, ".js") + ".test.js")
			} else {
				add(dir + "/" + strings.TrimSuffix(base, ".js") + ".test.js")
			}
		}
	}
	if len(seen) == 0 {
		return ""
	}
	return builder.String()
}

func writeSliceTargetsStillFailing(slices []session.WorkflowChangeSliceSnapshot, needed []string) []string {
	if len(needed) == 0 {
		return nil
	}
	missing := make([]string, 0)
	seen := make(map[string]struct{})
	for _, path := range needed {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" {
			continue
		}
		matched := ""
		for _, slice := range slices {
			if !strings.EqualFold(strings.TrimSpace(slice.ExecutionMode), "write") {
				continue
			}
			if session.ChangeSliceTargetsCover(slice.TargetFiles, path) {
				matched = path
			}
			for _, target := range slice.TargetFiles {
				target = filepath.ToSlash(target)
				if filepath.Base(target) == filepath.Base(path) || strings.HasPrefix(target, strings.TrimSuffix(path, "/")+"/") {
					matched = target
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			matched = path
		}
		if _, exists := seen[matched]; exists {
			continue
		}
		seen[matched] = struct{}{}
		missing = append(missing, matched)
	}
	sort.Strings(missing)
	return missing
}

func expandImplementationWritePaths(root string, needed []string) []string {
	out := uniqueChangeSlicePaths(needed)
	if strings.TrimSpace(root) == "" {
		return out
	}
	expanded := make([]string, 0, len(out))
	seen := make(map[string]struct{})
	add := func(path string) {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" || path == "." {
			return
		}
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		expanded = append(expanded, path)
	}
	for _, path := range out {
		add(path)
		if strings.HasSuffix(strings.ToLower(path), ".go") || strings.HasSuffix(strings.ToLower(path), ".js") {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(path), "*.go"))
		if err != nil {
			continue
		}
		for _, match := range matches {
			rel, relErr := filepath.Rel(root, match)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(strings.ToLower(rel), "_test.go") || strings.Contains(strings.ToLower(rel), "p7_hidden") {
				continue
			}
			add(rel)
		}
	}
	sort.Strings(expanded)
	return expanded
}

func writeSlicesForImplementationPaths(paths []string) []session.WorkflowChangeSliceSnapshot {
	out := make([]session.WorkflowChangeSliceSnapshot, 0, len(paths))
	for _, path := range uniqueChangeSlicePaths(paths) {
		if path == "" {
			continue
		}
		id := "slice-" + strings.NewReplacer("/", "-", "\\", "-", ".", "-").Replace(strings.TrimSuffix(path, filepath.Ext(path)))
		id = strings.Trim(id, "-")
		if id == "slice" {
			continue
		}
		out = append(out, session.WorkflowChangeSliceSnapshot{
			ID:                   id,
			Title:                "retry public tests for " + path,
			ExecutionMode:        "write",
			TargetFiles:          []string{path},
			VerificationCommands: []string{"go test ./..."},
			Status:               "pending",
		})
	}
	return out
}

func publicTestFailureRetryDetail(verifications []schema.Verification) string {
	var builder strings.Builder
	for _, item := range verifications {
		if !strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
			continue
		}
		detail := strings.TrimSpace(item.Detail)
		if detail == "" {
			continue
		}
		if len(detail) > 800 {
			detail = detail[:800]
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(detail)
	}
	return builder.String()
}

func writeSliceTargetsMissingChanges(slices []session.WorkflowChangeSliceSnapshot, changed, needed []string) []string {
	if len(needed) == 0 {
		return nil
	}
	changedSet := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path != "" {
			changedSet[path] = struct{}{}
		}
	}
	missing := make([]string, 0)
	seen := make(map[string]struct{})
	for _, path := range needed {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" {
			continue
		}
		if _, exists := changedSet[path]; exists {
			continue
		}
		changedByName := false
		for changedPath := range changedSet {
			if filepath.Base(changedPath) == filepath.Base(path) {
				changedByName = true
				break
			}
		}
		if changedByName {
			continue
		}
		covered := false
		for _, slice := range slices {
			if !strings.EqualFold(strings.TrimSpace(slice.ExecutionMode), "write") {
				continue
			}
			if session.ChangeSliceTargetsCover(slice.TargetFiles, path) {
				covered = true
				break
			}
			for _, target := range slice.TargetFiles {
				if filepath.Base(target) == filepath.Base(path) {
					covered = true
					path = filepath.ToSlash(target)
					break
				}
			}
			if covered {
				break
			}
		}
		if !covered {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		missing = append(missing, path)
	}
	sort.Strings(missing)
	return missing
}

func workflowGraphStageRecordsDiscoveredVerification(stageName string) bool {
	switch strings.ToLower(strings.TrimSpace(stageName)) {
	case "repair", "verify", "recovery-verify":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) attachDiscoveredWorkspaceVerification(stage *WorkflowStageResult, graphStage workflowGraphStage) {
	if w == nil || stage == nil || !workflowGraphStageRecordsDiscoveredVerification(graphStage.Name) {
		return
	}
	root := w.discoveredWorkspaceRoot()
	if root == "" {
		return
	}
	verifications := runDiscoveredWorkspaceVerification(root)
	if missingTests := missingRequestedTestCoverage(root, w.workflowRequestText()); len(missingTests) > 0 {
		verifications = append(verifications, schema.Verification{
			Kind:   "command",
			Status: "failed",
			Detail: "go test ./...\n[no test files]\nrequested tests missing: " + strings.Join(missingTests, ", "),
		})
	}
	if len(verifications) == 0 {
		return
	}
	stage.Result.Verification = append(stage.Result.Verification, verifications...)
	stage.Output.Verification = append(stage.Output.Verification, verifications...)
}

func (w *WorkflowRunner) discoveredWorkspaceRoot() string {
	if w != nil && w.runtime != nil && strings.TrimSpace(w.runID) != "" {
		if task, ok, err := w.runtime.loadTaskStaging(w.runID); err == nil && ok && task != nil && strings.TrimSpace(task.Workspace) != "" {
			return task.Workspace
		}
	}
	if w == nil {
		return ""
	}
	return workspaceRoot(w.runtime)
}

func runDiscoveredWorkspaceVerification(root string) []schema.Verification {
	commands := discoverWorkspaceVerificationCommands(root)
	if len(commands) == 0 {
		return nil
	}
	out := make([]schema.Verification, 0, len(commands))
	for _, command := range commands {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			continue
		}
		program, args := fields[0], fields[1:]
		class := commandpolicy.Classify(program, args)
		switch class.Class {
		case commandpolicy.ClassTest, commandpolicy.ClassBuild, commandpolicy.ClassLint, commandpolicy.ClassFormat:
		default:
			continue
		}
		var err error
		if program == "python" {
			program, err = workspaceVerificationPython(root)
		}
		detail := ""
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			cmd := exec.CommandContext(ctx, program, args...)
			cmd.Dir = root
			cmd.Env = os.Environ()
			var combined bytes.Buffer
			cmd.Stdout = &combined
			cmd.Stderr = &combined
			err = cmd.Run()
			detail = strings.TrimSpace(combined.String())
			cancel()
		}
		if detail == "" && err != nil {
			detail = err.Error()
		}
		if len(detail) > 1200 {
			detail = detail[:1200]
		}
		status := "passed"
		if err != nil {
			status = "failed"
		}
		out = append(out, schema.Verification{
			Kind:   "command",
			Status: status,
			Detail: program + " " + strings.Join(args, " ") + "\n" + detail,
		})
	}
	return out
}

// Resolve explicitly before exec so the verifier uses the same workspace trust
// boundary as command_tools. An existing but broken environment is a failure,
// not permission to silently run a different interpreter or dependency set.
func workspaceVerificationPython(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "python", err
	}
	for _, name := range []string{".venv", "venv"} {
		environment := filepath.Join(root, name)
		if _, err := os.Lstat(environment); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return environment, fmt.Errorf("inspect Python environment: %w", err)
		}
		candidate := filepath.Join(environment, "bin", "python")
		if runtime.GOOS == "windows" {
			candidate = filepath.Join(environment, "Scripts", "python.exe")
		}
		trustedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return candidate, fmt.Errorf("resolve workspace root: %w", err)
		}
		// Windows directory junctions may not be followed by EvalSymlinks.
		// Check each environment component's redirect before opening the binary.
		for _, path := range []string{environment, filepath.Dir(candidate), candidate} {
			if target, err := os.Readlink(path); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(path), target)
				}
				rel, err := filepath.Rel(trustedRoot, target)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return candidate, fmt.Errorf("project Python interpreter resolves outside workspace root")
				}
			}
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return candidate, fmt.Errorf("resolve project Python interpreter: %w", err)
		}
		rel, err := filepath.Rel(trustedRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return candidate, fmt.Errorf("project Python interpreter resolves outside workspace root")
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return candidate, fmt.Errorf("inspect project Python interpreter: %w", err)
		}
		if !info.Mode().IsRegular() {
			return candidate, fmt.Errorf("project Python interpreter is not a regular file")
		}
		return candidate, nil
	}
	path, err := exec.LookPath("python")
	if err != nil {
		return "python", fmt.Errorf("resolve Python on PATH: %w", err)
	}
	return path, nil
}
