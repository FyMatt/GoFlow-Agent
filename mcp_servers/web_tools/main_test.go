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
