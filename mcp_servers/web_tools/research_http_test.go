package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func useLocalDocumentTransport(t *testing.T) {
	t.Helper()
	previous := publicDocsTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	publicDocsTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		publicDocsTransport = previous
	})
}

func documentTool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(params)
	if result["is_error"] == true {
		t.Fatalf("%s failed: %v", name, result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestResearchRefreshAndExpiredCache(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	var requests atomic.Int32
	var refreshHeaders atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := requests.Add(1)
		refreshHeaders.Store(r.Header.Get("Cache-Control") == "no-cache" && r.Header.Get("Pragma") == "no-cache")
		fmt.Fprintf(w, "<title>Technical docs</title>version %d", version)
	}))
	defer server.Close()
	args := map[string]any{"urls": []string{server.URL}, "max_bytes_per_page": 4096}
	first := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
	cached := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
	if requests.Load() != 1 || cached["cache_hit"] != true || cached["cache_provenance"] != "cache" || cached["freshness"] != "cached_unvalidated" || cached["fetched_at"] != first["fetched_at"] {
		t.Fatalf("cache must retain its fetch time without claiming revalidation: %#v", cached)
	}
	args["refresh"] = true
	fresh := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
	if requests.Load() != 2 || !refreshHeaders.Load() || fresh["cache_hit"] != false || fresh["cache_provenance"] != "network" || fresh["freshness"] != "live_fetch" || fresh["content_sha256"] == first["content_sha256"] || fresh["fetched_at"] == first["fetched_at"] {
		t.Fatalf("refresh must obtain new evidence: %#v", fresh)
	}
	delete(args, "refresh")
	path := researchCachePath(server.URL)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored researchSnapshot
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	stored.FetchedAt = time.Now().Add(-7 * time.Hour).UTC().Format(time.RFC3339Nano)
	if err := writeResearchSnapshotCache(path, stored); err != nil {
		t.Fatal(err)
	}
	expired := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
	if requests.Load() != 3 || expired["cache_hit"] != false || expired["content_sha256"] == fresh["content_sha256"] {
		t.Fatalf("expired evidence must be fetched again: %#v", expired)
	}
	args["cache_ttl_seconds"] = 0
	documentTool(t, "research_snapshot", args)
	if requests.Load() != 4 {
		t.Fatal("cache_ttl_seconds=0 must still bypass the cache")
	}
}

func TestResearchLastModifiedIsNotPublicationDate(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	const lastModified = "Mon, 01 Jan 2024 00:00:00 GMT"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Last-Modified", lastModified)
		fmt.Fprint(w, "<title>Technical docs</title>API reference")
	}))
	defer server.Close()
	args := map[string]any{"urls": []string{server.URL}, "max_bytes_per_page": 4096}
	for _, wantCacheHit := range []bool{false, true} {
		item := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
		if item["last_modified"] != lastModified || stringValue(item["published_at"]) != "" || item["cache_hit"] != wantCacheHit {
			t.Fatalf("Last-Modified must stay separate on live and cached snapshots: %#v", item)
		}
		compiled, err := compileResearchEvidence("Docs", []map[string]any{{"text": "API reference", "source_ids": []string{"S001"}}}, []map[string]any{item}, 1)
		if err != nil {
			t.Fatal(err)
		}
		source := compiled["sources"].([]map[string]any)[0]
		if source["published_at"] != "" || source["last_modified"] != lastModified || source["stale"] != false {
			t.Fatalf("compilation must not infer publication age from Last-Modified: %#v", source)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("expected the second snapshot to reuse the current cache")
	}
	// Reproduce the old, unversioned cache containing the conflated timestamp.
	path := researchCachePath(server.URL)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "cache_version")
	delete(legacy, "last_modified")
	legacy["published_at"] = lastModified
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	refetched := documentTool(t, "research_snapshot", args)["snapshots"].([]any)[0].(map[string]any)
	if requests.Load() != 2 || refetched["cache_hit"] != false || stringValue(refetched["published_at"]) != "" || refetched["last_modified"] != lastModified {
		t.Fatalf("legacy cache must be invalidated without preserving its false publication date: %#v", refetched)
	}
}

func TestFetchDocumentAuditAndSizeLimits(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	var requests atomic.Int32
	body := strings.Repeat("technical documentation ", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/docs", http.StatusFound)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	fetched := documentTool(t, "fetch_url", map[string]any{"url": server.URL + "/redirect", "max_bytes": 1024, "refresh": true, "allowed_domains": []string{"127.0.0.1"}})
	if fetched["final_url"] != server.URL+"/docs" || fetched["status"] != float64(200) || fetched["truncated"] != true || fetched["bytes_read"] != float64(1024) || fetched["content_sha256"] != textSHA256(body[:1024]) || fetched["cache_provenance"] != "network" {
		t.Fatalf("incomplete fetch audit or incorrect size limit: %#v", fetched)
	}
	if _, err := time.Parse(time.RFC3339Nano, fetched["fetched_at"].(string)); err != nil {
		t.Fatal(err)
	}
	if len(fetched["redirect_chain"].([]any)) != 2 {
		t.Fatalf("missing redirect history: %#v", fetched)
	}
	large, _ := researchSnapshots([]string{server.URL}, 8192, defaultResearchCacheTTL)
	largeItem := large["snapshots"].([]researchSnapshot)[0]
	small, _ := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	smallItem := small["snapshots"].([]researchSnapshot)[0]
	if smallItem.CacheHit || smallItem.BytesRead != 4096 || !smallItem.Truncated || smallItem.ContentSHA256 != textSHA256(body[:4096]) || largeItem.ContentSHA256 == smallItem.ContentSHA256 {
		t.Fatalf("cache reuse must respect caller size: %#v", smallItem)
	}
	largeAgain, _ := researchSnapshots([]string{server.URL}, 8192, defaultResearchCacheTTL)
	if largeAgain["snapshots"].([]researchSnapshot)[0].CacheHit || requests.Load() != 5 {
		t.Fatalf("smaller cached evidence must not satisfy larger fetch: %#v requests=%d", largeAgain, requests.Load())
	}
}

func TestResearchDomainFilterEnforcesRedirectsAndCachedHistory(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	var excludedRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)+"/bounce", http.StatusFound)
		case "/bounce":
			excludedRequests.Add(1)
			http.Redirect(w, r, server.URL+"/docs", http.StatusFound)
		default:
			fmt.Fprint(w, "documentation")
		}
	}))
	defer server.Close()
	unfiltered, _ := researchSnapshots([]string{server.URL + "/start"}, 4096, defaultResearchCacheTTL)
	item := unfiltered["snapshots"].([]researchSnapshot)[0]
	if item.IsError || len(item.RedirectChain) != 3 || item.FinalURL != server.URL+"/docs" {
		t.Fatalf("expected successful redirect evidence: %#v", item)
	}
	filtered, err := researchSnapshotsWithOptions(context.Background(), []string{server.URL + "/start", strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/docs", server.URL + "/docs"}, 4096, defaultResearchCacheTTL, researchOptions{allowedDomains: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	items := filtered["snapshots"].([]researchSnapshot)
	if filtered["error_count"] != 2 || filtered["success_count"] != 1 || !items[0].IsError || items[0].CacheHit || !items[1].IsError || items[2].IsError || excludedRequests.Load() != 1 {
		t.Fatalf("filter must enforce initial URLs, redirects, and cached intermediate hops: %#v excluded requests=%d", filtered, excludedRequests.Load())
	}
	if items[0].Status != http.StatusFound || items[0].FetchedAt == "" || items[0].FinalURL != server.URL+"/start" {
		t.Fatalf("rejected redirect must retain the response metadata: %#v", items[0])
	}
	for _, raw := range []string{"https://docs.example.com/guide", "https://example.com/guide"} {
		if _, err := (researchOptions{allowedDomains: []string{"example.com"}}).validateURL(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"https://example.com.evil.test", "https://evilexample.com", "https://example.com@evil.test", "file:///docs"} {
		if _, err := (researchOptions{allowedDomains: []string{"example.com"}}).validateURL(raw); err == nil {
			t.Fatalf("filter allowed %s", raw)
		}
	}
}

func TestSearchFiltersBeforeLimitAndDecodesRedirectLinks(t *testing.T) {
	useLocalDocumentTransport(t)
	var refresh atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refresh.Store(r.Header.Get("Cache-Control") == "no-cache")
		fmt.Fprintf(w, `<a href="https://excluded.test/docs">Excluded</a><a href="https://example.com.evil.test">Spoof</a><a href="https://duckduckgo.com/l/?uddg=%s">Official</a><a href="https://example.com/api">API</a>`, url.QueryEscape("https://docs.example.com/guide?q=a%2Bb"))
	}))
	defer server.Close()
	previous := searchEndpoint
	searchEndpoint = server.URL
	t.Cleanup(func() { searchEndpoint = previous })
	payload := documentTool(t, "web_search", map[string]any{"query": "current technical documentation", "limit": 1, "allowed_domains": []string{"Example.COM."}, "refresh": true})
	results := payload["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["url"] != "https://docs.example.com/guide?q=a%2Bb" || !refresh.Load() || payload["content_sha256"] == "" || payload["fetched_at"] == "" {
		t.Fatalf("search must apply decoded domain filtering before limit: %#v", payload)
	}
}

func TestResearchPartialFailuresAndFailedRefresh(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" || fail.Load() {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "working technical docs")
	}))
	defer server.Close()
	result, err := researchSnapshots([]string{server.URL + "/good", server.URL + "/bad", "file:///private"}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatal(err)
	}
	items := result["snapshots"].([]researchSnapshot)
	if result["success_count"] != 1 || result["error_count"] != 2 || !items[1].IsError || items[1].Status != 500 || items[1].FinalURL == "" || items[1].FetchedAt == "" || items[1].ContentSHA256 == "" || !items[2].IsError {
		t.Fatalf("partial failures must remain auditable: %#v", result)
	}
	fail.Store(true)
	refreshed, err := researchSnapshotsWithOptions(context.Background(), []string{server.URL + "/good"}, 4096, defaultResearchCacheTTL, researchOptions{refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	item := refreshed["snapshots"].([]researchSnapshot)[0]
	if !item.IsError || item.CacheHit || item.Status != 500 || item.Text == items[0].Text {
		t.Fatalf("failed refresh must not relabel stale evidence: %#v", item)
	}
	tooMany := make([]string, hardMaxResearchURLs+1)
	if _, err := researchSnapshots(tooMany, 4096, defaultResearchCacheTTL); err == nil {
		t.Fatal("unbounded research batch accepted")
	}
}

func TestIncompleteBodiesAndSearchFailuresDoNotBecomeEvidence(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			http.Error(w, `<a href="https://docs.example.com">Error body link</a>`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", "4096")
		fmt.Fprint(w, "incomplete document")
	}))
	defer server.Close()
	result, err := researchSnapshots([]string{server.URL}, 4096, defaultResearchCacheTTL)
	if err != nil {
		t.Fatal(err)
	}
	item := result["snapshots"].([]researchSnapshot)[0]
	if !item.IsError || item.Status != 200 || item.FinalURL != server.URL || item.FetchedAt == "" || item.ContentSHA256 != "" {
		t.Fatalf("incomplete body must retain HTTP audit without successful evidence: %#v", item)
	}
	if _, err := os.Stat(researchCachePath(server.URL)); !os.IsNotExist(err) {
		t.Fatalf("incomplete body must not be cached: %v", err)
	}
	previous := searchEndpoint
	searchEndpoint = server.URL + "/search"
	t.Cleanup(func() { searchEndpoint = previous })
	if _, err := webSearch("docs", 5); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("failed search response was treated as results: %v", err)
	}
}

func TestPublicDocumentsRejectNonPublicNetworks(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicDocumentIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted non-public IP %s", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicDocumentIP(netip.MustParseAddr(raw)) {
			t.Errorf("rejected public IP %s", raw)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("public-document transport reached loopback")
	}))
	defer server.Close()
	if _, err := fetchURL(server.URL, 1024); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("expected network boundary rejection: %v", err)
	}
}

// The acceptance fixture serves research sources from loopback. The switch is
// explicit and default-off, so production keeps the non-public guard.
func TestFetchURLAllowsLoopbackWithAcceptanceSwitch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fixture body"))
	}))
	defer server.Close()
	if _, err := fetchURL(server.URL, 1024); err == nil {
		t.Fatalf("loopback must stay blocked without the switch")
	}
	t.Setenv("GOFLOW_RESEARCH_ALLOW_LOCAL", "1")
	document, err := fetchURL(server.URL, 1024)
	if err != nil {
		t.Fatalf("acceptance switch must allow the loopback fixture origin: %v", err)
	}
	if document == nil {
		t.Fatalf("expected a fetched document, got nil")
	}
}

func TestResearchCancellation(t *testing.T) {
	useLocalDocumentTransport(t)
	t.Setenv("GOFLOW_RESEARCH_CACHE_DIR", t.TempDir())
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan map[string]any, 1)
	go func() {
		result, _ := researchSnapshotsWithOptions(ctx, []string{server.URL + "/a", server.URL + "/b"}, 4096, defaultResearchCacheTTL, researchOptions{})
		done <- result
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture never received request")
	}
	cancel()
	select {
	case result := <-done:
		if result["error_count"] != 2 {
			t.Fatalf("canceled requests must report failures: %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("research did not respect cancellation")
	}
}

func TestResearchOptionsFailClosed(t *testing.T) {
	for _, args := range []map[string]any{
		{"allowed_domains": "example.com"}, {"allowed_domains": []any{}},
		{"allowed_domains": []any{42}}, {"allowed_domains": []any{"https://example.com"}},
		{"allowed_domains": []any{"*.example.com"}}, {"allowed_domains": []any{"example.com:80"}},
		{"allowed_domains": []any{"example.com/path"}}, {"refresh": "true"},
	} {
		if _, err := parseResearchOptions(args); err == nil {
			t.Fatalf("invalid options silently accepted: %#v", args)
		}
	}
}

func TestCompileResearchRetainsAuditAndCacheCaveat(t *testing.T) {
	sources := []map[string]any{{
		"id": "S001", "url": "https://example.com/start", "final_url": "https://docs.example.net/api",
		"status": 200, "fetched_at": "2026-01-01T12:00:00Z", "content_sha256": "snapshot-hash",
		"published_at": "2025-12-31", "last_modified": "Thu, 01 Jan 2026 00:00:00 GMT",
		"cache_hit": true, "cache_provenance": "cache", "freshness": "latest", "source_type": "official",
	}, {"id": "S002", "url": "https://failed.test", "status": 500, "is_error": true}}
	result, err := compileResearchEvidence("Technical docs", []map[string]any{{"text": "Documented API", "source_ids": []string{"S001"}}}, sources, 365)
	if err != nil {
		t.Fatal(err)
	}
	compiled := result["sources"].([]map[string]any)
	if len(compiled) != 1 || compiled[0]["freshness"] != "cached_unvalidated" || compiled[0]["domain"] != "docs.example.net" || compiled[0]["final_url"] != sources[0]["final_url"] || compiled[0]["status"] != 200 {
		t.Fatalf("compilation must retain cache and fetch provenance: %#v", compiled)
	}
	if compiled[0]["published_at"] != sources[0]["published_at"] || compiled[0]["last_modified"] != sources[0]["last_modified"] {
		t.Fatalf("actual publication date and HTTP Last-Modified must remain distinct: %#v", compiled[0])
	}
	for _, key := range []string{"markdown", "json", "csv"} {
		text := result[key].(string)
		if !strings.Contains(text, "cached_unvalidated") || !strings.Contains(text, "snapshot-hash") || !strings.Contains(text, "2026-01-01T12:00:00Z") || !strings.Contains(text, "https://docs.example.net/api") || !strings.Contains(text, sources[0]["last_modified"].(string)) {
			t.Fatalf("%s discarded audit metadata: %s", key, text)
		}
	}
	if _, err := csv.NewReader(strings.NewReader(result["csv"].(string))).ReadAll(); err != nil {
		t.Fatalf("invalid CSV deliverable: %v", err)
	}
}
