package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingRequestedWebStylingRejectsBorderOnlyHTML(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal"), 0o755); err != nil {
		t.Fatal(err)
	}
	unstyled := "package portal\nconst page = `<!doctype html><html><body><table border=\"1\"><tr><td>报修</td></tr></table></body></html>`\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal.go"), []byte(unstyled), 0o644); err != nil {
		t.Fatal(err)
	}
	request := "实现校园网 Web 门户，页面为服务端渲染的简体中文 HTML。"
	got := missingRequestedWebStyling(root, request)
	if len(got) != 1 || got[0] != "internal/portal/static/app.css" {
		t.Fatalf("unstyled HTML portal must require a stylesheet, got %#v", got)
	}
	if missingRequestedWebStyling(root, "fix the cache expiry bug") != nil {
		t.Fatal("non-web requests must not require styling")
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal", "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := "body { color:#111; background:#f4f6f8; font-family:sans-serif; padding:16px; display:block; max-width:960px; border-radius:4px; margin:0; }\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "static", "app.css"), []byte(orphan), 0o644); err != nil {
		t.Fatal(err)
	}
	got = missingRequestedWebStyling(root, request)
	if len(got) == 0 {
		t.Fatal("a stylesheet that is never linked from HTML must still fail")
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "portal.go") {
		t.Fatalf("unlinked CSS must reopen the HTML host file, got %#v", got)
	}
}

func TestWireStylesheetInFileLinksAndServesStatic(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "portal")
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package portal\nimport \"net/http\"\nfunc NewMux() http.Handler {\n\tmux := http.NewServeMux()\n\tmux.HandleFunc(\"/\", func(http.ResponseWriter, *http.Request) {})\n\treturn mux\n}\nconst page = `<!doctype html><html><head><meta charset=\"utf-8\"></head><body><table border=\"1\"></table></body></html>`\n"
	if err := os.WriteFile(filepath.Join(dir, "portal.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "static", "app.css"), []byte("body{color:#111;background:#fff;font-family:sans-serif;padding:8px;display:block;max-width:40rem;margin:0;border-radius:4px}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !wireStylesheetInFile(root, "internal/portal/portal.go", "internal/portal/static/app.css") {
		t.Fatal("expected stylesheet wiring")
	}
	got, err := os.ReadFile(filepath.Join(dir, "portal.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, `href="/static/app.css"`) || !strings.Contains(text, `"/static/"`) || !strings.Contains(text, `GET /{$}`) {
		t.Fatalf("host file must link and serve CSS: %s", text)
	}
}

func TestMissingRequestedProductLayoutRequiresShell(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal", "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	html := "package portal\nconst page = `<!doctype html><html><head><link rel=\"stylesheet\" href=\"/static/app.css\"></head><body><table class=\"queue\"><tr><td>报修</td></tr></table></body></html>`\n"
	css := `:root { --ink:#151b22; --paper:#e7edf3; --brand:#9f1239; }
body { margin:0; background:var(--paper); color:var(--ink); font-family:"IBM Plex Sans",sans-serif; }
.shell { display:flex; max-width:1120px; }
.queue { width:100%; border-collapse:collapse; }
th,td { padding:8px 10px; }
button { background:#0e7490; color:#fff; border-radius:4px; }
`
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal.go"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "static", "app.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	if missingRequestedWebStyling(root, "校园网门户 HTML 页面") != nil {
		t.Fatal("linked stylesheet is enough for MVP")
	}
	got := missingRequestedProductLayout(root, "完整产品 校园网门户 HTML")
	if len(got) == 0 {
		t.Fatal("product scope must still require header/nav/main")
	}
}

func TestWorkspaceHasRequestedWebStylingAcceptsRealStylesheet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "portal", "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	html := "package portal\nconst page = `<!doctype html><html><head><link rel=\"stylesheet\" href=\"/static/app.css\"></head><body><table class=\"queue\"><tr><td>报修</td></tr></table></body></html>`\n"
	css := `:root { --ink:#151b22; --paper:#e7edf3; --brand:#9f1239; }
body { margin:0; background:var(--paper); color:var(--ink); font-family:"IBM Plex Sans",sans-serif; }
.shell { display:flex; max-width:1120px; }
.queue { width:100%; border-collapse:collapse; }
th,td { padding:8px 10px; }
button { background:#0e7490; color:#fff; border-radius:4px; }
`
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "portal.go"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "portal", "static", "app.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspaceHasRequestedWebStyling(root) {
		t.Fatal("layout stylesheet must satisfy the web styling gate")
	}
	if missingRequestedWebStyling(root, "校园网门户 HTML 页面") != nil {
		t.Fatal("styled portal must pass the styling gate")
	}
}
