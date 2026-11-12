package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

type dataBackupSummary struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
	Download  string `json:"download"`
}

func (s *Server) handleDataBackupCollection(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.runtime == nil {
		http.Error(w, "runtime is not configured", http.StatusServiceUnavailable)
		return
	}
	store := persistence.New(s.runtime.DataRoot())
	switch r.Method {
	case http.MethodGet:
		items, err := listDataBackups(store.Root())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, items)
	case http.MethodPost:
		path, err := store.CreateBackup()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		item, err := dataBackupFromPath(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONStatus(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDataBackupItem(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.runtime == nil {
		http.Error(w, "runtime is not configured", http.StatusServiceUnavailable)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/data/backups/"), "/")
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(strings.ToLower(name), ".zip") {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.runtime.DataRoot(), "backups", name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		http.ServeFile(w, r, path)
	case http.MethodDelete:
		if err := os.Remove(path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func listDataBackups(root string) ([]dataBackupSummary, error) {
	dir := filepath.Join(root, "backups")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []dataBackupSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]dataBackupSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			continue
		}
		item, err := dataBackupFromPath(filepath.Join(dir, entry.Name()))
		if err == nil {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	return items, nil
}

func dataBackupFromPath(path string) (dataBackupSummary, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return dataBackupSummary{}, err
	}
	if !info.Mode().IsRegular() {
		return dataBackupSummary{}, fmt.Errorf("backup is not a regular file")
	}
	name := filepath.Base(path)
	return dataBackupSummary{Name: name, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339Nano), Download: "/api/data/backups/" + name}, nil
}
