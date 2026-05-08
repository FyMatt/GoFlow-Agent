package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const workspaceReferenceTraversalLimit = 3000

var errWorkspaceReferenceTraversalLimit = errors.New("workspace reference traversal limit reached")

type workspaceFilesResponse struct {
	Root               string               `json:"root"`
	Prefix             string               `json:"prefix"`
	Query              string               `json:"query,omitempty"`
	SearchMode         string               `json:"search_mode"`
	Limit              int                  `json:"limit,omitempty"`
	Truncated          bool                 `json:"truncated,omitempty"`
	TraversalLimit     int                  `json:"traversal_limit"`
	Visited            int                  `json:"visited"`
	TraversalTruncated bool                 `json:"traversal_truncated,omitempty"`
	IgnoredDirs        []string             `json:"ignored_dirs,omitempty"`
	Entries            []workspaceFileEntry `json:"entries"`
}

type workspaceFileEntry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

type workspaceReferenceSuggestions struct {
	Entries            []workspaceFileEntry
	ResultTruncated    bool
	SearchMode         string
	TraversalLimit     int
	Visited            int
	TraversalTruncated bool
	IgnoredDirs        []string
}

func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.workspace == nil || strings.TrimSpace(s.workspace.Root()) == "" {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":   "workspace_required",
			"message": "workspace is required before listing files",
		})
		return
	}
	if !s.workspace.Confirmed() {
		writeWorkspaceRequired(w, "file picker reads workspace paths", s.workspace.Snapshot())
		return
	}
	limit := 80
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	query := firstWorkspaceFilesQueryValue(r.URL.Query().Get("q"), r.URL.Query().Get("query"))
	suggestions, err := suggestWorkspaceReferenceEntries(prefix, query, s.workspace.Root(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, workspaceFilesResponse{
		Root:               s.workspace.Root(),
		Prefix:             prefix,
		Query:              query,
		SearchMode:         suggestions.SearchMode,
		Limit:              limit,
		Truncated:          suggestions.ResultTruncated,
		TraversalLimit:     suggestions.TraversalLimit,
		Visited:            suggestions.Visited,
		TraversalTruncated: suggestions.TraversalTruncated,
		IgnoredDirs:        suggestions.IgnoredDirs,
		Entries:            suggestions.Entries,
	})
}

func suggestWorkspaceReferenceEntries(prefix, query, workspaceRoot string, limit int) (workspaceReferenceSuggestions, error) {
	root, err := canonicalWorkspaceReferenceRoot(workspaceRoot)
	if err != nil {
		return workspaceReferenceSuggestions{}, err
	}
	normalizedPrefix := filepath.ToSlash(strings.TrimPrefix(prefix, "./"))
	normalizedQuery := strings.TrimSpace(query)
	searchMode := workspaceReferenceSearchMode(normalizedPrefix, normalizedQuery)
	if normalizedQuery == "" && !strings.ContainsAny(normalizedPrefix, `/\`) {
		normalizedQuery = normalizedPrefix
	}
	candidates := make([]workspaceFileEntry, 0, limit+1)
	visited := 0
	traversalTruncated := false
	ignoredDirSet := map[string]struct{}{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && shouldSkipWorkspaceReferenceDir(entry) {
			if entry.IsDir() {
				if rel, err := filepath.Rel(root, path); err == nil {
					ignoredDirSet[filepath.ToSlash(rel)+"/"] = struct{}{}
				}
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > workspaceReferenceTraversalLimit {
			traversalTruncated = true
			return errWorkspaceReferenceTraversalLimit
		}
		if path == root {
			return nil
		}
		if !workspacePathWithinRoot(root, path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			rel += "/"
		}
		if !workspaceReferenceEntryMatches(rel, entry.Name(), normalizedPrefix, normalizedQuery) {
			return nil
		}
		item := workspaceFileEntry{Path: rel, Name: entry.Name(), IsDir: entry.IsDir()}
		if info, err := entry.Info(); err == nil && !entry.IsDir() {
			item.Size = info.Size()
		}
		candidates = append(candidates, item)
		return nil
	})
	if err != nil && !errors.Is(err, errWorkspaceReferenceTraversalLimit) {
		return workspaceReferenceSuggestions{}, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].IsDir != candidates[j].IsDir {
			return candidates[i].IsDir
		}
		iRank := workspaceReferenceMatchRank(candidates[i], normalizedPrefix, normalizedQuery)
		jRank := workspaceReferenceMatchRank(candidates[j], normalizedPrefix, normalizedQuery)
		if iRank != jRank {
			return iRank < jRank
		}
		return strings.ToLower(candidates[i].Path) < strings.ToLower(candidates[j].Path)
	})
	ignoredDirs := make([]string, 0, len(ignoredDirSet))
	for ignoredDir := range ignoredDirSet {
		ignoredDirs = append(ignoredDirs, ignoredDir)
	}
	sort.Strings(ignoredDirs)
	suggestions := workspaceReferenceSuggestions{
		Entries:            candidates,
		SearchMode:         searchMode,
		TraversalLimit:     workspaceReferenceTraversalLimit,
		Visited:            min(visited, workspaceReferenceTraversalLimit),
		TraversalTruncated: traversalTruncated,
		IgnoredDirs:        ignoredDirs,
	}
	if len(candidates) > limit {
		suggestions.Entries = candidates[:limit]
		suggestions.ResultTruncated = true
	}
	return suggestions, nil
}

func firstWorkspaceFilesQueryValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func workspaceReferenceSearchMode(prefix, query string) string {
	prefix = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(prefix), "./"))
	query = strings.TrimSpace(query)
	switch {
	case query != "":
		return "contains"
	case prefix == "":
		return "all"
	case strings.ContainsAny(prefix, `/\`):
		return "prefix"
	default:
		return "prefix_or_contains"
	}
}

func shouldSkipWorkspaceReferenceDir(entry os.DirEntry) bool {
	if entry == nil || !entry.IsDir() {
		return false
	}
	switch strings.ToLower(entry.Name()) {
	case ".git", ".hg", ".svn", ".goflow",
		".venv", "venv", "env",
		"node_modules", "__pycache__", "vendor",
		"dist", "build", "target", "coverage",
		".next", ".nuxt", ".cache", ".pytest_cache", ".mypy_cache":
		return true
	default:
		return false
	}
}

func workspaceReferenceEntryMatches(path, name, prefix, query string) bool {
	pathLower := strings.ToLower(filepath.ToSlash(path))
	nameLower := strings.ToLower(name)
	prefixLower := strings.ToLower(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(prefix), "./")))
	queryLower := strings.ToLower(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(query), "./")))
	if prefixLower == "" && queryLower == "" {
		return true
	}
	if prefixLower != "" && strings.HasPrefix(pathLower, prefixLower) {
		return true
	}
	if queryLower != "" {
		return strings.Contains(nameLower, queryLower) || strings.Contains(pathLower, queryLower)
	}
	return false
}

func workspaceReferenceMatchRank(entry workspaceFileEntry, prefix, query string) int {
	pathLower := strings.ToLower(filepath.ToSlash(entry.Path))
	nameLower := strings.ToLower(entry.Name)
	prefixLower := strings.ToLower(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(prefix), "./")))
	queryLower := strings.ToLower(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(query), "./")))
	switch {
	case prefixLower != "" && strings.HasPrefix(pathLower, prefixLower):
		return 0
	case queryLower != "" && strings.EqualFold(nameLower, queryLower):
		return 1
	case queryLower != "" && strings.HasPrefix(nameLower, queryLower):
		return 2
	case queryLower != "" && strings.Contains(nameLower, queryLower):
		return 3
	case queryLower != "" && strings.Contains(pathLower, queryLower):
		return 4
	default:
		return 5
	}
}
