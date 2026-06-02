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

func TestSolutionMemoryLearnsSearchesInjectsAndMarksUsage(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := store.RecordTask(TaskSummary{
		UserGoal:        "fix DeepSeek thinking mode reasoning_content retry error",
		KeyDecisions:    []string{"pass provider reasoning_content back when the provider requires it"},
		ModifiedFiles:   []string{"internal/llm/client.go"},
		TestResults:     []string{"go test ./internal/llm passed"},
		ReusableLessons: []string{"store provider-specific retry decisions as reusable solution memory"},
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	solutions, err := store.Solutions()
	if err != nil {
		t.Fatalf("Solutions: %v", err)
	}
	if len(solutions.Solutions) != 1 {
		t.Fatalf("expected one learned solution, got %#v", solutions.Solutions)
	}
	learned := solutions.Solutions[0]
	if learned.ID == "" || learned.Confidence != "high" || !learned.Resolved {
		t.Fatalf("unexpected learned solution metadata: %#v", learned)
	}

	results, err := store.Search("DeepSeek reasoning_content", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	foundSolution := false
	for _, result := range results.Results {
		if result.Kind == "solution" && strings.Contains(result.Summary, "reasoning_content") {
			foundSolution = true
			break
		}
	}
	if !foundSolution {
		t.Fatalf("expected solution search result, got %#v", results.Results)
	}

	if err := store.MarkSolutionsUsed([]string{learned.ID}); err != nil {
		t.Fatalf("MarkSolutionsUsed: %v", err)
	}
	solutions, err = store.Solutions()
	if err != nil {
		t.Fatalf("Solutions after mark: %v", err)
	}
	if solutions.Solutions[0].UseCount != 1 || solutions.Solutions[0].LastUsedAt == "" {
		t.Fatalf("expected usage metadata update, got %#v", solutions.Solutions[0])
	}

	ctx, err := store.PromptContextFresh(context.Background(), "DeepSeek reasoning_content invalid_request_error", "run-solution")
	if err != nil {
		t.Fatalf("PromptContextFresh: %v", err)
	}
	block := findPromptBlockByKind(ctx, "solution")
	if block.Kind != "solution" || block.ContentMode != "decision" || !strings.Contains(block.Summary, "reasoning_content") {
		t.Fatalf("expected decision solution prompt block, got %#v in %#v", block, ctx.Blocks)
	}
	if !strings.Contains(ctx.PromptText(), "reuse that decision before asking the operator") {
		t.Fatalf("expected prompt reuse instruction, got %s", ctx.PromptText())
	}
	solutions, err = store.Solutions()
	if err != nil {
		t.Fatalf("Solutions after prompt: %v", err)
	}
	if solutions.Solutions[0].UseCount < 2 {
		t.Fatalf("expected prompt retrieval to mark solution used, got %#v", solutions.Solutions[0])
	}

	dashboard, err := store.Dashboard(5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(dashboard.Solutions.Solutions) != 1 {
		t.Fatalf("expected dashboard solutions, got %#v", dashboard.Solutions)
	}
}

func TestPromptContextSkipsInvalidatedSolutionMemory(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	kb, err := store.UpsertSolution(SolutionMemory{
		ProblemSignature:    "provider retry requires thinking payload",
		Problem:             "provider returns reasoning_content retry error",
		Decision:            "send the provider thinking payload back on retry",
		Solution:            "preserve reasoning_content and replay it only when the provider asks for it",
		Applicability:       []string{"same provider retry error"},
		InvalidWhen:         []string{"provider protocol changes"},
		VerificationCommand: "go test ./internal/llm",
		Confidence:          "high",
		Resolved:            true,
	})
	if err != nil {
		t.Fatalf("UpsertSolution: %v", err)
	}
	if len(kb.Solutions) != 1 {
		t.Fatalf("expected solution, got %#v", kb.Solutions)
	}
	id := kb.Solutions[0].ID

	ctx, err := store.PromptContextFresh(context.Background(), "provider protocol changed; investigate thinking payload retry", "run-invalidated")
	if err != nil {
		t.Fatalf("PromptContextFresh invalidated: %v", err)
	}
	if block := findPromptBlockByKind(ctx, "solution"); block.Kind != "" {
		t.Fatalf("invalidated solution should not be injected, got %#v", block)
	}
	omittedInvalidated := false
	for _, item := range ctx.Omitted {
		if strings.Contains(item, "invalidation rule matched") && strings.Contains(item, id) {
			omittedInvalidated = true
			break
		}
	}
	if !omittedInvalidated {
		t.Fatalf("expected invalidation omission diagnostic, got %#v", ctx.Omitted)
	}
	kb, err = store.Solutions()
	if err != nil {
		t.Fatalf("Solutions after invalidated prompt: %v", err)
	}
	if kb.Solutions[0].UseCount != 0 {
		t.Fatalf("invalidated solution should not be marked used, got %#v", kb.Solutions[0])
	}

	ctx, err = store.PromptContextFresh(context.Background(), "same provider retry error thinking payload", "run-valid")
	if err != nil {
		t.Fatalf("PromptContextFresh valid: %v", err)
	}
	block := findPromptBlockByKind(ctx, "solution")
	if block.Kind != "solution" || block.Ref != id {
		t.Fatalf("expected valid solution prompt block, got %#v in %#v", block, ctx.Blocks)
	}
	kb, err = store.Solutions()
	if err != nil {
		t.Fatalf("Solutions after valid prompt: %v", err)
	}
	if kb.Solutions[0].UseCount != 1 {
		t.Fatalf("expected valid solution use count, got %#v", kb.Solutions[0])
	}
}

func TestSolutionMemoryLifecycleSkipsRetiredRetrieval(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	kb, err := store.UpsertSolution(SolutionMemory{
		ProblemSignature:    "provider retry requires thinking payload",
		Problem:             "provider returns reasoning_content retry error",
		Decision:            "send the provider thinking payload back on retry",
		Solution:            "preserve reasoning_content and replay it only when the provider asks for it",
		Applicability:       []string{"same provider retry error"},
		InvalidWhen:         []string{"provider protocol changes"},
		VerificationCommand: "go test ./internal/llm",
		Confidence:          "high",
		Resolved:            true,
	})
	if err != nil {
		t.Fatalf("UpsertSolution: %v", err)
	}
	if len(kb.Solutions) != 1 {
		t.Fatalf("expected solution, got %#v", kb.Solutions)
	}
	id := kb.Solutions[0].ID

	replacementKB, err := store.UpsertSolution(SolutionMemory{
		ProblemSignature:    "provider retry uses response id",
		Problem:             "provider retry requires response id",
		Decision:            "send the response id on retry",
		Solution:            "reuse provider response ids for retry continuation",
		VerificationCommand: "go test ./internal/llm",
		Confidence:          "high",
		Resolved:            true,
	})
	if err != nil {
		t.Fatalf("Upsert replacement solution: %v", err)
	}
	replacementID := replacementKB.Solutions[0].ID

	kb, err = store.SupersedeSolution(id, replacementID, "provider protocol changed")
	if err != nil {
		t.Fatalf("SupersedeSolution: %v", err)
	}
	retiredSolution, ok := findSolutionByID(kb.Solutions, id)
	if !ok {
		t.Fatalf("expected superseded solution in kb, got %#v", kb.Solutions)
	}
	if !retiredSolution.Retired || retiredSolution.RetiredAt == "" || retiredSolution.RetiredReason == "" || retiredSolution.SupersededBy != replacementID {
		t.Fatalf("expected superseded metadata, got %#v", retiredSolution)
	}
	results, err := store.Search("thinking payload", 10)
	if err != nil {
		t.Fatalf("Search retired: %v", err)
	}
	for _, result := range results.Results {
		if result.Kind == "solution" {
			t.Fatalf("retired solution should not be searchable by default, got %#v", result)
		}
	}
	ctx, err := store.PromptContextFresh(context.Background(), "thinking payload", "run-retired")
	if err != nil {
		t.Fatalf("PromptContextFresh retired: %v", err)
	}
	if block := findPromptBlockByKind(ctx, "solution"); block.Kind != "" {
		t.Fatalf("retired solution should not be injected, got %#v", block)
	}
	if err := store.MarkSolutionsUsed([]string{id}); err != nil {
		t.Fatalf("MarkSolutionsUsed retired: %v", err)
	}
	kb, err = store.Solutions()
	if err != nil {
		t.Fatalf("Solutions after mark retired: %v", err)
	}
	retiredSolution, ok = findSolutionByID(kb.Solutions, id)
	if !ok {
		t.Fatalf("expected retired solution after mark, got %#v", kb.Solutions)
	}
	if retiredSolution.UseCount != 0 {
		t.Fatalf("retired solution should not record use count, got %#v", retiredSolution)
	}

	kb, err = store.RestoreSolution(id)
	if err != nil {
		t.Fatalf("RestoreSolution: %v", err)
	}
	restoredSolution, ok := findSolutionByID(kb.Solutions, id)
	if !ok {
		t.Fatalf("expected restored solution in kb, got %#v", kb.Solutions)
	}
	if restoredSolution.Retired || restoredSolution.RetiredAt != "" || restoredSolution.RetiredReason != "" || restoredSolution.SupersededBy != "" {
		t.Fatalf("expected restored solution lifecycle cleared, got %#v", restoredSolution)
	}
	results, err = store.Search("thinking payload", 10)
	if err != nil {
		t.Fatalf("Search restored: %v", err)
	}
	found := false
	for _, result := range results.Results {
		if result.Kind == "solution" && result.Metadata["solution_id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected restored solution in search, got %#v", results.Results)
	}
}

func findSolutionByID(items []SolutionMemory, id string) (SolutionMemory, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return SolutionMemory{}, false
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

func findPromptBlockByKind(ctx PromptContext, kind string) PromptBlock {
	for _, block := range ctx.Blocks {
		if block.Kind == kind {
			return block
		}
	}
	return PromptBlock{}
}
