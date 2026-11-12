package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCallToolFetchURLReturnsStructuredPayload(t *testing.T) {
	useLocalDocumentTransport(t)
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
		if !containsChineseText(tool.Description) {
			t.Fatalf("tool %s must expose a Chinese description, got %q", tool.Name, tool.Description)
		}
	}
	snapshot, ok := found["browser_snapshot"]
	if !ok {
		t.Fatal("expected browser_snapshot tool")
	}
	if snapshot.Kind != "network" || !strings.Contains(snapshot.Description, "主机白名单") {
		t.Fatalf("unexpected browser snapshot metadata: %#v", snapshot)
	}
	probe, ok := found["browser_probe_points"]
	if !ok {
		t.Fatal("expected browser_probe_points tool")
	}
	if probe.Kind != "network" || !strings.Contains(probe.Description, "审批") {
		t.Fatalf("unexpected browser probe metadata: %#v", probe)
	}
	if !strings.Contains(string(probe.InputSchema), `"max_probes":{"type":"integer","minimum":0,"maximum":8}`) {
		t.Fatalf("browser probe schema must enforce the bounded candidate limit, got %s", probe.InputSchema)
	}
	if hardMaxProbeCandidates != 8 || activeProbeInterval.Milliseconds() < 500 {
		t.Fatalf("unexpected active probe rate limit: candidates=%d interval=%s", hardMaxProbeCandidates, activeProbeInterval)
	}
}

func containsChineseText(value string) bool {
	for _, current := range value {
		if current >= '\u4e00' && current <= '\u9fff' {
			return true
		}
	}
	return false
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

func TestResearchSnapshotCachesAndDeduplicatesContent(t *testing.T) {
	useLocalDocumentTransport(t)
	cacheDir := t.TempDir()
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", cacheDir)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2024 00:00:00 GMT")
		_, _ = w.Write([]byte("<html><head><title>研究来源</title></head><body>可复核的研究正文</body></html>"))
	}))
	defer server.Close()

	first, err := researchSnapshots([]string{server.URL + "/a", server.URL + "/a#section", server.URL + "/b"}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("research snapshots: %v", err)
	}
	if first["unique_url_count"] != 2 || first["unique_content_count"] != 1 {
		t.Fatalf("expected URL and content deduplication, got %#v", first)
	}
	items := first["snapshots"].([]researchSnapshot)
	if len(items) != 2 || items[1].DuplicateOf == "" || items[0].ContentSHA256 == "" {
		t.Fatalf("expected duplicate evidence metadata, got %#v", items)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected two unique fetches, got %d", requests.Load())
	}

	second, err := researchSnapshots([]string{server.URL + "/a", server.URL + "/b"}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("cached research snapshots: %v", err)
	}
	if second["cache_hit_count"] != 2 || requests.Load() != 2 {
		t.Fatalf("expected two cache hits without more requests, result=%#v requests=%d", second, requests.Load())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected two cache files in %s, entries=%d err=%v", filepath.Clean(cacheDir), len(entries), err)
	}
}

func TestResearchSnapshotRetriesServiceUnavailableOnce(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("<html><head><title>恢复来源</title></head><body>来源已经恢复</body></html>"))
	}))
	defer server.Close()

	first, err := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("first research snapshot: %v", err)
	}
	item := first["snapshots"].([]researchSnapshot)[0]
	if item.IsError || item.Status != http.StatusOK {
		t.Fatalf("503 should retry once inside the snapshot, got %#v", item)
	}
	if len(item.AttemptStatuses) != 2 || item.AttemptStatuses[0] != http.StatusServiceUnavailable || item.AttemptStatuses[1] != http.StatusOK {
		t.Fatalf("expected 503 then 200 attempt statuses, got %#v", item.AttemptStatuses)
	}
	if requests != 2 {
		t.Fatalf("retry must happen in one snapshot call, requests=%d", requests)
	}
	second, err := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("cached research snapshot: %v", err)
	}
	cached := second["snapshots"].([]researchSnapshot)[0]
	if cached.IsError || !cached.CacheHit || requests != 2 {
		t.Fatalf("recovered snapshot should be cached, item=%#v requests=%d", cached, requests)
	}
}

func TestResearchSnapshotDoesNotCacheFailedResponse(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "permanent failure", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("<html><head><title>恢复来源</title></head><body>来源已经恢复</body></html>"))
	}))
	defer server.Close()

	first, err := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("first research snapshot: %v", err)
	}
	if !first["snapshots"].([]researchSnapshot)[0].IsError {
		t.Fatalf("expected failed HTTP response to be marked as error, got %#v", first)
	}
	second, err := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatalf("second research snapshot: %v", err)
	}
	item := second["snapshots"].([]researchSnapshot)[0]
	if item.IsError || item.CacheHit || requests != 2 {
		t.Fatalf("expected live recovery without failed cache reuse, item=%#v requests=%d", item, requests)
	}
}

func TestCompileResearchEvidenceProducesTraceableDeliverables(t *testing.T) {
	claims := []map[string]any{
		{"id": "C001", "text": "该项目支持结构化研究产物", "importance": "关键", "source_ids": []any{"S001", "S002"}, "confidence": "高"},
	}
	sources := []map[string]any{
		{"id": "S001", "url": "https://docs.example.com/research", "title": "官方文档", "source_type": "official", "published_at": "2026-01-01", "supports": []any{"C001"}},
		{"id": "S002", "url": "https://standards.example.net/report", "title": "标准说明", "source_type": "standard", "published_at": "2026-02-01", "supports": []any{"C001"}},
	}
	result, err := compileResearchEvidence("研究能力验收", claims, sources, 3650)
	if err != nil {
		t.Fatalf("compile research evidence: %v", err)
	}
	if result["review_status"] != "通过" {
		t.Fatalf("expected passing evidence review, got %#v", result)
	}
	for _, key := range []string{"markdown", "json", "csv"} {
		if !strings.Contains(result[key].(string), "C001") {
			t.Fatalf("expected %s deliverable to preserve claim mapping, got %q", key, result[key])
		}
	}
}

func TestNormalizeResearchImportanceAcceptsProviderAliases(t *testing.T) {
	tests := map[string]string{
		"key": "key", "关键": "key", "high": "key", "高": "key",
		"normal": "normal", "一般": "normal", "medium": "normal", "low": "normal", "中": "normal", "低": "normal", "": "normal",
	}
	for input, expected := range tests {
		if got := normalizeResearchImportance(input); got != expected {
			t.Fatalf("normalizeResearchImportance(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestCompileResearchEvidenceRejectsUnsupportedKeyClaim(t *testing.T) {
	result, err := compileResearchEvidence(
		"缺失来源验收",
		[]map[string]any{{"id": "C001", "text": "没有来源的关键结论", "importance": "关键"}},
		[]map[string]any{{"id": "S001", "url": "https://example.com/source", "title": "无关来源"}},
		365,
	)
	if err != nil {
		t.Fatalf("compile research evidence: %v", err)
	}
	if result["review_status"] != "不通过" {
		t.Fatalf("expected unsupported key claim to fail, got %#v", result)
	}
}

func TestBuiltinToolsIncludesResearchDeliveryTools(t *testing.T) {
	found := map[string]tool{}
	for _, item := range builtinTools() {
		found[item.Name] = item
	}
	for _, name := range []string{"research_snapshot", "compile_research_evidence"} {
		item, ok := found[name]
		if !ok || !containsChineseText(item.Description) {
			t.Fatalf("expected localized research tool %s, got %#v", name, item)
		}
	}
}
