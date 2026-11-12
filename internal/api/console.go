package api

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed web/console.html web/assets/*.css web/assets/*.js web/assets/*.svg web/assets/views/*.js
var consoleFS embed.FS

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	route := strings.Trim(r.URL.Path, "/")
	if route != "" && route != "console" && route != "workflows" {
		http.NotFound(w, r)
		return
	}
	asset, ok := consoleAssetByName("web/console.html")
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The console bundles embedded assets, so stale CSS or JavaScript can leave a
	// newly deployed workflow layout only partially updated in an open browser.
	// no-cache still revalidates, and the ETag turns that into a cheap 304.
	serveConsoleAsset(w, r, asset, "no-cache")
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	asset, ok := consoleAssetByName("web/assets/brand-mark.svg")
	if !ok {
		http.NotFound(w, r)
		return
	}
	serveConsoleAsset(w, r, asset, "public, max-age=86400")
}

func (s *Server) handleConsoleAssets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name, ok := consoleAssetPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		asset, ok := consoleAssetByName(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveConsoleAsset(w, r, asset, "no-cache")
	})
}

// consoleAssetPath maps a request path to an embedded asset name. Serving from
// the embedded FS by hand means path traversal has to be rejected here rather
// than by http.FileServerFS.
func consoleAssetPath(requestPath string) (string, bool) {
	relative := strings.TrimPrefix(requestPath, "/assets/")
	if relative == requestPath || relative == "" {
		return "", false
	}
	// A leading slash would make path.Clean keep "..", so normalise first.
	cleaned := path.Clean("/" + relative)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return "web/assets/" + cleaned, true
}
