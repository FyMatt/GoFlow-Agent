package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreProjectTaskSearchAndDashboard(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	project, err := store.UpdateProject("# Project Memory\n\n## Project Goal\n- Build a durable Agent memory layer.\n")
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if !strings.Contains(project.Summary, "durable Agent memory") {
		t.Fatalf("expected project summary, got %q", project.Summary)
	}
	task, err := store.RecordTask(TaskSummary{
		UserGoal:        "optimize login validation context",
		KeyDecisions:    []string{"store summaries instead of full tool logs"},
		ModifiedFiles:   []string{"internal/auth/login.go", "internal/auth/login.go"},
		TestResults:     []string{"go test ./internal/auth passed"},
		ReusableLessons: []string{"prefer summary refs for repeated outputs"},
	})
	if err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("expected generated task id")
	}
	results, err := store.Search("login summaries", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results.Total == 0 {
		t.Fatalf("expected search hits")
	}
	foundTask := false
	for _, result := range results.Results {
		if result.Kind == "task" && strings.Contains(result.Summary, "login validation") {
			foundTask = true
		}
	}
	if !foundTask {
		t.Fatalf("expected task search result, got %#v", results.Results)
	}
	dashboard, err := store.Dashboard(5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(dashboard.Tasks) != 1 || dashboard.Project.Path == "" {
		t.Fatalf("unexpected dashboard: %#v", dashboard)
	}
}

func TestStoreRebuildFilesIndexesSummaries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc RunMemory() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".goflow"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".goflow", "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile .goflow: %v", err)
	}
	store := NewStore(root)
	index, err := store.RebuildFiles(context.Background())
	if err != nil {
		t.Fatalf("RebuildFiles: %v", err)
	}
	if index.TotalFiles != 1 {
		t.Fatalf("expected only workspace source indexed, got %#v", index.Files)
	}
	file := index.Files[0]
	if file.Path != "main.go" || file.Language != "go" || file.Hash == "" || !strings.Contains(strings.Join(file.Symbols, ","), "RunMemory") {
		t.Fatalf("unexpected file summary: %#v", file)
	}
	results, err := store.Search("RunMemory", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results.Results) == 0 || results.Results[0].Kind != "file" {
		t.Fatalf("expected file search hit, got %#v", results.Results)
	}
}

func TestPromptContextRendersSummaryRefs(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := store.UpdateProject("# Project Memory\n\n## Key Constraints\n- Keep full history externalized.\n"); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if _, err := store.RecordTask(TaskSummary{UserGoal: "externalize large tool results", KeyDecisions: []string{"use sha256 refs"}}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	context, err := store.PromptContext("large tool results")
	if err != nil {
		t.Fatalf("PromptContext: %v", err)
	}
	text := context.PromptText()
	if !strings.Contains(text, "Memory context") || !strings.Contains(text, "Project memory") || !strings.Contains(text, "sha256 refs") {
		t.Fatalf("unexpected prompt text: %s", text)
	}
}

func TestStoreContextDashboardSearchAndPrompt(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	summary, err := store.RecordContext(ContextSummary{
		Summary:              "Compressed current session for token savings.",
		Reason:               "manual compact",
		ActiveAgent:          "planner",
		Mode:                 "plan",
		RecentGoals:          []string{"reduce prompt size"},
		Decisions:            []string{"use summary and artifact refs"},
		PendingActions:       []string{"verify API compact endpoint"},
		RelevantFiles:        []string{"internal/agent/context_compaction.go"},
		ArtifactRefs:         []string{"sha256:abc"},
		EstimatedSavedTokens: 1234,
	})
	if err != nil {
		t.Fatalf("RecordContext: %v", err)
	}
	if summary.ID == "" || summary.UpdatedAt == "" {
		t.Fatalf("expected persisted context metadata, got %#v", summary)
	}
	dashboard, err := store.Dashboard(5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if dashboard.Context.ID != summary.ID || dashboard.Context.EstimatedSavedTokens != 1234 {
		t.Fatalf("expected context on dashboard, got %#v", dashboard.Context)
	}
	results, err := store.Search("artifact refs", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, result := range results.Results {
		if result.Kind == "context" && strings.Contains(result.Summary, "artifact refs") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected compacted context search result, got %#v", results.Results)
	}
	ctx, err := store.PromptContext("reduce prompt size")
	if err != nil {
		t.Fatalf("PromptContext: %v", err)
	}
	text := ctx.PromptText()
	if !strings.Contains(text, "Compacted session context") || !strings.Contains(text, "Compressed current session") {
		t.Fatalf("expected context prompt block, got %s", text)
	}
}

func TestPromptContextFreshInjectsFileSummaryAndRefreshesChangedIndex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "auth.go")
	if err := os.WriteFile(path, []byte("package auth\n\nfunc LoginValidator() string { return \"old-token\" }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := NewStore(root)
	index, err := store.RebuildFiles(context.Background())
	if err != nil {
		t.Fatalf("RebuildFiles: %v", err)
	}
	if len(index.Files) != 1 || index.Files[0].Hash == "" {
		t.Fatalf("expected indexed auth.go with hash, got %#v", index.Files)
	}
	oldHash := index.Files[0].Hash

	ctx, err := store.PromptContextFresh(context.Background(), "inspect LoginValidator auth.go", "run-1")
	if err != nil {
		t.Fatalf("PromptContextFresh: %v", err)
	}
	fileBlock := findPromptFileBlock(ctx, "auth.go")
	if fileBlock.Ref != "auth.go" || fileBlock.ContentMode != "summary" || fileBlock.Hash != oldHash {
		t.Fatalf("expected summary-only auth.go file block, got %#v", fileBlock)
	}
	if strings.Contains(ctx.PromptText(), "old-token") {
		t.Fatalf("expected prompt to use summary/hash rather than full file content, got %s", ctx.PromptText())
	}
	usedIndex, err := store.FileIndex()
	if err != nil {
		t.Fatalf("FileIndex: %v", err)
	}
	if usedIndex.Files[0].LastReadAt == "" || usedIndex.Files[0].LastUsedByTask != "run-1" {
		t.Fatalf("expected selected file usage metadata, got %#v", usedIndex.Files[0])
	}

	if err := os.WriteFile(path, []byte("package auth\n\nfunc LoginValidator() string { return \"new-token\" }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile changed: %v", err)
	}
	ctx, err = store.PromptContextFresh(context.Background(), "inspect LoginValidator auth.go", "run-2")
	if err != nil {
		t.Fatalf("PromptContextFresh changed: %v", err)
	}
	fileBlock = findPromptFileBlock(ctx, "auth.go")
	if fileBlock.Hash == "" || fileBlock.Hash == oldHash {
		t.Fatalf("expected changed file hash after prompt context refresh, old=%s block=%#v", oldHash, fileBlock)
	}
	if !strings.Contains(strings.Join(ctx.Omitted, "\n"), "file summary index refreshed") {
		t.Fatalf("expected refresh diagnostic after changed file, got %#v", ctx.Omitted)
	}
	if strings.Contains(ctx.PromptText(), "new-token") {
		t.Fatalf("expected changed prompt to still use summary/hash, got %s", ctx.PromptText())
	}
}

func TestEnsureProjectProfileSeedsFromWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# GoFlow Agent\n\nToken-saving runtime for durable workflows.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	before, err := store.Project()
	if err != nil {
		t.Fatalf("Project before: %v", err)
	}
	if !isDefaultProjectMemory(before.Content) {
		t.Fatalf("expected default project memory before seeding")
	}
	project, changed, err := store.EnsureProjectProfile(context.Background())
	if err != nil {
		t.Fatalf("EnsureProjectProfile: %v", err)
	}
	if !changed {
		t.Fatalf("expected generated project profile")
	}
	if isDefaultProjectMemory(project.Content) {
		t.Fatalf("expected generated project profile to replace default template")
	}
	if !strings.Contains(project.Content, "GoFlow Agent") {
		t.Fatalf("expected README title reflected in project profile, got %q", project.Content)
	}
	if !strings.Contains(strings.ToLower(project.Content), "go") {
		t.Fatalf("expected detected tech stack in project profile, got %q", project.Content)
	}
}

func findPromptFileBlock(ctx PromptContext, path string) PromptBlock {
	for _, block := range ctx.Blocks {
		if block.Kind == "file" && block.Ref == path {
			return block
		}
	}
	return PromptBlock{}
}
