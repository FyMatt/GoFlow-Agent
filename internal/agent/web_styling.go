package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

var webStylePropertyPattern = regexp.MustCompile(`(?i)(?:^|[;{\s])(background(?:-color)?|color|font-family|font-size|padding|margin|display|flex|grid|gap|border-radius|max-width|min-height|box-sizing|line-height|letter-spacing|border(?:-color|-width)?)\s*:`)
var htmlMarkupPattern = regexp.MustCompile(`(?i)(?:<!doctype html|<html|template\.HTML|<table|<form method)`)

func workflowRequestRequiresWebStyling(request string) bool {
	text := strings.ToLower(request)
	switch {
	case strings.Contains(text, "html"),
		strings.Contains(request, "页面"),
		strings.Contains(request, "门户"),
		strings.Contains(request, "网站"),
		strings.Contains(text, "ssr"),
		strings.Contains(text, "stylesheet"),
		strings.Contains(text, "css"):
		return true
	default:
		return false
	}
}

func requestedWebStyleWritePaths(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, path := range plannedWritePathsFromText(text) {
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".css") && !strings.HasSuffix(lower, ".html") {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func defaultWebStyleWritePath(root, request string) string {
	for _, path := range plannedWritePathsFromText(request) {
		if !strings.HasSuffix(strings.ToLower(path), ".go") || skipChangeSliceTestPath(path) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		if strings.HasPrefix(dir, "internal/") {
			return dir + "/static/app.css"
		}
	}
	if strings.TrimSpace(root) != "" {
		if _, err := os.Stat(filepath.Join(root, "internal", "portal")); err == nil {
			return "internal/portal/static/app.css"
		}
		if _, err := os.Stat(filepath.Join(root, "internal", "app")); err == nil {
			return "internal/app/static/app.css"
		}
	}
	return "web/app.css"
}

func workspaceHasProductWebLayout(root string) bool {
	if !workspaceHasRequestedWebStyling(root) {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			if err == nil {
				switch strings.ToLower(entry.Name()) {
				case ".git", ".goflow", ".cache", "node_modules", "vendor", "task-staging":
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".html" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		low := strings.ToLower(string(data))
		if strings.Contains(low, "<header") || strings.Contains(low, "<nav") || strings.Contains(low, `class="topbar"`) || strings.Contains(low, `class="site-header"`) || strings.Contains(low, `class="app-shell"`) || strings.Contains(low, "<main") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func missingRequestedProductLayout(root, request string) []string {
	if normalizeDeliveryScope(request) != "product" && !strings.Contains(request, "完整产品") {
		return nil
	}
	if !workflowRequestRequiresWebStyling(request) {
		return nil
	}
	if workspaceHasProductWebLayout(root) {
		return nil
	}
	hosts := unlinkedHTMLHostPaths(root)
	if len(hosts) > 0 {
		return hosts
	}
	cssPath := defaultWebStyleWritePath(root, request)
	dir := filepath.ToSlash(filepath.Dir(cssPath))
	if strings.HasSuffix(dir, "/static") {
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
	if dir == "." || dir == "" {
		return []string{"internal/portal/portal.go"}
	}
	return []string{dir + "/portal.go"}
}

func missingRequestedWebStyling(root, request string) []string {
	if !workflowRequestRequiresWebStyling(request) {
		return nil
	}
	if workspaceHasRequestedWebStyling(root) {
		return nil
	}
	cssPath := defaultWebStyleWritePath(root, request)
	if paths := requestedWebStyleWritePaths(request); len(paths) > 0 {
		cssPath = paths[0]
		for _, path := range paths {
			if strings.HasSuffix(strings.ToLower(path), ".css") {
				cssPath = path
				break
			}
		}
	}
	out := []string{cssPath}
	cssAbs := filepath.Join(root, filepath.FromSlash(cssPath))
	if _, err := os.Stat(cssAbs); err == nil {
		out = append(out, unlinkedHTMLHostPaths(root)...)
	}
	return uniqueChangeSlicePaths(out)
}

func unlinkedHTMLHostPaths(root string) []string {
	out := make([]string, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			if err == nil {
				switch strings.ToLower(entry.Name()) {
				case ".git", ".goflow", ".cache", "node_modules", "vendor", "task-staging":
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		lower := strings.ToLower(rel)
		if strings.HasSuffix(lower, "_test.go") || strings.Contains(lower, "p7_hidden") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".go" && ext != ".html" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		content := string(data)
		if !htmlMarkupPattern.MatchString(content) {
			return nil
		}
		low := strings.ToLower(content)
		if strings.Contains(low, "app.css") || strings.Contains(low, "<style") || strings.Contains(low, "rel=\"stylesheet\"") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out
}

func workspaceHasRequestedWebStyling(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	props := make(map[string]struct{})
	hasSheet := false
	hasMarkup := false
	linked := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".goflow", ".cache", "node_modules", "vendor", "task-staging":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		lower := strings.ToLower(rel)
		if strings.Contains(lower, "p7_hidden") || strings.HasSuffix(lower, "_test.go") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		switch ext {
		case ".css", ".html", ".go", ".js":
		default:
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || len(data) == 0 {
			return nil
		}
		if len(data) > 120000 {
			data = data[:120000]
		}
		content := string(data)
		if htmlMarkupPattern.MatchString(content) {
			hasMarkup = true
		}
		lowContent := strings.ToLower(content)
		if strings.Contains(lowContent, "app.css") || strings.Contains(lowContent, "rel=\"stylesheet\"") || strings.Contains(lowContent, "rel='stylesheet'") || strings.Contains(lowContent, "<style") {
			linked = true
		}
		cssBlob := content
		if ext != ".css" {
			cssBlob = extractEmbeddedStylesheets(content)
		} else {
			hasSheet = true
		}
		if cssBlob != "" && ext != ".css" {
			hasSheet = true
		}
		for _, match := range webStylePropertyPattern.FindAllStringSubmatch(cssBlob, 40) {
			props[strings.ToLower(match[1])] = struct{}{}
		}
		return nil
	})
	if !hasMarkup {
		return true
	}
	return hasSheet && linked && len(props) >= 8
}

func (w *WorkflowRunner) seedWebStylingWiring(result *schema.AgentResult) {
	if w == nil || result == nil || !workflowRequestRequiresWebStyling(w.workflowRequestText()) {
		return
	}
	root := w.discoveredWorkspaceRoot()
	if strings.TrimSpace(root) == "" || workspaceHasRequestedWebStyling(root) {
		return
	}
	cssRel := defaultWebStyleWritePath(root, w.workflowRequestText())
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(cssRel))); err != nil {
		return
	}
	wired := false
	for _, host := range unlinkedHTMLHostPaths(root) {
		if wireStylesheetInFile(root, host, cssRel) {
			wired = true
		}
	}
	if wired {
		result.Output = strings.TrimSpace(result.Output) + "\nseeded stylesheet wiring for " + cssRel + "\n"
	}
}

func wireStylesheetInFile(root, rel, cssRel string) bool {
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	original := string(data)
	updated := original
	if !strings.Contains(strings.ToLower(updated), "app.css") && strings.Contains(updated, "<head>") {
		updated = strings.Replace(updated, "<head>", `<head><link rel="stylesheet" href="/static/app.css">`, 1)
	}
	staticDir := filepath.ToSlash(filepath.Dir(cssRel))
	if strings.Contains(updated, "NewServeMux()") && !strings.Contains(updated, `"/static/"`) {
		if idx := strings.Index(updated, "NewServeMux()"); idx >= 0 {
			if nl := strings.Index(updated[idx:], "\n"); nl >= 0 {
				at := idx + nl + 1
				updated = updated[:at] + "\tmux.Handle(\"/static/\", http.StripPrefix(\"/static/\", http.FileServer(http.Dir(\"" + staticDir + "\"))))\n" + updated[at:]
			}
		}
	}
	if strings.Contains(updated, `HandleFunc("/",`) && !strings.Contains(updated, `HandleFunc("GET /{$}"`) {
		updated = strings.Replace(updated, `HandleFunc("/",`, `HandleFunc("GET /{$}",`, 1)
	}
	if updated == original {
		return false
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false
	}
	return true
}

func extractEmbeddedStylesheets(content string) string {
	lower := strings.ToLower(content)
	var out strings.Builder
	for {
		start := strings.Index(lower, "<style")
		if start < 0 {
			break
		}
		open := strings.Index(lower[start:], ">")
		if open < 0 {
			break
		}
		body := start + open + 1
		end := strings.Index(lower[body:], "</style>")
		if end < 0 {
			out.WriteString(content[body:])
			break
		}
		out.WriteString(content[body : body+end])
		lower = lower[body+end+8:]
		content = content[body+end+8:]
	}
	return out.String()
}
