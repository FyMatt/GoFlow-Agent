package memory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	projectMemoryRelativePath  = ".goflow/memory/project.md"
	taskMemoryDirRelativePath  = ".goflow/memory/tasks"
	errorMemoryRelativePath    = ".goflow/memory/errors.json"
	solutionMemoryRelativePath = ".goflow/memory/solutions.json"
	contextMemoryRelativePath  = ".goflow/memory/context.json"
	fileIndexRelativePath      = ".goflow/index/files.json"

	maxProjectPromptBytes = 2400
	maxSearchSummaryBytes = 900
	maxIndexedFileBytes   = 512 * 1024
	maxIndexedFiles       = 2500
	maxSymbolScanLines    = 400
	maxPromptFileBlocks   = 4
)

// Store manages workspace-scoped long-term memory and lightweight indexes.
type Store struct {
	workspaceRoot string
}

// NewStore returns a workspace-scoped memory store. An empty workspace root
// yields a disabled store whose methods return useful errors instead of
// touching the process working directory.
func NewStore(workspaceRoot string) *Store {
	return &Store{workspaceRoot: strings.TrimSpace(workspaceRoot)}
}

// Root returns the workspace root backing this store.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.workspaceRoot)
}

// MemoryRoot returns the .goflow/memory directory for this workspace.
func (s *Store) MemoryRoot() string {
	if s == nil || strings.TrimSpace(s.workspaceRoot) == "" {
		return ""
	}
	return filepath.Join(s.workspaceRoot, ".goflow", "memory")
}

// ProjectPath returns the markdown project memory path.
func (s *Store) ProjectPath() string {
	if s == nil || strings.TrimSpace(s.workspaceRoot) == "" {
		return ""
	}
	return filepath.Join(s.workspaceRoot, projectMemoryRelativePath)
}

// Ensure creates the memory directories and a project memory template when
// missing. It never overwrites an existing project.md.
func (s *Store) Ensure() error {
	if err := s.ensureEnabled(); err != nil {
		return err
	}
	for _, dir := range []string{
		filepath.Join(s.workspaceRoot, ".goflow", "memory"),
		filepath.Join(s.workspaceRoot, taskMemoryDirRelativePath),
		filepath.Join(s.workspaceRoot, ".goflow", "index"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	projectPath := s.ProjectPath()
	if _, err := os.Stat(projectPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(projectPath, []byte(defaultProjectMemory()), 0o644)
}

// Project reads the workspace project memory.
func (s *Store) Project() (ProjectMemory, error) {
	if err := s.ensureEnabled(); err != nil {
		return ProjectMemory{}, err
	}
	path := s.ProjectPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ProjectMemory{Path: filepath.ToSlash(projectMemoryRelativePath), Content: "", Summary: ""}, nil
		}
		return ProjectMemory{}, err
	}
	info, _ := os.Stat(path)
	updated := ""
	if info != nil {
		updated = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	content := strings.TrimPrefix(string(data), "\ufeff")
	return ProjectMemory{
		Path:      filepath.ToSlash(projectMemoryRelativePath),
		Content:   content,
		Summary:   summarizeMarkdownMemory(content, maxProjectPromptBytes),
		UpdatedAt: updated,
	}, nil
}

// UpdateProject replaces project memory content.
func (s *Store) UpdateProject(content string) (ProjectMemory, error) {
	if err := s.Ensure(); err != nil {
		return ProjectMemory{}, err
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if strings.TrimSpace(content) == "" {
		if generated, ok := s.generatedProjectMemoryContent(context.Background()); ok {
			content = generated
		} else {
			content = defaultProjectMemory()
		}
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(s.ProjectPath(), []byte(content), 0o644); err != nil {
		return ProjectMemory{}, err
	}
	return s.Project()
}

// EnsureProjectProfile replaces the shipped placeholder with a workspace-derived
// project profile the first time memory is used. Existing user edits are never
// overwritten.
func (s *Store) EnsureProjectProfile(ctx context.Context) (ProjectMemory, bool, error) {
	if err := s.Ensure(); err != nil {
		return ProjectMemory{}, false, err
	}
	project, err := s.Project()
	if err != nil {
		return ProjectMemory{}, false, err
	}
	if !isDefaultProjectMemory(project.Content) {
		return project, false, nil
	}
	content, ok := s.generatedProjectMemoryContent(ctx)
	if !ok {
		return project, false, nil
	}
	updated, err := s.UpdateProject(content)
	if err != nil {
		return ProjectMemory{}, false, err
	}
	return updated, true, nil
}

// RebuildFiles scans workspace files and writes .goflow/index/files.json.
func (s *Store) RebuildFiles(ctx context.Context) (FileIndex, error) {
	if err := s.Ensure(); err != nil {
		return FileIndex{}, err
	}
	root, err := filepath.Abs(filepath.Clean(s.workspaceRoot))
	if err != nil {
		return FileIndex{}, err
	}
	index := FileIndex{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if skipIndexDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(index.Files) >= maxIndexedFiles || skipIndexFile(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info == nil || info.Mode()&os.ModeType != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		item, err := summarizeFile(path, filepath.ToSlash(rel), info)
		if err != nil {
			return nil
		}
		index.Files = append(index.Files, item)
		return nil
	})
	if err != nil {
		return FileIndex{}, err
	}
	sort.Slice(index.Files, func(i, j int) bool {
		return strings.ToLower(index.Files[i].Path) < strings.ToLower(index.Files[j].Path)
	})
	index.TotalFiles = len(index.Files)
	index.IndexedBytes = indexedBytes(index.Files)
	if err := writeJSONFile(filepath.Join(s.workspaceRoot, fileIndexRelativePath), index); err != nil {
		return FileIndex{}, err
	}
	return index, nil
}

// FileIndex reads the current file summary index.
func (s *Store) FileIndex() (FileIndex, error) {
	if err := s.ensureEnabled(); err != nil {
		return FileIndex{}, err
	}
	var index FileIndex
	if err := readJSONFile(filepath.Join(s.workspaceRoot, fileIndexRelativePath), &index); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileIndex{}, nil
		}
		return FileIndex{}, err
	}
	return index, nil
}

func (s *Store) FileSummaryForPath(ctx context.Context, path string) (FileSummary, bool, error) {
	index, _, err := s.EnsureFreshFileIndex(ctx)
	if err != nil {
		return FileSummary{}, false, err
	}
	summary, ok := fileSummaryByPath(index, path)
	return summary, ok, nil
}

// EnsureFreshFileIndex returns a file summary index that reflects current
// workspace file size, mtime, and hash metadata. A missing, stale, or empty
// index is rebuilt so prompt retrieval can use summaries instead of repeatedly
// injecting unchanged file contents.
func (s *Store) EnsureFreshFileIndex(ctx context.Context) (FileIndex, bool, error) {
	if err := s.Ensure(); err != nil {
		return FileIndex{}, false, err
	}
	index, err := s.FileIndex()
	if err != nil {
		return FileIndex{}, false, err
	}
	if strings.TrimSpace(index.GeneratedAt) == "" || s.fileIndexStale(index) {
		rebuilt, rebuildErr := s.RebuildFiles(ctx)
		return rebuilt, true, rebuildErr
	}
	return index, false, nil
}

func (s *Store) fileIndexStale(index FileIndex) bool {
	if s == nil || strings.TrimSpace(s.workspaceRoot) == "" {
		return true
	}
	root, err := filepath.Abs(filepath.Clean(s.workspaceRoot))
	if err != nil {
		return true
	}
	seen := make(map[string]FileSummary, len(index.Files))
	for _, file := range index.Files {
		path := cleanRelativeFilePath(file.Path)
		if path == "" {
			return true
		}
		seen[path] = file
		abs := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Stat(abs)
		if err != nil || info == nil || info.IsDir() || info.Mode()&os.ModeType != 0 {
			return true
		}
		if info.Size() != file.Size || info.ModTime().UTC().Format(time.RFC3339Nano) != file.MTime {
			return true
		}
		if info.Size() <= maxIndexedFileBytes && strings.TrimSpace(file.Hash) == "" {
			return true
		}
	}
	found := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || found > len(seen) {
			return nil
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if skipIndexDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipIndexFile(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info == nil || info.Mode()&os.ModeType != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if _, ok := seen[filepath.ToSlash(rel)]; ok {
			found++
			return nil
		}
		if len(seen) < maxIndexedFiles {
			found = len(seen) + 1
		}
		return nil
	})
	return found != len(seen)
}

// Errors reads the persistent error knowledge base.
func (s *Store) Errors() (ErrorKnowledgeBase, error) {
	if err := s.ensureEnabled(); err != nil {
		return ErrorKnowledgeBase{}, err
	}
	var kb ErrorKnowledgeBase
	if err := readJSONFile(filepath.Join(s.workspaceRoot, errorMemoryRelativePath), &kb); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrorKnowledgeBase{UpdatedAt: ""}, nil
		}
		return ErrorKnowledgeBase{}, err
	}
	return kb, nil
}

// UpsertError records an error entry using a stable normalized error key.
func (s *Store) UpsertError(item ErrorMemory) (ErrorKnowledgeBase, error) {
	if err := s.Ensure(); err != nil {
		return ErrorKnowledgeBase{}, err
	}
	kb, err := s.Errors()
	if err != nil {
		return ErrorKnowledgeBase{}, err
	}
	item.Error = strings.TrimSpace(item.Error)
	if item.Error == "" {
		return kb, nil
	}
	if item.ID == "" {
		item.ID = "err-" + shortHash(item.Error+"\x00"+strings.Join(item.RelatedFiles, "\x00"))
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	replaced := false
	for i := range kb.Errors {
		if kb.Errors[i].ID == item.ID {
			kb.Errors[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		kb.Errors = append([]ErrorMemory{item}, kb.Errors...)
	}
	if len(kb.Errors) > 200 {
		kb.Errors = append([]ErrorMemory(nil), kb.Errors[:200]...)
	}
	kb.UpdatedAt = now
	if err := writeJSONFile(filepath.Join(s.workspaceRoot, errorMemoryRelativePath), kb); err != nil {
		return ErrorKnowledgeBase{}, err
	}
	return kb, nil
}

// Solutions reads the persistent reusable solution knowledge base.
func (s *Store) Solutions() (SolutionKnowledgeBase, error) {
	if err := s.ensureEnabled(); err != nil {
		return SolutionKnowledgeBase{}, err
	}
	var kb SolutionKnowledgeBase
	if err := readJSONFile(filepath.Join(s.workspaceRoot, solutionMemoryRelativePath), &kb); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SolutionKnowledgeBase{UpdatedAt: ""}, nil
		}
		return SolutionKnowledgeBase{}, err
	}
	return kb, nil
}

// Solution returns a single solution memory item by ID.
func (s *Store) Solution(id string) (SolutionMemory, bool, error) {
	if err := s.ensureEnabled(); err != nil {
		return SolutionMemory{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return SolutionMemory{}, false, nil
	}
	kb, err := s.Solutions()
	if err != nil {
		return SolutionMemory{}, false, err
	}
	for _, item := range kb.Solutions {
		if item.ID == id {
			return item, true, nil
		}
	}
	return SolutionMemory{}, false, nil
}

// UpsertSolution records a reusable problem decision and verified solution.
func (s *Store) UpsertSolution(item SolutionMemory) (SolutionKnowledgeBase, error) {
	if err := s.Ensure(); err != nil {
		return SolutionKnowledgeBase{}, err
	}
	kb, err := s.Solutions()
	if err != nil {
		return SolutionKnowledgeBase{}, err
	}
	item = normalizeSolutionMemory(item)
	if item.ProblemSignature == "" {
		return kb, nil
	}
	if item.ID == "" {
		item.ID = "sol-" + shortHash(normalizeSolutionSignature(item.ProblemSignature)+"\x00"+strings.Join(item.RelatedFiles, "\x00"))
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	replaced := false
	for i := range kb.Solutions {
		if kb.Solutions[i].ID != item.ID {
			continue
		}
		if item.CreatedAt == "" {
			item.CreatedAt = kb.Solutions[i].CreatedAt
		}
		if item.UseCount == 0 {
			item.UseCount = kb.Solutions[i].UseCount
		}
		if item.LastUsedAt == "" {
			item.LastUsedAt = kb.Solutions[i].LastUsedAt
		}
		if !item.Retired && strings.TrimSpace(item.RetiredAt) == "" && strings.TrimSpace(item.RetiredReason) == "" && strings.TrimSpace(item.SupersededBy) == "" && kb.Solutions[i].Retired {
			item.Retired = true
			item.RetiredAt = kb.Solutions[i].RetiredAt
			item.RetiredReason = kb.Solutions[i].RetiredReason
			item.SupersededBy = kb.Solutions[i].SupersededBy
		}
		if item.CreatedAt == "" {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		kb.Solutions[i] = item
		replaced = true
		break
	}
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if !replaced {
		kb.Solutions = append([]SolutionMemory{item}, kb.Solutions...)
	}
	if len(kb.Solutions) > 200 {
		kb.Solutions = append([]SolutionMemory(nil), kb.Solutions[:200]...)
	}
	kb.UpdatedAt = now
	if err := writeJSONFile(filepath.Join(s.workspaceRoot, solutionMemoryRelativePath), kb); err != nil {
		return SolutionKnowledgeBase{}, err
	}
	return kb, nil
}

// RetireSolution marks a solution as retired and optionally points to a replacement.
func (s *Store) RetireSolution(id, reason, supersededBy string) (SolutionKnowledgeBase, error) {
	return s.updateSolutionLifecycle(id, func(item *SolutionMemory) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		item.Retired = true
		if strings.TrimSpace(item.RetiredAt) == "" {
			item.RetiredAt = now
		}
		item.RetiredReason = trimMemoryText(reason, 500)
		item.SupersededBy = trimMemoryText(supersededBy, 200)
	})
}

// RestoreSolution reactivates a retired solution.
func (s *Store) RestoreSolution(id string) (SolutionKnowledgeBase, error) {
	return s.updateSolutionLifecycle(id, func(item *SolutionMemory) {
		item.Retired = false
		item.RetiredAt = ""
		item.RetiredReason = ""
		item.SupersededBy = ""
	})
}

func (s *Store) updateSolutionLifecycle(id string, mutate func(*SolutionMemory)) (SolutionKnowledgeBase, error) {
	if err := s.Ensure(); err != nil {
		return SolutionKnowledgeBase{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return SolutionKnowledgeBase{}, fmt.Errorf("solution id is required")
	}
	kb, err := s.Solutions()
	if err != nil {
		return SolutionKnowledgeBase{}, err
	}
	for i := range kb.Solutions {
		if kb.Solutions[i].ID != id {
			continue
		}
		item := kb.Solutions[i]
		mutate(&item)
		item = normalizeSolutionMemory(item)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if strings.TrimSpace(item.CreatedAt) == "" {
			item.CreatedAt = kb.Solutions[i].CreatedAt
		}
		if strings.TrimSpace(item.CreatedAt) == "" {
			item.CreatedAt = now
		}
		if item.Retired {
			if strings.TrimSpace(item.RetiredAt) == "" {
				item.RetiredAt = now
			}
		} else {
			item.RetiredAt = ""
			item.RetiredReason = ""
			item.SupersededBy = ""
		}
		item.UpdatedAt = now
		kb.Solutions[i] = item
		kb.UpdatedAt = now
		if err := writeJSONFile(filepath.Join(s.workspaceRoot, solutionMemoryRelativePath), kb); err != nil {
			return SolutionKnowledgeBase{}, err
		}
		return kb, nil
	}
	return kb, fmt.Errorf("solution %q not found", id)
}

// MarkSolutionsUsed updates lightweight usage metadata for retrieved solutions.
func (s *Store) MarkSolutionsUsed(ids []string) error {
	if s == nil || len(ids) == 0 {
		return nil
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	kb, err := s.Solutions()
	if err != nil {
		return err
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	changed := false
	for i := range kb.Solutions {
		if _, ok := wanted[kb.Solutions[i].ID]; !ok {
			continue
		}
		if kb.Solutions[i].Retired {
			continue
		}
		kb.Solutions[i].UseCount++
		kb.Solutions[i].LastUsedAt = now
		kb.Solutions[i].UpdatedAt = now
		changed = true
	}
	if !changed {
		return nil
	}
	kb.UpdatedAt = now
	return writeJSONFile(filepath.Join(s.workspaceRoot, solutionMemoryRelativePath), kb)
}

// RecordTask writes a task summary under .goflow/memory/tasks.
func (s *Store) RecordTask(summary TaskSummary) (TaskSummary, error) {
	if err := s.Ensure(); err != nil {
		return TaskSummary{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(summary.ID) == "" {
		seed := strings.Join([]string{summary.UserGoal, summary.AgentID, summary.Mode, now}, "\x00")
		summary.ID = "task-" + time.Now().UTC().Format("20060102-150405") + "-" + shortHash(seed)
	}
	if strings.TrimSpace(summary.CreatedAt) == "" {
		summary.CreatedAt = now
	}
	summary.UpdatedAt = now
	summary.UserGoal = trimMemoryText(summary.UserGoal, 4096)
	summary.KeyDecisions = trimStringList(summary.KeyDecisions, 1200)
	summary.ModifiedFiles = dedupeStrings(summary.ModifiedFiles)
	summary.TestResults = trimStringList(summary.TestResults, 1200)
	summary.FailureReasons = trimStringList(summary.FailureReasons, 1200)
	summary.NextTodos = trimStringList(summary.NextTodos, 1200)
	summary.ReusableLessons = trimStringList(summary.ReusableLessons, 1200)
	path := filepath.Join(s.workspaceRoot, taskMemoryDirRelativePath, sanitizeFileComponent(summary.ID)+".json")
	if err := writeJSONFile(path, summary); err != nil {
		return TaskSummary{}, err
	}
	for _, failure := range summary.FailureReasons {
		_, _ = s.UpsertError(ErrorMemory{
			Error:               failure,
			RootCause:           firstString(summary.KeyDecisions...),
			Fix:                 firstString(summary.ReusableLessons...),
			RelatedFiles:        append([]string(nil), summary.ModifiedFiles...),
			VerificationCommand: firstVerificationCommand(summary.TestResults),
			Resolved:            false,
		})
	}
	if solution, ok := solutionFromTaskSummary(summary); ok {
		_, _ = s.UpsertSolution(solution)
	}
	return summary, nil
}

// Context reads the latest compacted working context.
func (s *Store) Context() (ContextSummary, error) {
	if err := s.ensureEnabled(); err != nil {
		return ContextSummary{}, err
	}
	var summary ContextSummary
	if err := readJSONFile(filepath.Join(s.workspaceRoot, contextMemoryRelativePath), &summary); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ContextSummary{}, nil
		}
		return ContextSummary{}, err
	}
	return summary, nil
}

// RecordContext persists the latest compacted working context.
func (s *Store) RecordContext(summary ContextSummary) (ContextSummary, error) {
	if err := s.Ensure(); err != nil {
		return ContextSummary{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(summary.ID) == "" {
		seed := strings.Join([]string{summary.Summary, summary.ActiveAgent, summary.Mode, summary.Reason, now}, "\x00")
		summary.ID = "ctx-" + time.Now().UTC().Format("20060102-150405") + "-" + shortHash(seed)
	}
	if strings.TrimSpace(summary.CreatedAt) == "" {
		summary.CreatedAt = now
	}
	summary.UpdatedAt = now
	summary.Summary = trimMemoryText(summary.Summary, 3000)
	summary.Reason = trimMemoryText(summary.Reason, 1000)
	summary.RecentGoals = trimStringList(summary.RecentGoals, 900)
	summary.Decisions = trimStringList(summary.Decisions, 900)
	summary.PendingActions = trimStringList(summary.PendingActions, 900)
	summary.RelevantFiles = dedupeStrings(summary.RelevantFiles)
	summary.ArtifactRefs = dedupeStrings(summary.ArtifactRefs)
	if summary.SourceCounts == nil {
		summary.SourceCounts = map[string]int{}
	}
	if err := writeJSONFile(filepath.Join(s.workspaceRoot, contextMemoryRelativePath), summary); err != nil {
		return ContextSummary{}, err
	}
	return summary, nil
}

// Tasks lists recent task summaries newest first.
func (s *Store) Tasks(limit int) ([]TaskSummary, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.workspaceRoot, taskMemoryDirRelativePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	tasks := make([]TaskSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		var task TaskSummary
		if err := readJSONFile(filepath.Join(dir, entry.Name()), &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].UpdatedAt > tasks[j].UpdatedAt
	})
	if limit > 0 && len(tasks) > limit {
		tasks = append([]TaskSummary(nil), tasks[:limit]...)
	}
	return tasks, nil
}

// Dashboard returns a summary-first view of all memory surfaces.
func (s *Store) Dashboard(taskLimit int) (Dashboard, error) {
	if err := s.ensureEnabled(); err != nil {
		return Dashboard{}, err
	}
	project, projectErr := s.Project()
	tasks, taskErr := s.Tasks(taskLimit)
	errorsKB, errorsErr := s.Errors()
	solutionsKB, solutionsErr := s.Solutions()
	fileIndex, indexErr := s.FileIndex()
	contextSummary, contextErr := s.Context()
	if projectErr != nil {
		return Dashboard{}, projectErr
	}
	if taskErr != nil {
		return Dashboard{}, taskErr
	}
	if errorsErr != nil {
		return Dashboard{}, errorsErr
	}
	if solutionsErr != nil {
		return Dashboard{}, solutionsErr
	}
	if indexErr != nil {
		return Dashboard{}, indexErr
	}
	if contextErr != nil {
		return Dashboard{}, contextErr
	}
	return Dashboard{Project: project, Tasks: tasks, Errors: errorsKB, Solutions: solutionsKB, FileIndex: fileIndex, Context: contextSummary}, nil
}

// Search performs lightweight keyword retrieval over project, task, file, error, and solution memory.
func (s *Store) Search(query string, limit int) (SearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if err := s.ensureEnabled(); err != nil {
		return SearchResponse{Query: strings.TrimSpace(query)}, err
	}
	index, _ := s.FileIndex()
	return s.searchResultsWithIndex(query, limit, index), nil
}

func (s *Store) searchResultsWithIndex(query string, limit int, index FileIndex) SearchResponse {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	response := SearchResponse{Query: strings.TrimSpace(query)}
	tokens := searchTokens(query)
	project, _ := s.Project()
	addScoredResult := func(result SearchResult, haystack string) {
		score := scoreText(tokens, haystack)
		if score <= 0 && len(tokens) > 0 {
			return
		}
		if len(tokens) == 0 {
			score = 1
		}
		result.Score = score
		result.Summary = trimMemoryText(result.Summary, maxSearchSummaryBytes)
		response.Results = append(response.Results, result)
	}
	if strings.TrimSpace(project.Content) != "" {
		addScoredResult(SearchResult{
			Kind:    "project",
			Title:   "Project memory",
			Path:    project.Path,
			Summary: project.Summary,
		}, project.Content)
	}
	contextSummary, _ := s.Context()
	if strings.TrimSpace(contextSummary.SearchSummary()) != "" {
		haystack := strings.Join([]string{
			contextSummary.Summary,
			strings.Join(contextSummary.RecentGoals, "\n"),
			strings.Join(contextSummary.Decisions, "\n"),
			strings.Join(contextSummary.PendingActions, "\n"),
			strings.Join(contextSummary.RelevantFiles, "\n"),
			strings.Join(contextSummary.ArtifactRefs, "\n"),
			contextSummary.Workflow,
			contextSummary.TaskStage,
			contextSummary.Reason,
		}, "\n")
		addScoredResult(SearchResult{
			Kind:    "context",
			Title:   "Compacted session context",
			Path:    filepath.ToSlash(contextMemoryRelativePath),
			Summary: contextSummary.SearchSummary(),
			Metadata: map[string]string{
				"context_id": contextSummary.ID,
				"agent":      contextSummary.ActiveAgent,
				"mode":       contextSummary.Mode,
			},
		}, haystack)
	}
	tasks, _ := s.Tasks(200)
	for _, task := range tasks {
		haystack := strings.Join([]string{
			task.UserGoal,
			strings.Join(task.KeyDecisions, "\n"),
			strings.Join(task.ModifiedFiles, "\n"),
			strings.Join(task.TestResults, "\n"),
			strings.Join(task.FailureReasons, "\n"),
			strings.Join(task.NextTodos, "\n"),
			strings.Join(task.ReusableLessons, "\n"),
		}, "\n")
		addScoredResult(SearchResult{
			Kind:    "task",
			Title:   task.UserGoal,
			Path:    filepath.ToSlash(filepath.Join(taskMemoryDirRelativePath, task.ID+".json")),
			Summary: task.SearchSummary(),
			Metadata: map[string]string{
				"task_id": task.ID,
				"agent":   task.AgentID,
				"mode":    task.Mode,
			},
		}, haystack)
	}
	for _, file := range index.Files {
		haystack := strings.Join([]string{file.Path, file.Language, file.Summary, strings.Join(file.Symbols, "\n")}, "\n")
		addScoredResult(SearchResult{
			Kind:    "file",
			Title:   file.Path,
			Path:    file.Path,
			Summary: file.Summary,
			Metadata: map[string]string{
				"hash":     file.Hash,
				"language": file.Language,
			},
		}, haystack)
	}
	errorsKB, _ := s.Errors()
	for _, item := range errorsKB.Errors {
		haystack := strings.Join([]string{item.Error, item.RootCause, item.Fix, strings.Join(item.RelatedFiles, "\n"), item.VerificationCommand}, "\n")
		addScoredResult(SearchResult{
			Kind:    "error",
			Title:   item.Error,
			Path:    filepath.ToSlash(errorMemoryRelativePath),
			Summary: item.SearchSummary(),
			Metadata: map[string]string{
				"error_id": item.ID,
				"resolved": fmt.Sprintf("%t", item.Resolved),
			},
		}, haystack)
	}
	solutionsKB, _ := s.Solutions()
	for _, item := range solutionsKB.Solutions {
		if item.Retired {
			continue
		}
		haystack := strings.Join([]string{
			item.ProblemSignature,
			item.Problem,
			item.Decision,
			item.Solution,
			strings.Join(item.Applicability, "\n"),
			strings.Join(item.InvalidWhen, "\n"),
			strings.Join(item.RelatedFiles, "\n"),
			item.VerificationCommand,
			item.Confidence,
		}, "\n")
		addScoredResult(SearchResult{
			Kind:    "solution",
			Title:   item.ProblemSignature,
			Path:    filepath.ToSlash(solutionMemoryRelativePath),
			Summary: item.SearchSummary(),
			Metadata: map[string]string{
				"solution_id": item.ID,
				"resolved":    fmt.Sprintf("%t", item.Resolved),
				"confidence":  item.Confidence,
			},
		}, haystack)
	}
	sort.SliceStable(response.Results, func(i, j int) bool {
		if response.Results[i].Score != response.Results[j].Score {
			return response.Results[i].Score > response.Results[j].Score
		}
		return response.Results[i].Title < response.Results[j].Title
	})
	response.Total = len(response.Results)
	if len(response.Results) > limit {
		response.Results = append([]SearchResult(nil), response.Results[:limit]...)
	}
	response.Returned = len(response.Results)
	return response
}

// PromptContext builds the concise memory block injected into model prompts.
func (s *Store) PromptContext(query string) (PromptContext, error) {
	return s.PromptContextFresh(context.Background(), query, "")
}

// PromptContextFresh builds prompt memory after ensuring the file index is
// current. taskID is recorded on selected file summaries so later diagnostics
// can explain which task last used summary-only file context.
func (s *Store) PromptContextFresh(ctx context.Context, query, taskID string) (PromptContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return PromptContext{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	index, rebuilt, indexErr := s.EnsureFreshFileIndex(ctx)
	project, _ := s.Project()
	promptCtx := PromptContext{Project: project}
	if strings.TrimSpace(project.Summary) != "" {
		promptCtx.Blocks = append(promptCtx.Blocks, PromptBlock{
			Kind:    "project",
			Title:   "Project memory",
			Ref:     project.Path,
			Summary: trimMemoryText(project.Summary, maxProjectPromptBytes),
		})
	}
	if contextSummary, err := s.Context(); err == nil && strings.TrimSpace(contextSummary.SearchSummary()) != "" {
		promptCtx.Blocks = append(promptCtx.Blocks, PromptBlock{
			Kind:                 "context",
			Title:                "Compacted session context",
			Ref:                  filepath.ToSlash(contextMemoryRelativePath),
			Summary:              trimMemoryText(contextSummary.SearchSummary(), 1200),
			ContentMode:          "summary",
			EstimatedSavedTokens: contextSummary.EstimatedSavedTokens,
		})
	}
	results, _ := s.searchWithFileIndex(query, 8, index)
	retrievalBlocks := 0
	fileRetrievalSeen := make(map[string]struct{})
	solutionRetrievalIDs := make([]string, 0, 2)
	for _, result := range results.Results {
		if result.Kind == "project" || result.Kind == "context" {
			continue
		}
		if result.Kind == "solution" && len(solutionRetrievalIDs) >= 2 {
			continue
		}
		block := PromptBlock{
			Kind:     result.Kind,
			Title:    result.Title,
			Ref:      firstString(result.Metadata["solution_id"], result.Metadata["task_id"], result.Metadata["error_id"], result.Path),
			Summary:  trimMemoryText(result.Summary, 700),
			Score:    result.Score,
			Hash:     result.Metadata["hash"],
			Language: result.Metadata["language"],
		}
		if result.Kind == "file" {
			fileRetrievalSeen[cleanRelativeFilePath(result.Path)] = struct{}{}
			if file, ok := fileSummaryByPath(index, result.Path); ok {
				block = promptBlockForFileSummary(file)
				block.Score = result.Score
			}
		}
		if result.Kind == "solution" {
			block.ContentMode = "decision"
			if id := strings.TrimSpace(result.Metadata["solution_id"]); id != "" {
				solutionRetrievalIDs = append(solutionRetrievalIDs, id)
			}
		}
		promptCtx.Blocks = append(promptCtx.Blocks, block)
		retrievalBlocks++
		if retrievalBlocks >= 4 {
			break
		}
	}
	if len(solutionRetrievalIDs) > 0 {
		if err := s.MarkSolutionsUsed(solutionRetrievalIDs); err != nil {
			promptCtx.Omitted = append(promptCtx.Omitted, "solution memory usage update failed: "+err.Error())
		}
	}
	selectedFiles := selectedPromptFiles(index, query, maxPromptFileBlocks)
	if len(selectedFiles) > 0 {
		if err := s.MarkFilesUsedByTask(selectedFiles, taskID); err != nil {
			promptCtx.Omitted = append(promptCtx.Omitted, "file summary usage metadata update failed: "+err.Error())
		}
		for _, file := range selectedFiles {
			if _, ok := fileRetrievalSeen[cleanRelativeFilePath(file.Path)]; ok {
				continue
			}
			promptCtx.Blocks = append(promptCtx.Blocks, promptBlockForFileSummary(file))
		}
	}
	if rebuilt {
		if indexErr != nil {
			promptCtx.Omitted = append(promptCtx.Omitted, "file summary index refresh failed: "+indexErr.Error())
		} else {
			promptCtx.Omitted = append(promptCtx.Omitted, "file summary index refreshed before prompt retrieval")
		}
	}
	if results.Total > retrievalBlocks {
		promptCtx.Omitted = append(promptCtx.Omitted, fmt.Sprintf("%d lower-scoring memory results omitted", results.Total-retrievalBlocks))
	}
	promptCtx.EstimatedSavedTokens = estimateSavedTokens(promptCtx)
	return promptCtx, nil
}

func (s *Store) searchWithFileIndex(query string, limit int, index FileIndex) (SearchResponse, error) {
	if err := s.ensureEnabled(); err != nil {
		return SearchResponse{Query: strings.TrimSpace(query)}, err
	}
	return s.searchResultsWithIndex(query, limit, index), nil
}

func (s *Store) MarkFilesUsedByTask(files []FileSummary, taskID string) error {
	if s == nil || len(files) == 0 {
		return nil
	}
	index, err := s.FileIndex()
	if err != nil {
		return err
	}
	if len(index.Files) == 0 {
		return nil
	}
	targets := make(map[string]struct{}, len(files))
	for _, file := range files {
		if path := cleanRelativeFilePath(file.Path); path != "" {
			targets[path] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	changed := false
	for i := range index.Files {
		if _, ok := targets[cleanRelativeFilePath(index.Files[i].Path)]; !ok {
			continue
		}
		index.Files[i].LastReadAt = now
		if strings.TrimSpace(taskID) != "" {
			index.Files[i].LastUsedByTask = strings.TrimSpace(taskID)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return writeJSONFile(filepath.Join(s.workspaceRoot, fileIndexRelativePath), index)
}

func (s *Store) MarkFilePathsUsedByTask(ctx context.Context, paths []string, taskID string) error {
	if s == nil || len(paths) == 0 {
		return nil
	}
	if _, _, err := s.EnsureFreshFileIndex(ctx); err != nil {
		return err
	}
	files := make([]FileSummary, 0, len(paths))
	for _, path := range paths {
		path = cleanRelativeFilePath(path)
		if path == "" {
			continue
		}
		files = append(files, FileSummary{Path: path})
	}
	return s.MarkFilesUsedByTask(files, taskID)
}

// PromptText renders this memory context for the stable system prompt.
func (c PromptContext) PromptText() string {
	if len(c.Blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Memory context:\n")
	b.WriteString("- Use these summaries and refs as lightweight context; load full artifacts/files only when needed.\n")
	b.WriteString("- If a solution block matches the current problem and its invalid conditions do not apply, reuse that decision before asking the operator to decide again.\n")
	for _, block := range c.Blocks {
		title := strings.TrimSpace(block.Title)
		if title == "" {
			title = block.Kind
		}
		fmt.Fprintf(&b, "- %s", block.Kind)
		if strings.TrimSpace(title) != "" {
			fmt.Fprintf(&b, " %q", title)
		}
		if strings.TrimSpace(block.Ref) != "" {
			fmt.Fprintf(&b, " ref=%s", block.Ref)
		}
		if strings.TrimSpace(block.Hash) != "" {
			fmt.Fprintf(&b, " hash=%s", compactMemoryHash(block.Hash))
		}
		if strings.TrimSpace(block.Language) != "" {
			fmt.Fprintf(&b, " language=%s", block.Language)
		}
		if block.Size > 0 {
			fmt.Fprintf(&b, " size=%d", block.Size)
		}
		if strings.TrimSpace(block.ContentMode) != "" {
			fmt.Fprintf(&b, " content=%s", block.ContentMode)
		}
		if block.Score > 0 {
			fmt.Fprintf(&b, " score=%d", block.Score)
		}
		b.WriteString(": ")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(block.Summary), "\n", " "))
		b.WriteString("\n")
	}
	if len(c.Omitted) > 0 {
		b.WriteString("- Omitted: ")
		b.WriteString(strings.Join(c.Omitted, "; "))
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Store) ensureEnabled() error {
	if s == nil || strings.TrimSpace(s.workspaceRoot) == "" {
		return fmt.Errorf("memory store is not configured")
	}
	return nil
}

func defaultProjectMemory() string {
	return `# Project Memory

## Project Goal
- Describe what this workspace is trying to accomplish.

## Tech Stack
- Record languages, frameworks, services, and important runtime assumptions.

## Directory Summary
- Map the key folders and ownership boundaries.

## Common Commands
- Add build, test, lint, and run commands that are safe to reuse.

## Key Constraints
- Record sandbox, approval, compatibility, release, and style constraints.

## Known Risks
- Track brittle areas, performance risks, security concerns, and migration notes.

## User Preferences
- Capture durable preferences that should guide future work.

## Current Iteration Goal
- Summarize the active implementation target and acceptance criteria.
`
}

func isDefaultProjectMemory(content string) bool {
	normalized := normalizeMemoryDocument(content)
	return normalized == normalizeMemoryDocument(defaultProjectMemory())
}

func normalizeMemoryDocument(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	return content
}

func (s *Store) generatedProjectMemoryContent(ctx context.Context) (string, bool) {
	if s == nil || strings.TrimSpace(s.workspaceRoot) == "" {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	index, _, err := s.EnsureFreshFileIndex(ctx)
	if err != nil {
		index, _ = s.FileIndex()
	}
	title, goalLines := s.generatedProjectGoals(index)
	techStack := generatedProjectTechStack(index)
	directories := generatedProjectDirectories(index)
	commands := s.generatedProjectCommands()
	constraints := s.generatedProjectConstraints(index)
	risks := s.generatedProjectRisks(index)
	preferences := generatedProjectPreferences()
	iteration := s.generatedIterationGoals()
	if title == "" &&
		len(goalLines) == 0 &&
		len(techStack) == 0 &&
		len(directories) == 0 &&
		len(commands) == 0 &&
		len(constraints) == 0 &&
		len(risks) == 0 &&
		len(preferences) == 0 &&
		len(iteration) == 0 {
		return "", false
	}
	if title == "" {
		title = filepath.Base(strings.TrimSpace(s.workspaceRoot))
		if title == "" || title == "." || title == string(filepath.Separator) {
			title = "Workspace"
		}
	}
	var builder strings.Builder
	builder.WriteString("# 项目画像\n\n")
	builder.WriteString("> 这是根据当前工作区结构自动生成的初始画像。后续目标、约束或偏好发生变化时，请直接更新这里，后续提示词会优先注入这份摘要。\n\n")
	builder.WriteString("## 项目目标\n")
	writeProjectMemoryList(&builder, goalLines)
	builder.WriteString("\n## 技术栈\n")
	writeProjectMemoryList(&builder, techStack)
	builder.WriteString("\n## 目录结构摘要\n")
	writeProjectMemoryList(&builder, directories)
	builder.WriteString("\n## 常用命令\n")
	writeProjectMemoryList(&builder, commands)
	builder.WriteString("\n## 关键约束\n")
	writeProjectMemoryList(&builder, constraints)
	builder.WriteString("\n## 已知风险\n")
	writeProjectMemoryList(&builder, risks)
	builder.WriteString("\n## 用户偏好\n")
	writeProjectMemoryList(&builder, preferences)
	builder.WriteString("\n## 当前迭代目标\n")
	writeProjectMemoryList(&builder, iteration)
	return strings.TrimSpace(builder.String()) + "\n", true
}

func writeProjectMemoryList(builder *strings.Builder, items []string) {
	if builder == nil {
		return
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		builder.WriteString("- ")
		builder.WriteString(item)
		builder.WriteString("\n")
	}
}

func (s *Store) generatedProjectGoals(index FileIndex) (string, []string) {
	title, summary := s.workspaceReadmeSummary()
	lines := make([]string, 0, 4)
	if title != "" {
		lines = append(lines, fmt.Sprintf("当前工作区识别为 `%s`。", title))
	}
	if summary != "" {
		lines = append(lines, summary)
	}
	if profile := generatedWorkspaceProfile(index); profile != "" {
		lines = append(lines, profile)
	}
	if len(lines) == 0 {
		lines = append(lines, "补充这个工作区面向的目标用户、核心问题和当前交付目标。")
	}
	return title, lines
}

func generatedWorkspaceProfile(index FileIndex) string {
	hasGo := false
	hasWeb := false
	hasWorkflows := false
	for _, file := range index.Files {
		switch strings.ToLower(strings.TrimSpace(file.Language)) {
		case "go":
			hasGo = true
		case "javascript", "typescript", "html", "css":
			hasWeb = true
		}
		path := strings.ToLower(strings.TrimSpace(file.Path))
		if strings.HasPrefix(path, "workflows/") || strings.HasPrefix(path, "templates/workflows/") {
			hasWorkflows = true
		}
	}
	switch {
	case hasGo && hasWeb && hasWorkflows:
		return "工作区同时包含 Go 后端、内嵌 Web Studio 前端以及 workflow 资源，适合围绕 Agent 平台能力持续迭代。"
	case hasGo && hasWeb:
		return "工作区同时包含 Go 后端和前端资源，修改时要同步考虑接口字段、页面展示和交互反馈。"
	case hasGo && hasWorkflows:
		return "工作区以 Go 运行时和 workflow 资源为主，适合围绕多阶段执行、上下文传递与资源编排优化。"
	default:
		return ""
	}
}

func generatedProjectTechStack(index FileIndex) []string {
	counts := make(map[string]int)
	for _, file := range index.Files {
		language := strings.TrimSpace(file.Language)
		if language == "" {
			continue
		}
		counts[language]++
	}
	type entry struct {
		name  string
		count int
	}
	items := make([]entry, 0, len(counts))
	for name, count := range counts {
		items = append(items, entry{name: name, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
	})
	lines := make([]string, 0, 6)
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("%s（%d 个索引文件）", item.name, item.count))
		if len(lines) >= 6 {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "尚未从文件索引中识别出明确技术栈。")
	}
	return lines
}

func generatedProjectDirectories(index FileIndex) []string {
	type dirStat struct {
		files int
		bytes int64
	}
	stats := make(map[string]dirStat)
	for _, file := range index.Files {
		segment := firstPathSegment(file.Path)
		if segment == "" {
			continue
		}
		item := stats[segment]
		item.files++
		item.bytes += file.Size
		stats[segment] = item
	}
	type entry struct {
		name  string
		files int
		bytes int64
	}
	items := make([]entry, 0, len(stats))
	for name, stat := range stats {
		items = append(items, entry{name: name, files: stat.files, bytes: stat.bytes})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].files != items[j].files {
			return items[i].files > items[j].files
		}
		return items[i].name < items[j].name
	})
	lines := make([]string, 0, 6)
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("`%s/`：%d 个文件，%s。", item.name, item.files, describeProjectDirectory(item.name)))
		if len(lines) >= 6 {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "当前索引尚未收集到足够目录信息。")
	}
	return lines
}

func firstPathSegment(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func describeProjectDirectory(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cmd":
		return "可执行入口与启动命令"
	case "internal":
		return "核心运行时、后端接口与内部实现"
	case "docs":
		return "面向用户或开发者的文档"
	case "scripts":
		return "验证、构建或烟雾测试脚本"
	case "workflows":
		return "工作流资源与编排定义"
	case "templates":
		return "可复用模板与脚手架资源"
	case "examples":
		return "示例配置与演示材料"
	case "web":
		return "前端页面或静态资源"
	default:
		return "主要源码或资源目录"
	}
}

func (s *Store) generatedProjectCommands() []string {
	root := strings.TrimSpace(s.workspaceRoot)
	if root == "" {
		return []string{"按项目实际情况补充常用构建、测试和运行命令。"}
	}
	type candidate struct {
		path string
		cmd  string
	}
	candidates := []candidate{
		{path: "go.mod", cmd: "`go test ./...`"},
		{path: filepath.Join("cmd", "goflow"), cmd: "`go run ./cmd/goflow`"},
		{path: filepath.Join("scripts", "smoke_http_browser.py"), cmd: "`python scripts/smoke_http_browser.py`"},
		{path: filepath.Join("scripts", "smoke_http_studio.py"), cmd: "`python scripts/smoke_http_studio.py`"},
		{path: "package.json", cmd: "检查 `package.json` 中定义的前端脚本并记录常用命令。"},
	}
	lines := make([]string, 0, len(candidates))
	for _, item := range candidates {
		if _, err := os.Stat(filepath.Join(root, item.path)); err == nil {
			lines = append(lines, item.cmd)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "按项目实际情况补充常用构建、测试和运行命令。")
	}
	return lines
}

func (s *Store) generatedProjectConstraints(index FileIndex) []string {
	lines := make([]string, 0, 4)
	if len(index.Files) > 0 {
		lines = append(lines, "默认应优先复用文件索引、摘要和引用，只有在文件变化或明确需要时再读取全文。")
	}
	if hasIndexedPath(index, "internal/api/web/") && hasIndexedPath(index, "internal/agent/") {
		lines = append(lines, "涉及 Go 后端字段或运行状态时，需要同步检查 Web Studio 展示和中英文文案。")
	}
	if hasIndexedPath(index, "workflows/") {
		lines = append(lines, "工作流阶段之间依赖结构化输出、artifact 引用和上下文契约，修改时要兼顾上下游兼容性。")
	}
	lines = append(lines, "`.goflow/` 下的 memory、index、artifacts 与 session 文件属于运行时数据，不应被随意破坏。")
	return dedupeStrings(lines)
}

func (s *Store) generatedProjectRisks(index FileIndex) []string {
	lines := make([]string, 0, 4)
	if hasIndexedPath(index, "internal/api/web/") && hasIndexedPath(index, "internal/agent/") {
		lines = append(lines, "后端生成文本与前端本地化字典一旦不同步，中文模式下容易出现中英混杂。")
	}
	if hasIndexedPath(index, "workflows/") {
		lines = append(lines, "工作流节点输出字段一旦漂移，可能导致下游阶段、运行详情和资源说明同时受影响。")
	}
	if len(index.Files) == 0 {
		lines = append(lines, "文件索引为空时，项目画像和检索式记忆的参考价值会明显下降。")
	} else {
		lines = append(lines, "如果文件索引未及时刷新，模型可能继续依赖旧摘要和旧 hash。")
	}
	if len(lines) == 0 {
		lines = append(lines, "补充当前项目最脆弱、最耗时或最容易回归的区域。")
	}
	return dedupeStrings(lines)
}

func generatedProjectPreferences() []string {
	return []string{
		"默认采用摘要优先、按需懒加载的上下文策略，避免把完整历史直接送入模型。",
		"长期有效的约束、命令和目录认知应写入项目画像，而不是散落在一次性任务对话里。",
	}
}

func (s *Store) generatedIterationGoals() []string {
	tasks, _ := s.Tasks(3)
	if len(tasks) == 0 {
		return []string{"当前还没有任务摘要记录；完成一次任务后，这里会自动带入最近迭代目标。"}
	}
	lines := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if strings.TrimSpace(task.UserGoal) == "" {
			continue
		}
		line := fmt.Sprintf("最近任务：%s", strings.TrimSpace(task.UserGoal))
		if len(task.ModifiedFiles) > 0 {
			line += fmt.Sprintf("（涉及 %d 个文件）", len(task.ModifiedFiles))
		}
		lines = append(lines, line)
		if len(lines) >= 2 {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "根据最近完成的任务补充当前迭代目标和验收标准。")
	}
	return lines
}

func hasIndexedPath(index FileIndex, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(filepath.ToSlash(prefix)))
	if prefix == "" {
		return false
	}
	for _, file := range index.Files {
		if strings.HasPrefix(strings.ToLower(filepath.ToSlash(strings.TrimSpace(file.Path))), prefix) {
			return true
		}
	}
	return false
}

func (s *Store) workspaceReadmeSummary() (string, string) {
	root := strings.TrimSpace(s.workspaceRoot)
	if root == "" {
		return "", ""
	}
	for _, name := range []string{"README.md", "README.zh-CN.md", "README.txt"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
		if content == "" {
			continue
		}
		title := markdownTitle(content)
		summary := markdownIntroSummary(content)
		return title, summary
	}
	return "", ""
}

func markdownTitle(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func markdownIntroSummary(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	lines := make([]string, 0, 3)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= 3 {
			break
		}
	}
	summary := trimMemoryText(strings.Join(lines, " "), 320)
	if summary == "" {
		return ""
	}
	return "README 摘要：" + summary
}

func summarizeMarkdownMemory(content string, maxBytes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		kept = append(kept, trimmed)
	}
	return trimMemoryText(strings.Join(kept, "\n"), maxBytes)
}

func summarizeFile(path, rel string, info os.FileInfo) (FileSummary, error) {
	item := FileSummary{
		Path:     rel,
		Size:     info.Size(),
		MTime:    info.ModTime().UTC().Format(time.RFC3339Nano),
		Language: languageForPath(rel),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return item, err
	}
	sum := sha256.Sum256(data)
	item.Hash = hex.EncodeToString(sum[:])
	if info.Size() > maxIndexedFileBytes {
		item.Summary = fmt.Sprintf("Large %s file (%d bytes); content summary skipped until explicitly read.", fallbackText(item.Language, "workspace"), info.Size())
		return item, nil
	}
	if !utf8.Valid(data) {
		item.Summary = fmt.Sprintf("Binary or non-UTF-8 file (%d bytes).", len(data))
		return item, nil
	}
	content := strings.TrimPrefix(string(data), "\ufeff")
	item.Symbols = extractSymbols(rel, content)
	item.Summary = summarizeFileContent(rel, item.Language, content, item.Symbols)
	return item, nil
}

func summarizeFileContent(path, language, content string, symbols []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Sprintf("Empty %s file.", fallbackText(language, "workspace"))
	}
	var parts []string
	if language != "" {
		parts = append(parts, language+" file")
	}
	if len(symbols) > 0 {
		limit := len(symbols)
		if limit > 8 {
			limit = 8
		}
		parts = append(parts, "symbols: "+strings.Join(symbols[:limit], ", "))
	}
	firstLines := ""
	if shouldIncludeFileSummaryTopContent(language, symbols) {
		firstLines = firstMeaningfulLines(content, 5)
	}
	if firstLines != "" {
		parts = append(parts, "top content: "+firstLines)
	}
	if len(parts) == 0 {
		return "Workspace file " + path
	}
	return trimMemoryText(strings.Join(parts, "; "), 900)
}

func shouldIncludeFileSummaryTopContent(language string, symbols []string) bool {
	if len(symbols) == 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go", "javascript", "typescript", "python", "rust", "shell", "css", "html":
		return false
	default:
		return true
	}
}

func firstMeaningfulLines(content string, limit int) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	lines := make([]string, 0, limit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return strings.Join(lines, " ")
}

var (
	goSymbolPattern     = regexp.MustCompile(`^\s*(func|type)\s+(\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	jsSymbolPattern     = regexp.MustCompile(`^\s*(export\s+)?(async\s+)?(function|class)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	jsConstFuncPattern  = regexp.MustCompile(`^\s*(export\s+)?(const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(async\s*)?(\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)?\s*=>`)
	pySymbolPattern     = regexp.MustCompile(`^\s*(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustSymbolPattern   = regexp.MustCompile(`^\s*(pub\s+)?(fn|struct|enum|trait|impl)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	markdownHeadPattern = regexp.MustCompile(`^\s{0,3}#{1,4}\s+(.+)$`)
)

func extractSymbols(path, content string) []string {
	lang := languageForPath(path)
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	symbols := make([]string, 0, 24)
	seen := map[string]struct{}{}
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxSymbolScanLines || len(symbols) >= 24 {
			break
		}
		line := scanner.Text()
		var symbol string
		switch lang {
		case "go":
			if match := goSymbolPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = match[len(match)-1]
			}
		case "javascript", "typescript":
			if match := jsSymbolPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = match[len(match)-1]
			} else if match := jsConstFuncPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = match[3]
			}
		case "python":
			if match := pySymbolPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = match[2]
			}
		case "rust":
			if match := rustSymbolPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = match[3]
			}
		case "markdown":
			if match := markdownHeadPattern.FindStringSubmatch(line); len(match) > 0 {
				symbol = strings.TrimSpace(match[1])
			}
		}
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		key := strings.ToLower(symbol)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		symbols = append(symbols, symbol)
	}
	return symbols
}

func languageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".sh", ".bash", ".ps1", ".cmd", ".bat":
		return "shell"
	default:
		return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
}

func selectedPromptFiles(index FileIndex, query string, limit int) []FileSummary {
	if limit <= 0 || len(index.Files) == 0 {
		return nil
	}
	lowerQuery := strings.ToLower(query)
	tokens := searchTokens(query)
	type scoredFile struct {
		file  FileSummary
		score int
	}
	scored := make([]scoredFile, 0, len(index.Files))
	for _, file := range index.Files {
		if strings.TrimSpace(file.Path) == "" || strings.TrimSpace(file.Summary) == "" {
			continue
		}
		haystack := strings.Join([]string{
			file.Path,
			file.Language,
			file.Summary,
			strings.Join(file.Symbols, "\n"),
		}, "\n")
		score := scoreText(tokens, haystack)
		if score <= 0 && len(tokens) > 0 {
			continue
		}
		if strings.Contains(lowerQuery, strings.ToLower(file.Path)) {
			score += 40
		}
		if score <= 0 {
			score = 1
		}
		scored = append(scored, scoredFile{file: file, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		leftSize := scored[i].file.Size
		rightSize := scored[j].file.Size
		if leftSize != rightSize {
			return leftSize < rightSize
		}
		return strings.ToLower(scored[i].file.Path) < strings.ToLower(scored[j].file.Path)
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	files := make([]FileSummary, 0, len(scored))
	for _, item := range scored {
		files = append(files, item.file)
	}
	return files
}

func fileSummaryByPath(index FileIndex, path string) (FileSummary, bool) {
	target := cleanRelativeFilePath(path)
	if target == "" {
		return FileSummary{}, false
	}
	for _, file := range index.Files {
		if cleanRelativeFilePath(file.Path) == target {
			return file, true
		}
	}
	return FileSummary{}, false
}

func promptBlockForFileSummary(file FileSummary) PromptBlock {
	symbols := strings.Join(file.Symbols, ", ")
	summaryParts := []string{
		strings.TrimSpace(file.Summary),
	}
	if symbols != "" {
		summaryParts = append(summaryParts, "symbols="+symbols)
	}
	return PromptBlock{
		Kind:                 "file",
		Title:                file.Path,
		Ref:                  file.Path,
		Summary:              trimMemoryText(strings.Join(summaryParts, "; "), 900),
		Hash:                 file.Hash,
		Language:             file.Language,
		Size:                 file.Size,
		MTime:                file.MTime,
		ContentMode:          "summary",
		EstimatedSavedTokens: estimatedFileSummarySavedTokens(file),
	}
}

func estimatedFileSummarySavedTokens(file FileSummary) int {
	savedBytes := int(file.Size) - len([]byte(file.Summary))
	if savedBytes <= 0 {
		return 0
	}
	return savedBytes / 4
}

func cleanRelativeFilePath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." || path == "/" || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
		return ""
	}
	return path
}

func normalizeSolutionMemory(item SolutionMemory) SolutionMemory {
	item.ID = strings.TrimSpace(item.ID)
	item.ProblemSignature = trimMemoryText(item.ProblemSignature, 500)
	if item.ProblemSignature == "" {
		item.ProblemSignature = trimMemoryText(firstString(item.Problem, item.Decision, item.Solution), 500)
	}
	item.Problem = trimMemoryText(item.Problem, 1200)
	item.Decision = trimMemoryText(item.Decision, 1200)
	item.Solution = trimMemoryText(item.Solution, 1400)
	item.Applicability = trimStringList(item.Applicability, 500)
	item.InvalidWhen = trimStringList(item.InvalidWhen, 500)
	item.RelatedFiles = dedupeStrings(item.RelatedFiles)
	item.VerificationCommand = trimMemoryText(item.VerificationCommand, 500)
	item.Confidence = strings.ToLower(strings.TrimSpace(item.Confidence))
	item.RetiredReason = trimMemoryText(item.RetiredReason, 500)
	item.SupersededBy = trimMemoryText(item.SupersededBy, 200)
	switch item.Confidence {
	case "high", "medium", "low":
	default:
		if item.Resolved {
			item.Confidence = "medium"
		} else if item.Confidence != "" {
			item.Confidence = trimMemoryText(item.Confidence, 80)
		}
	}
	return item
}

func normalizeSolutionSignature(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func solutionFromTaskSummary(summary TaskSummary) (SolutionMemory, bool) {
	if strings.TrimSpace(summary.UserGoal) == "" {
		return SolutionMemory{}, false
	}
	decision := firstString(summary.KeyDecisions...)
	solution := firstString(summary.ReusableLessons...)
	if strings.TrimSpace(decision) == "" && strings.TrimSpace(solution) == "" {
		return SolutionMemory{}, false
	}
	signature := strings.Join(nonEmptyStrings(
		summary.UserGoal,
		firstString(summary.ModifiedFiles...),
		decision,
	), " | ")
	if signature == "" {
		return SolutionMemory{}, false
	}
	return SolutionMemory{
		ProblemSignature:    signature,
		Problem:             summary.UserGoal,
		Decision:            decision,
		Solution:            solution,
		Applicability:       nonEmptyStrings("same goal or failure signature recurs", "related files or workflow resources overlap"),
		InvalidWhen:         nonEmptyStrings("requirements, provider behavior, or resource schema changed"),
		RelatedFiles:        append([]string(nil), summary.ModifiedFiles...),
		VerificationCommand: firstVerificationCommand(summary.TestResults),
		Confidence:          solutionConfidence(summary),
		Resolved:            len(summary.FailureReasons) == 0 && strings.TrimSpace(solution) != "",
	}, true
}

func activeSolutionCount(items []SolutionMemory) (active, retired int) {
	for _, item := range items {
		if item.Retired {
			retired++
			continue
		}
		active++
	}
	return active, retired
}

func solutionConfidence(summary TaskSummary) string {
	if len(summary.FailureReasons) > 0 {
		return "low"
	}
	if strings.TrimSpace(firstVerificationCommand(summary.TestResults)) != "" {
		return "high"
	}
	return "medium"
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func skipIndexDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".goflow", ".codex", ".claude", ".gocache", ".gotmp", ".gomodcache", ".venv", "venv", "node_modules", "vendor", "dist", "bin", "coverage", "__pycache__":
		return true
	default:
		return false
	}
}

func skipIndexFile(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".test") || strings.HasSuffix(lower, ".out") || strings.HasSuffix(lower, ".pyc") {
		return true
	}
	return false
}

func indexedBytes(files []FileSummary) int64 {
	var total int64
	for _, file := range files {
		total += file.Size
	}
	return total
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	return decoder.Decode(target)
}

func searchTokens(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(query)), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.')
	})
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "._-")
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func scoreText(tokens []string, text string) int {
	if len(tokens) == 0 {
		return 1
	}
	text = strings.ToLower(text)
	score := 0
	for _, token := range tokens {
		if token == "" {
			continue
		}
		count := strings.Count(text, token)
		if count > 0 {
			score += 10 + count
		}
	}
	if score > 0 {
		for _, token := range tokens {
			if strings.Contains(text, token) {
				score++
			}
		}
	}
	return score
}

func trimMemoryText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	trimmed := value[:maxBytes]
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return strings.TrimRight(trimmed, "\r\n\t ") + "..."
}

func trimStringList(values []string, maxBytes int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = trimMemoryText(value, maxBytes)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func compactMemoryHash(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "sha256:") {
		value = strings.TrimPrefix(value, "sha256:")
	}
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func sanitizeFileComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if (r == '-' || r == '_' || r == '.') && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "item"
	}
	return out
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstVerificationCommand(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "go test") || strings.Contains(lower, "npm test") || strings.Contains(lower, "pytest") || strings.Contains(lower, "cargo test") {
			return value
		}
	}
	return firstString(values...)
}

func estimateSavedTokens(ctx PromptContext) int {
	savedTokens := 0
	summaryBytes := 0
	for _, block := range ctx.Blocks {
		if block.EstimatedSavedTokens > 0 {
			savedTokens += block.EstimatedSavedTokens
		}
		summaryBytes += len([]byte(block.Summary))
	}
	if savedTokens > 0 {
		return savedTokens
	}
	if summaryBytes == 0 {
		return 0
	}
	return summaryBytes / 2 / 4
}

func fallbackText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
