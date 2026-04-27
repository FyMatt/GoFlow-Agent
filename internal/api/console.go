package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/console.html web/assets/*.css web/assets/*.js web/assets/views/*.js
var consoleFS embed.FS

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	if path != "" && path != "console" && path != "workflows" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFileFS(w, r, consoleFS, "web/console.html")
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConsoleAssets() http.Handler {
	assets, err := fs.Sub(consoleFS, "web/assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/assets/", http.FileServerFS(assets))
}
