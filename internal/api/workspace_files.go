package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type workspaceFilesResponse struct {
	Root    string               `json:"root"`
	Prefix  string               `json:"prefix"`
	Entries []workspaceFileEntry `json:"entries"`
}

type workspaceFileEntry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
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
	entries, err := suggestWorkspaceReferenceEntries(prefix, s.workspace.Root(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, workspaceFilesResponse{Root: s.workspace.Root(), Prefix: prefix, Entries: entries})
}

func suggestWorkspaceReferenceEntries(prefix, workspaceRoot string, limit int) ([]workspaceFileEntry, error) {
	root, err := canonicalWorkspaceReferenceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	normalizedPrefix := filepath.ToSlash(strings.TrimPrefix(prefix, "./"))
	candidates := make([]workspaceFileEntry, 0, limit)
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && entry.Name() == ".goflow" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > 3000 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
		if normalizedPrefix != "" && !strings.HasPrefix(strings.ToLower(rel), strings.ToLower(normalizedPrefix)) {
			return nil
		}
		item := workspaceFileEntry{Path: rel, Name: entry.Name(), IsDir: entry.IsDir()}
		if info, err := entry.Info(); err == nil && !entry.IsDir() {
			item.Size = info.Size()
		}
		candidates = append(candidates, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].IsDir != candidates[j].IsDir {
			return candidates[i].IsDir
		}
		return strings.ToLower(candidates[i].Path) < strings.ToLower(candidates[j].Path)
	})
	if len(candidates) > limit {
		return candidates[:limit], nil
	}
	return candidates, nil
}
