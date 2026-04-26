package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultMaxFetchBytes = 256 * 1024
const hardMaxFetchBytes = 1024 * 1024

var httpClient = &http.Client{Timeout: 12 * time.Second}
var searchEndpoint = "https://lite.duckduckgo.com/lite/"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch req.Method {
		case "tools/list":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: callTool(req.Params)})
		default:
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{
		{
			Name:        "web_search",
			Description: "Search the web and return a compact list of result titles, URLs, and snippets.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1},"limit":{"type":"integer","minimum":1,"maximum":10}},"required":["query"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "fetch_url",
			Description: "Fetch a web page over HTTP(S) and return status, title, text preview, and truncated content.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"max_bytes":{"type":"integer","minimum":1024,"maximum":1048576}},"required":["url"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "fetch_page_assets",
			Description: "Fetch an HTML page plus referenced JavaScript and CSS assets for security review.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"max_assets":{"type":"integer","minimum":1,"maximum":50},"max_bytes_per_asset":{"type":"integer","minimum":1024,"maximum":1048576},"include_styles":{"type":"boolean"}},"required":["url"],"additionalProperties":false}`),
			Kind:        "network",
		},
	}
}

func callTool(params json.RawMessage) map[string]any {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	var payload map[string]any
	var err error
	switch input.Name {
	case "web_search":
		query, _ := input.Arguments["query"].(string)
		limit := optionalInt(input.Arguments, "limit", 5, 1, 10)
		payload, err = webSearch(query, limit)
	case "fetch_url":
		rawURL, _ := input.Arguments["url"].(string)
		maxBytes := optionalInt(input.Arguments, "max_bytes", defaultMaxFetchBytes, 1024, hardMaxFetchBytes)
		payload, err = fetchURL(rawURL, maxBytes)
	case "fetch_page_assets":
		rawURL, _ := input.Arguments["url"].(string)
		maxAssets := optionalInt(input.Arguments, "max_assets", 20, 1, 50)
		maxBytes := optionalInt(input.Arguments, "max_bytes_per_asset", defaultMaxFetchBytes, 1024, hardMaxFetchBytes)
		includeStyles, _ := input.Arguments["include_styles"].(bool)
		if _, exists := input.Arguments["include_styles"]; !exists {
			includeStyles = true
		}
		payload, err = fetchPageAssets(rawURL, maxAssets, maxBytes, includeStyles)
	default:
		return map[string]any{"content": "unknown tool", "is_error": true}
	}
	if err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	encoded, _ := json.Marshal(payload)
	return map[string]any{"content": string(encoded), "is_error": false}
}

func webSearch(query string, limit int) (map[string]any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	endpoint, err := url.Parse(searchEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse search endpoint: %w", err)
	}
	values := endpoint.Query()
	values.Set("q", query)
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoFlow-Agent/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, hardMaxFetchBytes+1))
	if err != nil {
		return nil, err
	}
	results := parseDuckDuckGoLiteResults(string(body), limit)
	return map[string]any{"query": query, "status": resp.StatusCode, "source": "duckduckgo-lite", "results": results}, nil
}

func fetchURL(rawURL string, maxBytes int) (map[string]any, error) {
	parsed, err := validateHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	page, err := fetchRaw(parsed, maxBytes)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"url":          parsed.String(),
		"status":       page.status,
		"content_type": page.contentType,
		"title":        extractHTMLTitle(page.content),
		"text_preview": compactText(stripHTML(page.content), 1200),
		"content":      page.content,
		"bytes_read":   page.bytesRead,
		"truncated":    page.truncated,
	}, nil
}

func fetchPageAssets(rawURL string, maxAssets, maxBytesPerAsset int, includeStyles bool) (map[string]any, error) {
	parsed, err := validateHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	page, err := fetchRaw(parsed, maxBytesPerAsset)
	if err != nil {
		return nil, err
	}
	refs := extractAssetRefs(parsed, page.content, includeStyles)
	if len(refs) > maxAssets {
		refs = refs[:maxAssets]
	}
	assets := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		assetURL, err := validateHTTPURL(ref.URL)
		if err != nil {
			assets = append(assets, map[string]any{"url": ref.URL, "type": ref.Type, "is_error": true, "error": err.Error()})
			continue
		}
		asset, err := fetchRaw(assetURL, maxBytesPerAsset)
		if err != nil {
			assets = append(assets, map[string]any{"url": assetURL.String(), "type": ref.Type, "is_error": true, "error": err.Error()})
			continue
		}
		assets = append(assets, map[string]any{
			"url":          assetURL.String(),
			"type":         ref.Type,
			"status":       asset.status,
			"content_type": asset.contentType,
			"content":      asset.content,
			"text_preview": compactText(stripHTML(asset.content), 800),
			"bytes_read":   asset.bytesRead,
			"truncated":    asset.truncated,
			"is_error":     false,
		})
	}
	return map[string]any{
		"url":              parsed.String(),
		"status":           page.status,
		"content_type":     page.contentType,
		"title":            extractHTMLTitle(page.content),
		"html":             page.content,
		"text_preview":     compactText(stripHTML(page.content), 1200),
		"bytes_read":       page.bytesRead,
		"truncated":        page.truncated,
		"asset_count":      len(assets),
		"max_assets":       maxAssets,
		"include_styles":   includeStyles,
		"assets":           assets,
		"assets_truncated": len(extractAssetRefs(parsed, page.content, includeStyles)) > maxAssets,
	}, nil
}

type fetchedPage struct {
	status      int
	contentType string
	content     string
	bytesRead   int
	truncated   bool
}

func fetchRaw(parsed *url.URL, maxBytes int) (fetchedPage, error) {
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fetchedPage{}, err
	}
	req.Header.Set("User-Agent", "GoFlow-Agent/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fetchedPage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return fetchedPage{}, err
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}
	content := string(body)
	return fetchedPage{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), content: content, bytesRead: len(body), truncated: truncated}, nil
}

func validateHTTPURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are supported")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("url host is required")
	}
	return parsed, nil
}

var resultLinkRe = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
var scriptSrcRe = regexp.MustCompile(`(?is)<script[^>]+src=["']([^"']+)["'][^>]*>`)
var stylesheetHrefRe = regexp.MustCompile(`(?is)<link[^>]+href=["']([^"']+)["'][^>]*>`)
var relStylesheetRe = regexp.MustCompile(`(?is)rel=["'][^"']*stylesheet[^"']*["']`)

type assetRef struct {
	URL  string
	Type string
}

func extractAssetRefs(base *url.URL, htmlContent string, includeStyles bool) []assetRef {
	refs := make([]assetRef, 0)
	seen := make(map[string]struct{})
	addRef := func(raw, kind string) {
		resolved := resolveAssetURL(base, html.UnescapeString(strings.TrimSpace(raw)))
		if resolved == "" {
			return
		}
		if _, exists := seen[resolved]; exists {
			return
		}
		seen[resolved] = struct{}{}
		refs = append(refs, assetRef{URL: resolved, Type: kind})
	}
	for _, match := range scriptSrcRe.FindAllStringSubmatch(htmlContent, -1) {
		if len(match) > 1 {
			addRef(match[1], "script")
		}
	}
	if includeStyles {
		for _, match := range stylesheetHrefRe.FindAllStringSubmatch(htmlContent, -1) {
			if len(match) < 2 || !relStylesheetRe.MatchString(match[0]) {
				continue
			}
			addRef(match[1], "stylesheet")
		}
	}
	return refs
}

func resolveAssetURL(base *url.URL, raw string) string {
	if raw == "" || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "javascript:") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return base.ResolveReference(parsed).String()
}

func parseDuckDuckGoLiteResults(body string, limit int) []map[string]any {
	matches := resultLinkRe.FindAllStringSubmatch(body, -1)
	results := make([]map[string]any, 0, limit)
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(results) >= limit {
			break
		}
		link := html.UnescapeString(match[1])
		title := compactText(stripHTML(html.UnescapeString(match[2])), 240)
		if title == "" || strings.HasPrefix(link, "/") || strings.Contains(link, "duckduckgo.com") {
			continue
		}
		if redirect, err := url.Parse(link); err == nil {
			if raw := redirect.Query().Get("uddg"); raw != "" {
				if decoded, decodeErr := url.QueryUnescape(raw); decodeErr == nil {
					link = decoded
				}
			}
		}
		if _, exists := seen[link]; exists {
			continue
		}
		seen[link] = struct{}{}
		results = append(results, map[string]any{"title": title, "url": link})
	}
	return results
}

func extractHTMLTitle(content string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return compactText(stripHTML(html.UnescapeString(match[1])), 240)
}

func stripHTML(content string) string {
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<[^>]+>`)
	return html.UnescapeString(re.ReplaceAllString(content, " "))
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-3]) + "..."
}

func optionalInt(args map[string]any, key string, fallback, minimum, maximum int) int {
	value := fallback
	switch typed := args[key].(type) {
	case float64:
		value = int(typed)
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil {
			value = parsed
		}
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func write(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}
