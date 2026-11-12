package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const repositoryIndexVersion = 1
const maxIndexedContentFiles = 10000

type repositoryIndexEntry struct {
	RelativePath string `json:"relative_path"`
	IsDir        bool   `json:"is_dir,omitempty"`
	Size         int64  `json:"size,omitempty"`
	ModifiedNS   int64  `json:"modified_ns,omitempty"`
}

type repositoryIndex struct {
	Version     int                    `json:"version"`
	Root        string                 `json:"root"`
	GeneratedAt string                 `json:"generated_at"`
	Entries     []repositoryIndexEntry `json:"entries"`
}

type repositoryIndexStatus struct {
	Rebuilt      bool
	UpdatedPaths int
	DurationMS   int64
}

var repositoryIndexMu sync.Mutex
var cachedRepositoryIndex *repositoryIndex

func repositoryIndexPath() string {
	return filepath.Join(workspaceRoot, ".goflow", "index", "repository.json")
}

func repositoryIndexDirtyPath() string {
	return filepath.Join(workspaceRoot, ".goflow", "index", "dirty")
}

func loadRepositoryIndex(force bool) (repositoryIndex, repositoryIndexStatus, error) {
	started := time.Now()
	repositoryIndexMu.Lock()
	defer repositoryIndexMu.Unlock()

	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return repositoryIndex{}, repositoryIndexStatus{}, err
	}
	status := repositoryIndexStatus{}
	if force || cachedRepositoryIndex == nil || !strings.EqualFold(cachedRepositoryIndex.Root, root) {
		cachedRepositoryIndex = nil
		if !force {
			loaded, loadErr := readRepositoryIndex(root)
			if loadErr == nil {
				cachedRepositoryIndex = &loaded
			}
		}
	}
	if cachedRepositoryIndex == nil {
		built, buildErr := buildRepositoryIndex(root)
		if buildErr != nil {
			return repositoryIndex{}, status, buildErr
		}
		cachedRepositoryIndex = &built
		status.Rebuilt = true
	}

	dirty, dirtyErr := os.ReadFile(repositoryIndexDirtyPath())
	if dirtyErr == nil && len(strings.TrimSpace(string(dirty))) > 0 {
		paths, full := normalizeDirtyIndexPaths(string(dirty))
		if full {
			built, buildErr := buildRepositoryIndex(root)
			if buildErr != nil {
				return repositoryIndex{}, status, buildErr
			}
			cachedRepositoryIndex = &built
			status.Rebuilt = true
		} else {
			status.UpdatedPaths, err = refreshRepositoryIndexPaths(cachedRepositoryIndex, root, paths)
			if err != nil {
				return repositoryIndex{}, status, err
			}
			if status.UpdatedPaths > 0 {
				cachedRepositoryIndex.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if err := persistRepositoryIndex(*cachedRepositoryIndex); err != nil {
					return repositoryIndex{}, status, err
				}
			}
		}
		if err := os.Remove(repositoryIndexDirtyPath()); err != nil && !os.IsNotExist(err) {
			return repositoryIndex{}, status, fmt.Errorf("clear repository index dirty marker: %w", err)
		}
	} else if dirtyErr != nil && !os.IsNotExist(dirtyErr) {
		return repositoryIndex{}, status, fmt.Errorf("read repository index dirty marker: %w", dirtyErr)
	}

	copyIndex := *cachedRepositoryIndex
	copyIndex.Entries = append([]repositoryIndexEntry(nil), cachedRepositoryIndex.Entries...)
	status.DurationMS = time.Since(started).Milliseconds()
	return copyIndex, status, nil
}

func readRepositoryIndex(root string) (repositoryIndex, error) {
	data, err := os.ReadFile(repositoryIndexPath())
	if err != nil {
		return repositoryIndex{}, err
	}
	var index repositoryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return repositoryIndex{}, err
	}
	if index.Version != repositoryIndexVersion || !strings.EqualFold(index.Root, root) {
		return repositoryIndex{}, fmt.Errorf("repository index identity or version mismatch")
	}
	return index, nil
}

func buildRepositoryIndex(root string) (repositoryIndex, error) {
	index := repositoryIndex{
		Version:     repositoryIndexVersion,
		Root:        root,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		parent := filepath.Dir(path)
		if shouldHideIndexEntry(parent, entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		index.Entries = append(index.Entries, repositoryIndexEntry{
			RelativePath: filepath.ToSlash(relativeWorkspacePath(path)),
			IsDir:        entry.IsDir(),
			Size:         info.Size(),
			ModifiedNS:   info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return repositoryIndex{}, err
	}
	sortRepositoryIndexEntries(index.Entries)
	if err := persistRepositoryIndex(index); err != nil {
		return repositoryIndex{}, err
	}
	return index, nil
}

func shouldHideIndexEntry(parent, name string) bool {
	if shouldHideRootEntry(parent, name) {
		return true
	}
	if name != ".git" {
		return false
	}
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return false
	}
	cleanParent, err := filepath.Abs(filepath.Clean(parent))
	return err == nil && strings.EqualFold(cleanParent, root)
}

func persistRepositoryIndex(index repositoryIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("encode repository index: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryIndexPath()), 0o700); err != nil {
		return fmt.Errorf("create repository index directory: %w", err)
	}
	if err := atomicWriteFile(repositoryIndexPath(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("persist repository index: %w", err)
	}
	return nil
}

func normalizeDirtyIndexPaths(raw string) ([]string, bool) {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = filepath.ToSlash(filepath.Clean(strings.TrimSpace(line)))
		if line == "" || line == "." {
			continue
		}
		if line == "*" {
			return nil, true
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		paths = append(paths, line)
	}
	sort.Strings(paths)
	return paths, false
}

func refreshRepositoryIndexPaths(index *repositoryIndex, root string, paths []string) (int, error) {
	if index == nil {
		return 0, fmt.Errorf("repository index is required")
	}
	entries := make(map[string]repositoryIndexEntry, len(index.Entries))
	for _, entry := range index.Entries {
		entries[entry.RelativePath] = entry
	}
	updated := 0
	for _, relative := range paths {
		absolute, err := resolveWorkspacePath(relative, root)
		if err != nil {
			return updated, err
		}
		info, statErr := os.Lstat(absolute)
		if os.IsNotExist(statErr) {
			if _, exists := entries[relative]; exists {
				delete(entries, relative)
				updated++
			}
			continue
		}
		if statErr != nil {
			return updated, statErr
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return updated, fmt.Errorf("repository index directory or symbolic-link changes require a full rebuild")
		}
		entry := repositoryIndexEntry{RelativePath: relative, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
		if old, exists := entries[relative]; !exists || old != entry {
			entries[relative] = entry
			updated++
		}
	}
	index.Entries = index.Entries[:0]
	for _, entry := range entries {
		index.Entries = append(index.Entries, entry)
	}
	sortRepositoryIndexEntries(index.Entries)
	return updated, nil
}

func sortRepositoryIndexEntries(entries []repositoryIndexEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelativePath < entries[j].RelativePath })
}

func searchWorkspaceFiles(path, query string, maxResults int, includeContent, caseSensitive bool, contextLines int) (map[string]any, error) {
	index, status, err := loadRepositoryIndex(false)
	if err != nil {
		fallback, fallbackErr := boundedScanWorkspaceFiles(path, query, maxResults, includeContent, caseSensitive, contextLines)
		if fallbackErr != nil {
			return nil, err
		}
		fallback["search_strategy"] = "bounded-full-scan"
		fallback["index_error"] = err.Error()
		return fallback, nil
	}
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	scopeRelative := filepath.ToSlash(relativeWorkspacePath(path))
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	matches := make([]map[string]any, 0, maxResults)
	for _, entry := range index.Entries {
		if !repositoryEntryWithinScope(entry.RelativePath, scopeRelative) {
			continue
		}
		haystack := entry.RelativePath
		if !caseSensitive {
			haystack = strings.ToLower(haystack)
		}
		if strings.Contains(haystack, needle) {
			absolute := filepath.Join(root, filepath.FromSlash(entry.RelativePath))
			matches = append(matches, map[string]any{"path": absolute, "relative_path": entry.RelativePath, "is_dir": entry.IsDir, "match_type": "name", "line_number": 0, "snippet": filepath.Base(entry.RelativePath)})
			if len(matches) >= maxResults {
				break
			}
		}
	}

	contentScanned := 0
	contentLimited := false
	if includeContent && len(matches) < maxResults {
		for _, entry := range index.Entries {
			if entry.IsDir || entry.Size > maxReadBytes || !repositoryEntryWithinScope(entry.RelativePath, scopeRelative) {
				continue
			}
			if contentScanned >= maxIndexedContentFiles {
				contentLimited = true
				break
			}
			contentScanned++
			absolute := filepath.Join(root, filepath.FromSlash(entry.RelativePath))
			fileMatches, valid := searchIndexedTextFile(absolute, entry.RelativePath, query, caseSensitive, maxResults-len(matches), contextLines)
			if valid {
				matches = append(matches, fileMatches...)
			}
			if len(matches) >= maxResults {
				contentLimited = true
				break
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := fmt.Sprint(matches[i]["relative_path"])
		right := fmt.Sprint(matches[j]["relative_path"])
		if left != right {
			return left < right
		}
		return fmt.Sprint(matches[i]["match_type"]) < fmt.Sprint(matches[j]["match_type"])
	})
	return map[string]any{
		"path": path, "relative_path": scopeRelative, "query": query,
		"case_sensitive": caseSensitive, "include_content": includeContent,
		"max_results": maxResults, "truncated": contentLimited,
		"matches": matches, "search_strategy": "repository-index",
		"index_entries": len(index.Entries), "index_rebuilt": status.Rebuilt,
		"index_updated_paths": status.UpdatedPaths, "index_duration_ms": status.DurationMS,
		"duration_ms":           status.DurationMS,
		"content_files_scanned": contentScanned, "content_scan_limit": maxIndexedContentFiles,
	}, nil
}

func repositoryEntryWithinScope(relative, scope string) bool {
	scope = strings.Trim(filepath.ToSlash(scope), "/")
	if scope == "" || scope == "." {
		return true
	}
	return relative == scope || strings.HasPrefix(relative, scope+"/")
}

func searchIndexedTextFile(path, relative, query string, caseSensitive bool, limit, contextLines int) ([]map[string]any, bool) {
	if limit <= 0 {
		return nil, true
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	matches := make([]map[string]any, 0)
	lineNumbers := make([]int, 0, limit)
	lineNumber := 0
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if !utf8.ValidString(line) {
				return nil, false
			}
			lineNumber++
			if lineNumber == 1 {
				line = strings.TrimPrefix(line, "\ufeff")
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			haystack := line
			if !caseSensitive {
				haystack = strings.ToLower(haystack)
			}
			if strings.Contains(haystack, needle) {
				matches = append(matches, map[string]any{"path": path, "relative_path": relative, "is_dir": false, "match_type": "content", "line_number": lineNumber, "snippet": truncateDiffLine(strings.TrimSpace(line))})
				if contextLines > 0 {
					lineNumbers = append(lineNumbers, lineNumber)
				}
				if len(matches) >= limit {
					break
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	if contextLines > 0 && len(lineNumbers) > 0 {
		if data, err := os.ReadFile(path); err == nil {
			if text, decodeErr := decodeUTF8Text(data); decodeErr == nil {
				lines := strings.Split(text, "\n")
				for index, match := range matches {
					if index >= len(lineNumbers) {
						break
					}
					line := lineNumbers[index]
					start := line - contextLines
					if start < 1 {
						start = 1
					}
					end := line + contextLines
					if end > len(lines) {
						end = len(lines)
					}
					context := make([]string, 0, end-start+1)
					for cursor := start; cursor <= end; cursor++ {
						context = append(context, fmt.Sprintf("%d: %s", cursor, strings.TrimRight(lines[cursor-1], "\r")))
					}
					match["start_line"] = start
					match["end_line"] = end
					match["context"] = strings.Join(context, "\n")
				}
			}
		}
	}
	return matches, true
}

func repositoryMap(path string, force bool) (map[string]any, error) {
	index, status, err := loadRepositoryIndex(force)
	if err != nil {
		return nil, err
	}
	scope := filepath.ToSlash(relativeWorkspacePath(path))
	extensions := map[string]int{}
	directories := map[string]int{}
	markers := make([]string, 0)
	fileCount := 0
	directoryCount := 0
	for _, entry := range index.Entries {
		if !repositoryEntryWithinScope(entry.RelativePath, scope) {
			continue
		}
		if entry.IsDir {
			directoryCount++
			continue
		}
		fileCount++
		ext := strings.ToLower(filepath.Ext(entry.RelativePath))
		if ext == "" {
			ext = "(无扩展名)"
		}
		extensions[ext]++
		top := strings.Split(entry.RelativePath, "/")[0]
		directories[top]++
		if isRepositoryMarker(filepath.Base(entry.RelativePath)) {
			markers = append(markers, entry.RelativePath)
		}
	}
	return map[string]any{
		"relative_path": scope, "generated_at": index.GeneratedAt,
		"file_count": fileCount, "directory_count": directoryCount,
		"top_directories": rankedRepositoryCounts(directories, 20),
		"extensions":      rankedRepositoryCounts(extensions, 30),
		"project_markers": uniqueSortedStrings(markers),
		"index_rebuilt":   status.Rebuilt, "index_updated_paths": status.UpdatedPaths,
		"index_duration_ms": status.DurationMS, "duration_ms": status.DurationMS,
	}, nil
}

func isRepositoryMarker(name string) bool {
	switch strings.ToLower(name) {
	case "go.mod", "package.json", "pnpm-workspace.yaml", "cargo.toml", "pyproject.toml", "requirements.txt", "pom.xml", "build.gradle", "build.gradle.kts", "cmakelists.txt", "makefile", "dockerfile":
		return true
	default:
		return strings.HasSuffix(strings.ToLower(name), ".sln") || strings.HasSuffix(strings.ToLower(name), ".csproj")
	}
}

func rankedRepositoryCounts(values map[string]int, limit int) []map[string]any {
	type pair struct {
		name  string
		count int
	}
	items := make([]pair, 0, len(values))
	for name, count := range values {
		items = append(items, pair{name: name, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].name < items[j].name
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"name": item.name, "count": item.count})
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
