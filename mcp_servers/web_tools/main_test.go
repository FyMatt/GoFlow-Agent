package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallToolFetchURLReturnsStructuredPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Example Page</title></head><body><h1>Hello</h1><p>World</p></body></html>"))
	}))
	defer server.Close()

	params, _ := json.Marshal(map[string]any{
		"name":      "fetch_url",
		"arguments": map[string]any{"url": server.URL, "max_bytes": 4096},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected fetch success, got %v", result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["title"] != "Example Page" || !strings.Contains(payload["text_preview"].(string), "Hello World") {
		t.Fatalf("unexpected fetch payload: %#v", payload)
	}
}

func TestCallToolFetchURLRejectsNonHTTP(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name":      "fetch_url",
		"arguments": map[string]any{"url": "file:///etc/passwd"},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "only http and https") {
		t.Fatalf("expected non-http rejection, got %#v", result)
	}
}

func TestCallToolBrowserSnapshotRequiresAuthorization(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "browser_snapshot",
		"arguments": map[string]any{
			"url":              "https://example.com",
			"allowed_hosts":    []string{"example.com"},
			"authorized_scope": false,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "authorized_scope") {
		t.Fatalf("expected browser snapshot authorization rejection, got %#v", result)
	}
}

func TestCallToolBrowserProbePointsRequiresAuthorization(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "browser_probe_points",
		"arguments": map[string]any{
			"url":                   "https://example.com/?q=test",
			"allowed_hosts":         []string{"example.com"},
			"authorized_scope":      false,
			"active_probe_approved": false,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "authorized_scope") {
		t.Fatalf("expected browser probe authorization rejection, got %#v", result)
	}
}

func TestAnalyzeBrowserDOMReturnsCompactSecuritySurface(t *testing.T) {
	parsed, _ := validateHTTPURL("https://example.com/search?q=test")
	payload := analyzeBrowserDOM(parsed, `
		<html>
			<head><title>Runtime App</title><script src="/app.js"></script><script>document.write(location.hash);</script></head>
			<body>
				<form method="post" action="/submit"><input name="user"><input type="password" name="pass"></form>
				<a href="/profile">Profile</a><a href="https://cdn.example.net/lib.js">CDN</a>
			</body>
		</html>
	`, 120)
	if payload["title"] != "Runtime App" || payload["dom_sha256"] == "" || payload["dom_preview"] == "" {
		t.Fatalf("expected compact DOM metadata, got %#v", payload)
	}
	if payload["dom_truncated"] != true {
		t.Fatalf("expected truncated DOM preview, got %#v", payload)
	}
	forms := payload["forms"].([]map[string]any)
	if len(forms) != 1 || forms[0]["method"] != "POST" {
		t.Fatalf("expected form summary, got %#v", forms)
	}
	inputs := forms[0]["inputs"].([]map[string]string)
	if len(inputs) != 2 || inputs[0]["name"] != "user" || inputs[1]["type"] != "password" {
		t.Fatalf("expected compact input summaries, got %#v", inputs)
	}
	signals := payload["security_signals"].([]map[string]string)
	if len(signals) == 0 {
		t.Fatalf("expected security signals, got %#v", payload)
	}
	queryParams := payload["query_parameters"].([]string)
	if len(queryParams) != 1 || queryParams[0] != "q" {
		t.Fatalf("expected query parameter summary, got %#v", queryParams)
	}
}

func TestBuildBrowserProbeCandidatesSummarizesBoundedGETSurfaces(t *testing.T) {
	parsed, _ := validateHTTPURL("https://example.com/search?q=test")
	candidates := buildBrowserProbeCandidates(parsed, `
		<html>
			<body>
				<form method="get" action="/lookup"><input name="term"><input name="page"></form>
				<form method="post" action="/login"><input name="user"></form>
				<a href="/item?id=42">Item</a>
				<a href="https://cdn.example.net/lib.js?debug=1">External</a>
			</body>
		</html>
	`, 4)
	if len(candidates) != 4 {
		t.Fatalf("expected four bounded candidates, got %#v", candidates)
	}
	if candidates[0].Source != "url_query" || candidates[0].Param != "q" {
		t.Fatalf("expected URL query candidate first, got %#v", candidates[0])
	}
	var sawForm, sawLink bool
	for _, candidate := range candidates {
		if candidate.Source == "get_form" && candidate.Param == "term" {
			sawForm = true
		}
		if candidate.Source == "same_origin_link" && candidate.Param == "id" {
			sawLink = true
		}
		if candidate.Method != "GET" {
			t.Fatalf("expected only GET candidates, got %#v", candidates)
		}
	}
	if !sawForm || !sawLink {
		t.Fatalf("expected form and same-origin link candidates, got %#v", candidates)
	}
}

func TestBrowserProbeHelpersKeepEvidenceCompact(t *testing.T) {
	rawURL := "https://example.com/search?q=original&debug=1"
	canary := canaryValue("reflection", "q")
	probeURL := probeURLWithValue(rawURL, "q", canary)
	if !strings.Contains(probeURL, canary) || strings.Contains(compactProbeURL(probeURL), canary) {
		t.Fatalf("expected probe URL to carry canary and compact URL to mask it, probe=%s compact=%s", probeURL, compactProbeURL(probeURL))
	}
	if preview := evidencePreview("<html><body>before "+canary+" after</body></html>", canary, 80); !strings.Contains(preview, canary) {
		t.Fatalf("expected evidence preview to include canary, got %q", preview)
	}
	signals := sqlErrorSignals("SQL syntax error near quote in MySQL")
	if len(signals) == 0 {
		t.Fatalf("expected SQL error signals")
	}
}

func TestBuiltinToolsIncludesBrowserAutomationSecurityTools(t *testing.T) {
	found := map[string]tool{}
	for _, tool := range builtinTools() {
		found[tool.Name] = tool
	}
	snapshot, ok := found["browser_snapshot"]
	if !ok {
		t.Fatal("expected browser_snapshot tool")
	}
	if snapshot.Kind != "network" || !strings.Contains(snapshot.Description, "allowed host") {
		t.Fatalf("unexpected browser snapshot metadata: %#v", snapshot)
	}
	probe, ok := found["browser_probe_points"]
	if !ok {
		t.Fatal("expected browser_probe_points tool")
	}
	if probe.Kind != "network" || !strings.Contains(probe.Description, "approval-gated") {
		t.Fatalf("unexpected browser probe metadata: %#v", probe)
	}
}

func TestCallToolFetchPageAssetsFetchesScriptsAndStyles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>App</title><script src="/app.js"></script><link rel="stylesheet" href="/app.css"></head><body>Hello</body></html>`))
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("const token = location.hash;"))
	})
	mux.HandleFunc("/app.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte("body { color: red; }"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	params, _ := json.Marshal(map[string]any{
		"name":      "fetch_page_assets",
		"arguments": map[string]any{"url": server.URL, "max_assets": 5, "include_styles": true},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected fetch page assets success, got %v", result["content"])
	}
	content := result["content"].(string)
	if !strings.Contains(content, "app.js") || !strings.Contains(content, "app.css") || !strings.Contains(content, "const token") {
		t.Fatalf("expected JS and CSS assets in payload, got %s", content)
	}
}

func TestParseDuckDuckGoLiteResults(t *testing.T) {
	body := `<html><body>
		<a rel="nofollow" href="https://example.com/one">One Result</a>
		<a rel="nofollow" href="/lite/">DuckDuckGo Internal</a>
		<a rel="nofollow" href="https://example.com/two">Two <b>Result</b></a>
	</body></html>`
	results := parseDuckDuckGoLiteResults(body, 5)
	if len(results) != 2 {
		t.Fatalf("expected two external results, got %#v", results)
	}
	if results[0]["title"] != "One Result" || results[1]["title"] != "Two Result" {
		t.Fatalf("unexpected parsed titles: %#v", results)
	}
}
