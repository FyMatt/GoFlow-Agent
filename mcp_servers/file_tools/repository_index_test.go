package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRepositoryIndexSearchFindsFilesBeyondLegacyWalkLimit(t *testing.T) {
	workspaceRoot = t.TempDir()
	for i := 0; i < maxSearchFiles+25; i++ {
		path := filepath.Join(workspaceRoot, "generated", fmt.Sprintf("entry-%04d.txt", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ordinary\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(workspaceRoot, "zz-target-module.go")
	if err := os.WriteFile(target, []byte("package target\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := callRepositoryIndexTool(t, "search_files", map[string]any{
		"query": "zz-target-module", "include_content": false,
	})
	if payload["search_strategy"] != "repository-index" {
		t.Fatalf("expected indexed search, got %#v", payload)
	}
	if _, ok := payload["duration_ms"]; !ok {
		t.Fatalf("indexed search must expose duration_ms, got %#v", payload)
	}
	matches, _ := payload["matches"].([]any)
	if len(matches) != 1 || !strings.Contains(fmt.Sprint(matches[0]), "zz-target-module.go") {
		t.Fatalf("expected file beyond legacy walk limit, got %#v", payload)
	}
	if entries, _ := payload["index_entries"].(float64); entries <= maxSearchFiles {
		t.Fatalf("expected complete index beyond %d entries, got %#v", maxSearchFiles, payload)
	}
}

func TestRepositoryIndexRefreshesDirtyFilePathIncrementally(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "go.mod"), []byte("module example.test/index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := callRepositoryIndexTool(t, "repository_map", map[string]any{})
	if initial["index_rebuilt"] != true {
		t.Fatalf("expected initial index build, got %#v", initial)
	}

	callRepositoryIndexTool(t, "write_file", map[string]any{"path": "new-feature.go", "content": "package feature\n"})
	payload := callRepositoryIndexTool(t, "search_files", map[string]any{
		"query": "new-feature", "include_content": false,
	})
	if updated, _ := payload["index_updated_paths"].(float64); updated != 1 {
		t.Fatalf("expected one incremental update, got %#v", payload)
	}
	if payload["index_rebuilt"] == true {
		t.Fatalf("file update should not rebuild the full index: %#v", payload)
	}
}

func TestRepositoryIndexConcurrentWritesInvalidateAndSearch(t *testing.T) {
	workspaceRoot = t.TempDir()
	if _, _, err := loadRepositoryIndex(true); err != nil {
		t.Fatal(err)
	}

	const writers = 24
	var group sync.WaitGroup
	group.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer group.Done()
			name := fmt.Sprintf("concurrent-%02d.go", i)
			params, err := json.Marshal(map[string]any{
				"name": "write_file",
				"arguments": map[string]any{
					"path":    name,
					"content": fmt.Sprintf("package concurrent%d\n", i),
				},
			})
			if err != nil {
				t.Errorf("marshal write %s: %v", name, err)
				return
			}
			result := callTool(params)
			if result["is_error"] == true {
				t.Errorf("concurrent write %s failed: %v", name, result["content"])
			}
		}()
	}
	group.Wait()

	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("concurrent-%02d.go", i)
		payload := callRepositoryIndexTool(t, "search_files", map[string]any{
			"query": name, "include_content": false,
		})
		matches, _ := payload["matches"].([]any)
		if len(matches) != 1 || !strings.Contains(fmt.Sprint(matches[0]), name) {
			t.Fatalf("concurrent write %s was not indexed: %#v", name, payload)
		}
		if payload["index_rebuilt"] == true {
			t.Fatalf("concurrent file update should use incremental index refresh: %#v", payload)
		}
	}
}

func TestRepositoryMapForceRefreshSeesExternalChanges(t *testing.T) {
	workspaceRoot = t.TempDir()
	callRepositoryIndexTool(t, "repository_map", map[string]any{})
	if err := os.WriteFile(filepath.Join(workspaceRoot, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stale := callRepositoryIndexTool(t, "repository_map", map[string]any{})
	if strings.Contains(fmt.Sprint(stale["project_markers"]), "package.json") {
		t.Fatalf("external changes should require explicit refresh, got %#v", stale)
	}
	refreshed := callRepositoryIndexTool(t, "repository_map", map[string]any{"refresh": true})
	if refreshed["index_rebuilt"] != true || !strings.Contains(fmt.Sprint(refreshed["project_markers"]), "package.json") {
		t.Fatalf("expected forced refresh to include external change, got %#v", refreshed)
	}
}

func TestRepositoryIndexLargeWorkspaceAcceptance(t *testing.T) {
	if os.Getenv("GOFLOW_LARGE_INDEX_TEST") != "1" {
		t.Skip("set GOFLOW_LARGE_INDEX_TEST=1 to run the 100k-file repository index acceptance")
	}
	fileCount := 100000
	if raw := strings.TrimSpace(os.Getenv("GOFLOW_LARGE_INDEX_FILES")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 10000 {
			t.Fatalf("GOFLOW_LARGE_INDEX_FILES must be an integer >= 10000")
		}
		fileCount = parsed
	}
	workspaceRoot = t.TempDir()
	for directory := 0; directory < (fileCount+999)/1000; directory++ {
		dir := filepath.Join(workspaceRoot, fmt.Sprintf("module-%03d", directory))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		start := directory * 1000
		end := start + 1000
		if end > fileCount {
			end = fileCount
		}
		for i := start; i < end; i++ {
			name := fmt.Sprintf("source-%06d.go", i)
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	coldStarted := time.Now()
	index, coldStatus, err := loadRepositoryIndex(true)
	if err != nil {
		t.Fatal(err)
	}
	coldDuration := time.Since(coldStarted)
	if len(index.Entries) < fileCount {
		t.Fatalf("expected at least %d indexed entries, got %d", fileCount, len(index.Entries))
	}
	if !coldStatus.Rebuilt || coldDuration > 60*time.Second {
		t.Fatalf("cold index gate failed: rebuilt=%v duration=%s", coldStatus.Rebuilt, coldDuration)
	}

	warmStarted := time.Now()
	result, err := searchWorkspaceFiles(workspaceRoot, fmt.Sprintf("source-%06d", fileCount-1), 10, false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	warmDuration := time.Since(warmStarted)
	if warmDuration > 500*time.Millisecond || !strings.Contains(fmt.Sprint(result["matches"]), fmt.Sprintf("source-%06d.go", fileCount-1)) {
		t.Fatalf("warm search gate failed: duration=%s result=%#v", warmDuration, result)
	}

	changed := filepath.Join(workspaceRoot, "module-000", "incremental-target.go")
	if err := os.WriteFile(changed, []byte("package incremental\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markIndexInvalidated(map[string]any{}, changed)
	incrementalStarted := time.Now()
	updated, updateStatus, err := loadRepositoryIndex(false)
	if err != nil {
		t.Fatal(err)
	}
	incrementalDuration := time.Since(incrementalStarted)
	if updateStatus.Rebuilt || updateStatus.UpdatedPaths != 1 || incrementalDuration > time.Second || len(updated.Entries) != len(index.Entries)+1 {
		t.Fatalf("incremental index gate failed: rebuilt=%v updated=%d duration=%s entries=%d", updateStatus.Rebuilt, updateStatus.UpdatedPaths, incrementalDuration, len(updated.Entries))
	}
	t.Logf("repository-index acceptance files=%d cold=%s warm=%s incremental=%s", fileCount, coldDuration, warmDuration, incrementalDuration)
}

func callRepositoryIndexTool(t *testing.T, name string, arguments map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(params)
	if result["is_error"] == true {
		t.Fatalf("%s failed: %v", name, result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return payload
}
func TestSearchWorkspaceFilesReturnsContextLines(t *testing.T) {
	workspaceRoot = t.TempDir()
	source := "package demo\n\nfunc Alpha() {\n\t// needle here\n\treturn\n}\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, "alpha.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	result, err := searchWorkspaceFiles(workspaceRoot, "needle", 5, true, false, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	matches, _ := result["matches"].([]map[string]any)
	if len(matches) == 0 {
		raw, _ := json.Marshal(result)
		t.Fatalf("expected a match, got %s", raw)
	}
	first := matches[0]
	if _, ok := first["context"]; !ok {
		t.Fatalf("expected context lines in match: %#v", first)
	}
	if fmt.Sprint(first["start_line"]) != "2" || fmt.Sprint(first["end_line"]) != "6" {
		t.Fatalf("unexpected context range: %#v", first)
	}
}
