package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultMaxFetchBytes = 256 * 1024
const hardMaxFetchBytes = 1024 * 1024
const hardMaxProbeCandidates = 8
const activeProbeInterval = 500 * time.Millisecond
const defaultResearchCacheTTL = 6 * time.Hour

// Unversioned snapshots conflated HTTP Last-Modified with publication dates.
const researchSnapshotCacheVersion = 1
const hardMaxResearchURLs = 24
const defaultResearchTextChars = 12000

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
			Description: "搜索互联网公开文档并返回精简结果，可限制来源域名；无需渗透测试授权。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1},"limit":{"type":"integer","minimum":1,"maximum":10},"allowed_domains":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20,"description":"Only these domains and their subdomains; checked before fetching and on every redirect."},"refresh":{"type":"boolean","description":"Bypass local snapshot cache and request intermediary revalidation; does not guarantee the publisher has updated the document."}},"required":["query"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "fetch_url",
			Description: "通过 HTTP(S) 获取公开文档，返回最终 URL、状态、获取时间、正文哈希和受限长度正文；支持域名过滤和 refresh 请求，无需渗透测试授权。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"max_bytes":{"type":"integer","minimum":1024,"maximum":1048576},"content_chars":{"type":"integer","minimum":200,"maximum":1048576},"allowed_domains":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20,"description":"Only these domains and their subdomains; checked before fetching and on every redirect."},"refresh":{"type":"boolean","description":"Bypass local snapshot cache and request intermediary revalidation; does not guarantee the publisher has updated the document."}},"required":["url"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "research_snapshot",
			Description: "并发获取公开研究来源，支持 refresh 强制联网刷新和域名过滤；返回最终 URL、时间、哈希与缓存来源。缓存未重新验证时不代表最新资料，无需渗透测试授权。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"urls":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":24},"max_bytes_per_page":{"type":"integer","minimum":1024,"maximum":262144},"cache_ttl_seconds":{"type":"integer","minimum":0,"maximum":604800},"allowed_domains":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20,"description":"Only these domains and their subdomains; checked before fetching and on every redirect."},"refresh":{"type":"boolean","description":"Bypass local snapshot cache and request intermediary revalidation; does not guarantee the publisher has updated the document."}},"required":["urls"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "compile_research_evidence",
			Description: "核验研究结论与来源的对应关系，检查冲突、时效和来源独立性，并生成 Markdown、JSON 与 CSV 交付内容。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","minLength":1},"claims":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{"id":{"type":"string"},"text":{"type":"string","minLength":1},"importance":{"type":"string","enum":["key","normal","关键","一般","high","medium","low","高","中","低"]},"source_ids":{"type":"array","items":{"type":"string"}},"source_urls":{"type":"array","items":{"type":"string"}},"confidence":{"type":"string"},"notes":{"type":"string"}},"required":["text"],"additionalProperties":true}},"sources":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{"id":{"type":"string"},"url":{"type":"string","minLength":1},"title":{"type":"string"},"domain":{"type":"string"},"published_at":{"type":"string","description":"实际来源数据中已知的发布日期；不从 HTTP Last-Modified 响应头推断。"},"last_modified":{"type":"string","description":"HTTP Last-Modified 响应头，表示最后修改时间，不代表发布日期。"},"fetched_at":{"type":"string"},"source_type":{"type":"string"},"excerpt":{"type":"string"},"content_sha256":{"type":"string"},"supports":{"type":"array","items":{"type":"string"}},"contradicts":{"type":"array","items":{"type":"string"}},"final_url":{"type":"string"},"status":{"type":"integer"},"cache_hit":{"type":"boolean"},"cache_provenance":{"type":"string"},"freshness":{"type":"string"},"redirect_chain":{"type":"array","items":{"type":"string"}},"bytes_read":{"type":"integer"},"truncated":{"type":"boolean"},"is_error":{"type":"boolean"}},"required":["url"],"additionalProperties":true}},"stale_after_days":{"type":"integer","minimum":1,"maximum":3650}},"required":["topic","claims","sources"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "fetch_page_assets",
			Description: "获取 HTML 页面及其引用的 JavaScript 和 CSS 资源，用于安全审查。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"max_assets":{"type":"integer","minimum":1,"maximum":50},"max_bytes_per_asset":{"type":"integer","minimum":1024,"maximum":1048576},"include_styles":{"type":"boolean"}},"required":["url"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "browser_snapshot",
			Description: "在本地无头浏览器中打开已授权的 HTTP(S) 目标，返回精简 DOM、表单、网络攻击面、风险信号、实际布局尺寸与动画元数据和可选截图证据。可指定 320–4096 CSS 像素视口，默认 1365×768；仅使用内置固定脚本。该工具默认只做被动收集，并要求明确的主机白名单。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20},"authorized_scope":{"type":"boolean"},"wait_ms":{"type":"integer","minimum":500,"maximum":10000},"timeout_seconds":{"type":"integer","minimum":2,"maximum":30},"max_dom_chars":{"type":"integer","minimum":500,"maximum":20000},"screenshot":{"type":"boolean"},"viewport_width":{"type":"integer","minimum":320,"maximum":4096,"default":1365},"viewport_height":{"type":"integer","minimum":320,"maximum":4096,"default":768}},"required":["url","allowed_hosts","authorized_scope"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "browser_probe_points",
			Description: "在本地无头浏览器中打开已授权的 HTTP(S) 目标，枚举精简的 XSS/SQLi 探测面，并可在审批后运行少量 GET 金丝雀探测。该工具不会暴力破解、大范围爬取或提交 POST 表单。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20},"authorized_scope":{"type":"boolean"},"active_probe_approved":{"type":"boolean"},"probe_types":{"type":"array","items":{"type":"string","enum":["reflection","sql_error"]},"maxItems":2},"wait_ms":{"type":"integer","minimum":500,"maximum":10000},"timeout_seconds":{"type":"integer","minimum":2,"maximum":30},"max_dom_chars":{"type":"integer","minimum":500,"maximum":20000},"max_probes":{"type":"integer","minimum":0,"maximum":8}},"required":["url","allowed_hosts","authorized_scope","active_probe_approved"],"additionalProperties":false}`),
			Kind:        "network",
		},
	}
}

func callTool(params json.RawMessage) map[string]any {
	return callToolContext(context.Background(), params)
}

func callToolContext(ctx context.Context, params json.RawMessage) map[string]any {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	var options researchOptions
	var err error
	if input.Name == "web_search" || input.Name == "fetch_url" || input.Name == "research_snapshot" {
		options, err = parseResearchOptions(input.Arguments)
		if err != nil {
			return map[string]any{"content": err.Error(), "is_error": true}
		}
	}
	var payload map[string]any
	switch input.Name {
	case "web_search":
		query, _ := input.Arguments["query"].(string)
		limit := optionalInt(input.Arguments, "limit", 5, 1, 10)
		payload, err = webSearchWithOptions(ctx, query, limit, options)
	case "fetch_url":
		rawURL, _ := input.Arguments["url"].(string)
		maxBytes := optionalInt(input.Arguments, "max_bytes", defaultMaxFetchBytes, 1024, hardMaxFetchBytes)
		fetchOptions := options
		if raw, ok := input.Arguments["content_chars"].(float64); ok && int(raw) > 0 {
			fetchOptions.contentChars = int(raw)
		}
		payload, err = fetchURLWithOptions(ctx, rawURL, maxBytes, fetchOptions)
	case "research_snapshot":
		urls := optionalStringList(input.Arguments, "urls", hardMaxResearchURLs)
		maxBytes := optionalInt(input.Arguments, "max_bytes_per_page", 128*1024, 1024, 256*1024)
		cacheTTL := optionalInt(input.Arguments, "cache_ttl_seconds", int(defaultResearchCacheTTL.Seconds()), 0, int((7 * 24 * time.Hour).Seconds()))
		payload, err = researchSnapshotsWithOptions(ctx, urls, maxBytes, time.Duration(cacheTTL)*time.Second, options)
	case "compile_research_evidence":
		topic, _ := input.Arguments["topic"].(string)
		staleDays := optionalInt(input.Arguments, "stale_after_days", 365, 1, 3650)
		payload, err = compileResearchEvidence(topic, optionalObjectList(input.Arguments, "claims", 100), optionalObjectList(input.Arguments, "sources", 100), staleDays)
	case "fetch_page_assets":
		rawURL, _ := input.Arguments["url"].(string)
		maxAssets := optionalInt(input.Arguments, "max_assets", 20, 1, 50)
		maxBytes := optionalInt(input.Arguments, "max_bytes_per_asset", defaultMaxFetchBytes, 1024, hardMaxFetchBytes)
		includeStyles, _ := input.Arguments["include_styles"].(bool)
		if _, exists := input.Arguments["include_styles"]; !exists {
			includeStyles = true
		}
		payload, err = fetchPageAssets(rawURL, maxAssets, maxBytes, includeStyles)
	case "browser_snapshot":
		viewportWidth, viewportHeight, viewportErr := snapshotViewport(input.Arguments)
		if viewportErr != nil {
			return map[string]any{"content": viewportErr.Error(), "is_error": true}
		}
		rawURL, _ := input.Arguments["url"].(string)
		allowedHosts := optionalStringList(input.Arguments, "allowed_hosts", 20)
		authorizedScope, _ := input.Arguments["authorized_scope"].(bool)
		waitMS := optionalInt(input.Arguments, "wait_ms", 2000, 500, 10000)
		timeoutSeconds := optionalInt(input.Arguments, "timeout_seconds", 15, 2, 30)
		maxDOMChars := optionalInt(input.Arguments, "max_dom_chars", 4000, 500, 20000)
		screenshot, _ := input.Arguments["screenshot"].(bool)
		payload, err = browserSnapshot(rawURL, allowedHosts, authorizedScope, waitMS, timeoutSeconds, maxDOMChars, screenshot, viewportWidth, viewportHeight)
	case "browser_probe_points":
		rawURL, _ := input.Arguments["url"].(string)
		allowedHosts := optionalStringList(input.Arguments, "allowed_hosts", 20)
		authorizedScope, _ := input.Arguments["authorized_scope"].(bool)
		activeApproved, _ := input.Arguments["active_probe_approved"].(bool)
		probeTypes := optionalStringList(input.Arguments, "probe_types", 2)
		waitMS := optionalInt(input.Arguments, "wait_ms", 2000, 500, 10000)
		timeoutSeconds := optionalInt(input.Arguments, "timeout_seconds", 15, 2, 30)
		maxDOMChars := optionalInt(input.Arguments, "max_dom_chars", 4000, 500, 20000)
		maxProbes := optionalInt(input.Arguments, "max_probes", 6, 0, hardMaxProbeCandidates)
		payload, err = browserProbePoints(rawURL, allowedHosts, authorizedScope, activeApproved, probeTypes, waitMS, timeoutSeconds, maxDOMChars, maxProbes)
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
	return webSearchWithOptions(context.Background(), query, limit, researchOptions{})
}

func webSearchWithOptions(ctx context.Context, query string, limit int, options researchOptions) (map[string]any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	endpoint, err := validateHTTPURL(searchEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse search endpoint: %w", err)
	}
	values := endpoint.Query()
	values.Set("q", query)
	endpoint.RawQuery = values.Encode()
	// The domain filter applies to results, not the search engine itself.
	page, err := fetchDocument(ctx, endpoint, hardMaxFetchBytes, researchOptions{refresh: options.refresh})
	if err != nil {
		return nil, err
	}
	if page.status < 200 || page.status >= 300 {
		return nil, fmt.Errorf("search returned HTTP %d", page.status)
	}
	limit = minInt(10, limit)
	if limit < 1 {
		limit = 1
	}
	results := parseSearchResults(page.content, limit, options)
	return map[string]any{
		"query": query, "status": page.status, "source": "duckduckgo-lite", "results": results,
		"final_url": page.finalURL, "fetched_at": page.fetchedAt, "content_sha256": textSHA256(page.content),
		"cache_hit": false, "cache_provenance": "network", "freshness": "live_fetch",
		"bytes_read": page.bytesRead, "truncated": page.truncated,
	}, nil
}

func fetchURL(rawURL string, maxBytes int) (map[string]any, error) {
	return fetchURLWithOptions(context.Background(), rawURL, maxBytes, researchOptions{})
}

func fetchURLWithOptions(ctx context.Context, rawURL string, maxBytes int, options researchOptions) (map[string]any, error) {
	parsed, err := options.validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	page, err := fetchDocument(ctx, parsed, maxBytes, options)
	if err != nil {
		return nil, err
	}
	// Inline body is bounded at the source: a full page in context dominated
	// measured research cost. Callers that need more text pass content_chars.
	inlineLimit := options.contentChars
	if inlineLimit <= 0 {
		inlineLimit = 4000
	}
	inlineContent := page.content
	contentTruncated := false
	if len(inlineContent) > inlineLimit {
		inlineContent = inlineContent[:inlineLimit]
		contentTruncated = true
	}
	return map[string]any{
		"url": parsed.String(), "final_url": page.finalURL, "redirect_chain": page.redirectChain,
		"status": page.status, "content_type": page.contentType,
		"title": extractHTMLTitle(page.content), "text_preview": compactText(stripHTML(page.content), 1200),
		"content": inlineContent, "content_truncated": contentTruncated, "content_chars": inlineLimit,
		"bytes_read": page.bytesRead, "truncated": page.truncated,
		"fetched_at": page.fetchedAt, "content_sha256": textSHA256(page.content),
		"cache_hit": false, "cache_provenance": "network", "freshness": "live_fetch",
		"etag": page.etag, "last_modified": page.lastModified, "response_date": page.responseDate,
		"is_error": page.status < 200 || page.status >= 300,
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

func browserSnapshot(rawURL string, allowedHosts []string, authorizedScope bool, waitMS, timeoutSeconds, maxDOMChars int, screenshot bool, viewportWidth, viewportHeight int) (map[string]any, error) {
	if !authorizedScope {
		return nil, fmt.Errorf("authorized_scope must be true for browser automation")
	}
	parsed, err := validateHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !hostAllowed(parsed.Hostname(), allowedHosts) {
		return nil, fmt.Errorf("url host %q is not in allowed_hosts", parsed.Hostname())
	}
	browser, err := resolveBrowserPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	capture, screenshotPath, runErr := captureBrowserSnapshot(ctx, browser, parsed.String(), allowedHosts, waitMS, screenshot, viewportWidth, viewportHeight)
	if runErr != nil {
		return nil, runErr
	}
	analysis := analyzeBrowserDOM(parsed, capture.DOM, maxDOMChars)
	analysis["viewport"] = map[string]int{"width": viewportWidth, "height": viewportHeight}
	analysis["layout_metrics"] = capture.Layout
	analysis["motion"] = capture.Motion
	analysis["final_url"] = capture.URL
	analysis["url"] = parsed.String()
	analysis["browser"] = filepath.Base(browser)
	analysis["authorized_scope"] = true
	analysis["allowed_hosts"] = normalizeAllowedHosts(allowedHosts)
	analysis["wait_ms"] = waitMS
	if screenshot && screenshotPath != "" {
		if info, err := os.Stat(screenshotPath); err == nil {
			analysis["screenshot"] = map[string]any{
				"path":   screenshotPath,
				"sha256": fileSHA256(screenshotPath),
				"bytes":  info.Size(),
			}
		}
	}
	return analysis, nil
}

type browserProbeCandidate struct {
	Method string
	Source string
	URL    string
	Param  string
}

func browserProbePoints(rawURL string, allowedHosts []string, authorizedScope bool, activeApproved bool, probeTypes []string, waitMS, timeoutSeconds, maxDOMChars, maxProbes int) (map[string]any, error) {
	if !authorizedScope {
		return nil, fmt.Errorf("authorized_scope must be true for browser automation")
	}
	parsed, err := validateHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !hostAllowed(parsed.Hostname(), allowedHosts) {
		return nil, fmt.Errorf("url host %q is not in allowed_hosts", parsed.Hostname())
	}
	browser, err := resolveBrowserPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	dom, _, runErr := runHeadlessBrowserSnapshot(ctx, browser, parsed.String(), waitMS, false)
	if runErr != nil {
		return nil, runErr
	}
	analysis := analyzeBrowserDOM(parsed, dom, maxDOMChars)
	candidates := buildBrowserProbeCandidates(parsed, dom, maxProbes)
	payload := map[string]any{
		"url":                   parsed.String(),
		"browser":               filepath.Base(browser),
		"authorized_scope":      true,
		"allowed_hosts":         normalizeAllowedHosts(allowedHosts),
		"active_probe_approved": activeApproved,
		"wait_ms":               waitMS,
		"surface_summary": map[string]any{
			"title":            analysis["title"],
			"dom_sha256":       analysis["dom_sha256"],
			"dom_bytes":        analysis["dom_bytes"],
			"query_parameters": analysis["query_parameters"],
			"form_count":       analysis["form_count"],
			"forms":            analysis["forms"],
			"scripts":          analysis["scripts"],
			"security_signals": analysis["security_signals"],
		},
		"probe_candidates": candidateSummaries(candidates),
		"rate_limit": map[string]any{
			"max_candidates":      hardMaxProbeCandidates,
			"max_requests":        hardMaxProbeCandidates * 2,
			"minimum_interval_ms": activeProbeInterval.Milliseconds(),
		},
		"token_note": "探测结果保持精简；主动探测只返回金丝雀反射、错误特征、DOM 哈希和预览",
	}
	if !activeApproved {
		payload["active_probe_count"] = 0
		payload["active_probe_skipped"] = "运行 GET 金丝雀探测前，active_probe_approved 必须为 true"
		return payload, nil
	}
	results := runBrowserCanaryProbes(browser, candidates, normalizeProbeTypes(probeTypes), allowedHosts, waitMS, timeoutSeconds, activeProbeInterval)
	payload["active_probe_count"] = len(results)
	payload["active_probe_results"] = results
	return payload, nil
}

func runHeadlessBrowserSnapshot(ctx context.Context, browser, targetURL string, waitMS int, screenshot bool) (string, string, error) {
	profileDir, err := os.MkdirTemp("", "goflow-browser-profile-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(profileDir)
	evidenceDir := browserEvidenceDir()
	screenshotPath := ""
	if screenshot {
		if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
			return "", "", fmt.Errorf("create browser evidence directory: %w", err)
		}
		screenshotPath = filepath.Join(evidenceDir, fmt.Sprintf("browser-%d.png", time.Now().UnixNano()))
	}
	var failures []string
	for _, headlessFlag := range []string{"--headless=new", "--headless"} {
		args := []string{
			headlessFlag,
			"--disable-gpu",
			"--disable-dev-shm-usage",
			"--disable-extensions",
			"--disable-background-networking",
			"--no-first-run",
			"--no-default-browser-check",
			"--no-sandbox",
			"--window-size=1365,768",
			"--user-data-dir=" + profileDir,
			fmt.Sprintf("--virtual-time-budget=%d", waitMS),
			"--dump-dom",
		}
		if screenshotPath != "" {
			args = append(args, "--screenshot="+screenshotPath)
		}
		args = append(args, targetURL)
		cmd := exec.CommandContext(ctx, browser, args...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			return stdout.String(), screenshotPath, nil
		}
		detail := strings.TrimSpace(strings.Join([]string{stdout.String(), stderr.String()}, "\n"))
		failures = append(failures, fmt.Sprintf("%s: %v: %s", headlessFlag, err, compactText(detail, 1000)))
	}
	return "", "", fmt.Errorf("headless browser snapshot failed: %s", strings.Join(failures, " | "))
}

func browserEvidenceDir() string {
	root := strings.TrimSpace(os.Getenv("GOFLOW_WORKSPACE_ROOT"))
	if root == "" {
		return filepath.Join(os.TempDir(), "goflow-browser-evidence")
	}
	return filepath.Join(root, ".goflow", "browser-evidence")
}

func resolveBrowserPath() (string, error) {
	candidates := browserPathCandidates()
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no Chrome, Edge, or Chromium browser found; set GOFLOW_BROWSER_PATH or install a supported browser")
}

func browserPathCandidates() []string {
	candidates := []string{}
	if env := strings.TrimSpace(os.Getenv("GOFLOW_BROWSER_PATH")); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "msedge", "microsoft-edge")
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
		if strings.TrimSpace(base) == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "Chromium", "Application", "chrome.exe"),
		)
	}
	return candidates
}

func analyzeBrowserDOM(parsed *url.URL, dom string, maxDOMChars int) map[string]any {
	forms := extractFormSummaries(parsed, dom, 20)
	links := extractLinkSummaries(parsed, dom, 30)
	scripts := extractScriptSummaries(parsed, dom)
	signals := browserSecuritySignals(dom)
	queryParams := make([]string, 0)
	for key := range parsed.Query() {
		queryParams = append(queryParams, key)
	}
	return map[string]any{
		"title":            extractHTMLTitle(dom),
		"text_preview":     compactText(stripHTML(dom), 1200),
		"dom_preview":      compactText(dom, maxDOMChars),
		"dom_sha256":       textSHA256(dom),
		"dom_bytes":        len([]byte(dom)),
		"dom_truncated":    len([]rune(dom)) > maxDOMChars,
		"query_parameters": queryParams,
		"forms":            forms,
		"form_count":       len(forms),
		"links":            links,
		"link_count":       len(links),
		"scripts":          scripts,
		"security_signals": securitySignalsAsMaps(signals),
		"token_note":       "仅返回精简浏览器证据；需要时再单独加载完整截图或 DOM",
	}
}

type browserSignal struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
}

func browserSecuritySignals(dom string) []browserSignal {
	lower := strings.ToLower(dom)
	checks := []struct {
		name     string
		severity string
		needle   string
		evidence string
	}{
		{name: "dom-inner-html-sink", severity: "medium", needle: ".innerhtml", evidence: "JavaScript references innerHTML"},
		{name: "document-write-sink", severity: "medium", needle: "document.write", evidence: "JavaScript references document.write"},
		{name: "eval-like-execution", severity: "high", needle: "eval(", evidence: "JavaScript references eval("},
		{name: "location-hash-source", severity: "low", needle: "location.hash", evidence: "JavaScript references location.hash"},
		{name: "location-search-source", severity: "low", needle: "location.search", evidence: "JavaScript references location.search"},
		{name: "inline-event-handlers", severity: "low", needle: " onclick=", evidence: "DOM contains inline event handler attributes"},
		{name: "javascript-url", severity: "low", needle: "javascript:", evidence: "DOM contains javascript: URL references"},
	}
	out := make([]browserSignal, 0)
	for _, check := range checks {
		if strings.Contains(lower, check.needle) {
			out = append(out, browserSignal{Name: check.name, Severity: check.severity, Evidence: check.evidence})
		}
	}
	return out
}

func securitySignalsAsMaps(signals []browserSignal) []map[string]string {
	out := make([]map[string]string, 0, len(signals))
	for _, signal := range signals {
		out = append(out, map[string]string{"name": signal.Name, "severity": signal.Severity, "evidence": signal.Evidence})
	}
	return out
}

var (
	formRe      = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	inputNameRe = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)
	linkHrefRe  = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>`)
	attrValueRe = regexp.MustCompile(`(?is)\b([a-zA-Z0-9_-]+)=["']([^"']*)["']`)
	scriptTagRe = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
)

func extractFormSummaries(base *url.URL, dom string, limit int) []map[string]any {
	matches := formRe.FindAllStringSubmatch(dom, -1)
	out := make([]map[string]any, 0, minInt(len(matches), limit))
	for _, match := range matches {
		if len(out) >= limit || len(match) < 3 {
			break
		}
		attrs := parseHTMLAttrs(match[1])
		inputs := make([]map[string]string, 0)
		for _, inputMatch := range inputNameRe.FindAllStringSubmatch(match[2], -1) {
			if len(inputMatch) < 3 {
				continue
			}
			inputAttrs := parseHTMLAttrs(inputMatch[2])
			name := inputAttrs["name"]
			if name == "" {
				name = inputAttrs["id"]
			}
			if name == "" {
				continue
			}
			inputType := inputAttrs["type"]
			if inputType == "" {
				inputType = strings.ToLower(inputMatch[1])
			}
			inputs = append(inputs, map[string]string{"name": name, "type": inputType})
		}
		action := strings.TrimSpace(attrs["action"])
		if action != "" {
			action = resolveAssetURL(base, action)
		}
		method := strings.ToUpper(strings.TrimSpace(attrs["method"]))
		if method == "" {
			method = "GET"
		}
		out = append(out, map[string]any{
			"method": method,
			"action": action,
			"id":     attrs["id"],
			"name":   attrs["name"],
			"inputs": inputs,
		})
	}
	return out
}

func extractLinkSummaries(base *url.URL, dom string, limit int) []map[string]string {
	matches := linkHrefRe.FindAllStringSubmatch(dom, -1)
	out := make([]map[string]string, 0, minInt(len(matches), limit))
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(out) >= limit || len(match) < 2 {
			break
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		resolved := resolveAssetURL(base, href)
		if resolved == "" {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		kind := "same-origin"
		if parsed, err := url.Parse(resolved); err == nil && !strings.EqualFold(parsed.Hostname(), base.Hostname()) {
			kind = "external"
		}
		out = append(out, map[string]string{"url": resolved, "kind": kind})
	}
	return out
}

func extractScriptSummaries(base *url.URL, dom string) map[string]any {
	refs := extractAssetRefs(base, dom, false)
	scripts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Type == "script" {
			scripts = append(scripts, ref.URL)
		}
	}
	return map[string]any{
		"external_count": len(scripts),
		"external":       scripts,
		"inline_count":   countInlineScriptTags(dom),
	}
}

func countInlineScriptTags(dom string) int {
	count := 0
	for _, match := range scriptTagRe.FindAllStringSubmatch(dom, -1) {
		if len(match) < 2 {
			continue
		}
		attrs := parseHTMLAttrs(match[1])
		if strings.TrimSpace(attrs["src"]) == "" {
			count++
		}
	}
	return count
}

func parseHTMLAttrs(raw string) map[string]string {
	out := make(map[string]string)
	for _, match := range attrValueRe.FindAllStringSubmatch(raw, -1) {
		if len(match) >= 3 {
			out[strings.ToLower(match[1])] = html.UnescapeString(match[2])
		}
	}
	return out
}

func hostAllowed(host string, allowedHosts []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, allowed := range normalizeAllowedHosts(allowedHosts) {
		if allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowed, "*")) {
			return true
		}
	}
	return false
}

func normalizeAllowedHosts(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "http://")
		value = strings.TrimPrefix(value, "https://")
		if strings.Contains(value, "/") {
			value = strings.Split(value, "/")[0]
		}
		if host, _, ok := strings.Cut(value, ":"); ok {
			value = host
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func buildBrowserProbeCandidates(base *url.URL, dom string, limit int) []browserProbeCandidate {
	if limit <= 0 {
		return nil
	}
	out := make([]browserProbeCandidate, 0, limit)
	seen := make(map[string]struct{})
	add := func(method, source, rawURL, param string) {
		if len(out) >= limit {
			return
		}
		param = strings.TrimSpace(param)
		if param == "" {
			return
		}
		parsed, err := validateHTTPURL(rawURL)
		if err != nil {
			return
		}
		key := strings.ToUpper(method) + "|" + source + "|" + parsed.String() + "|" + param
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, browserProbeCandidate{Method: strings.ToUpper(method), Source: source, URL: parsed.String(), Param: param})
	}
	for _, key := range sortedQueryKeys(base.Query()) {
		add("GET", "url_query", base.String(), key)
	}
	for _, form := range extractFormSummaries(base, dom, 20) {
		method, _ := form["method"].(string)
		if !strings.EqualFold(method, "GET") {
			continue
		}
		action, _ := form["action"].(string)
		if strings.TrimSpace(action) == "" {
			action = base.String()
		}
		inputs, _ := form["inputs"].([]map[string]string)
		for _, input := range inputs {
			add("GET", "get_form", action, input["name"])
		}
	}
	for _, link := range extractLinkSummaries(base, dom, 30) {
		if len(out) >= limit {
			break
		}
		if link["kind"] != "same-origin" {
			continue
		}
		linked, err := validateHTTPURL(link["url"])
		if err != nil {
			continue
		}
		for _, key := range sortedQueryKeys(linked.Query()) {
			add("GET", "same_origin_link", linked.String(), key)
		}
	}
	return out
}

func candidateSummaries(candidates []browserProbeCandidate) []map[string]string {
	out := make([]map[string]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, map[string]string{
			"method": candidate.Method,
			"source": candidate.Source,
			"url":    candidate.URL,
			"param":  candidate.Param,
		})
	}
	return out
}

func runBrowserCanaryProbes(browser string, candidates []browserProbeCandidate, probeTypes []string, allowedHosts []string, waitMS, timeoutSeconds int, interval time.Duration) []map[string]any {
	results := make([]map[string]any, 0, len(candidates)*len(probeTypes))
	firstRequest := true
	for _, candidate := range candidates {
		parsed, err := validateHTTPURL(candidate.URL)
		if err != nil || !hostAllowed(parsed.Hostname(), allowedHosts) {
			continue
		}
		for _, probeType := range probeTypes {
			if !firstRequest && interval > 0 {
				time.Sleep(interval)
			}
			firstRequest = false
			canary := canaryValue(probeType, candidate.Param)
			probeURL := probeURLWithValue(candidate.URL, candidate.Param, canary)
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
			dom, _, runErr := runHeadlessBrowserSnapshot(ctx, browser, probeURL, waitMS, false)
			cancel()
			item := map[string]any{
				"type":   probeType,
				"method": candidate.Method,
				"source": candidate.Source,
				"url":    compactProbeURL(probeURL),
				"param":  candidate.Param,
			}
			if runErr != nil {
				item["is_error"] = true
				item["error"] = compactText(runErr.Error(), 300)
				results = append(results, item)
				continue
			}
			item["is_error"] = false
			item["dom_sha256"] = textSHA256(dom)
			item["dom_bytes"] = len([]byte(dom))
			switch probeType {
			case "reflection":
				item["canary_reflected"] = strings.Contains(dom, canary)
				if preview := evidencePreview(dom, canary, 320); preview != "" {
					item["evidence_preview"] = preview
				}
			case "sql_error":
				signals := sqlErrorSignals(dom)
				item["error_signal_count"] = len(signals)
				item["error_signals"] = signals
			}
			results = append(results, item)
		}
	}
	return results
}

func normalizeProbeTypes(values []string) []string {
	if len(values) == 0 {
		return []string{"reflection"}
	}
	allowed := map[string]struct{}{
		"reflection": {},
		"sql_error":  {},
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{"reflection"}
	}
	return out
}

func canaryValue(probeType, seed string) string {
	key := textSHA256(probeType + ":" + seed)
	if len(key) > 12 {
		key = key[:12]
	}
	switch probeType {
	case "sql_error":
		return "goflow-sql-" + key + "'"
	default:
		return "goflow-ref-" + key
	}
}

func probeURLWithValue(rawURL, param, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	values := parsed.Query()
	values.Set(param, value)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func compactProbeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return compactText(rawURL, 360)
	}
	values := parsed.Query()
	for key := range values {
		values.Set(key, "<canary>")
	}
	parsed.RawQuery = values.Encode()
	return compactText(parsed.String(), 360)
}

func evidencePreview(text, needle string, limit int) string {
	index := strings.Index(text, needle)
	if index < 0 {
		return ""
	}
	start := index - 160
	if start < 0 {
		start = 0
	}
	end := index + len(needle) + 160
	if end > len(text) {
		end = len(text)
	}
	return compactText(stripHTML(text[start:end]), limit)
}

func sqlErrorSignals(dom string) []string {
	lower := strings.ToLower(dom)
	checks := []struct {
		name   string
		needle string
	}{
		{name: "sql-syntax-error", needle: "sql syntax"},
		{name: "mysql-error", needle: "mysql"},
		{name: "postgresql-error", needle: "postgresql"},
		{name: "sqlite-error", needle: "sqlite"},
		{name: "odbc-error", needle: "odbc"},
		{name: "sqlstate-error", needle: "sqlstate"},
		{name: "unclosed-quotation", needle: "unclosed quotation"},
		{name: "generic-syntax-error", needle: "syntax error"},
	}
	out := make([]string, 0)
	for _, check := range checks {
		if strings.Contains(lower, check.needle) {
			out = append(out, check.name)
		}
	}
	return out
}

func sortedQueryKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func textSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:])
}

func fileSHA256(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type fetchedPage struct {
	status        int
	contentType   string
	content       string
	bytesRead     int
	truncated     bool
	etag          string
	lastModified  string
	responseDate  string
	finalURL      string
	fetchedAt     string
	redirectChain []string
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
	return fetchedPage{
		status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), content: content,
		bytesRead: len(body), truncated: truncated, etag: resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"), responseDate: resp.Header.Get("Date"),
	}, nil
}

type researchSnapshot struct {
	CacheVersion    int      `json:"cache_version"`
	URL             string   `json:"url"`
	CanonicalURL    string   `json:"canonical_url"`
	FinalURL        string   `json:"final_url"`
	RedirectChain   []string `json:"redirect_chain,omitempty"`
	CacheProvenance string   `json:"cache_provenance"`
	Freshness       string   `json:"freshness"`
	MaxBytes        int      `json:"max_bytes"`
	Domain          string   `json:"domain"`
	Status          int      `json:"status"`
	ContentType     string   `json:"content_type,omitempty"`
	Title           string   `json:"title,omitempty"`
	Text            string   `json:"text,omitempty"`
	Excerpt         string   `json:"excerpt,omitempty"`
	ContentSHA256   string   `json:"content_sha256,omitempty"`
	FetchedAt       string   `json:"fetched_at"`
	PublishedAt     string   `json:"published_at,omitempty"`
	LastModified    string   `json:"last_modified,omitempty"`
	ResponseDate    string   `json:"response_date,omitempty"`
	ETag            string   `json:"etag,omitempty"`
	BytesRead       int      `json:"bytes_read,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
	CacheHit        bool     `json:"cache_hit"`
	DuplicateOf     string   `json:"duplicate_of,omitempty"`
	AttemptStatuses []int    `json:"attempt_statuses,omitempty"`
	IsError         bool     `json:"is_error"`
	Error           string   `json:"error,omitempty"`
}

func researchSnapshots(rawURLs []string, maxBytes int, cacheTTL time.Duration) (map[string]any, error) {
	return researchSnapshotsWithOptions(context.Background(), rawURLs, maxBytes, cacheTTL, researchOptions{})
}

func researchSnapshotsWithOptions(ctx context.Context, rawURLs []string, maxBytes int, cacheTTL time.Duration, options researchOptions) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if len(rawURLs) > hardMaxResearchURLs {
		return nil, fmt.Errorf("at most %d research URLs are supported", hardMaxResearchURLs)
	}
	maxBytes = minInt(maxBytes, 256*1024)
	if maxBytes <= 0 {
		maxBytes = 128 * 1024
	}
	if len(rawURLs) == 0 {
		return nil, fmt.Errorf("至少需要一个来源 URL")
	}
	canonical := make([]string, 0, len(rawURLs))
	seen := make(map[string]struct{}, len(rawURLs))
	for _, raw := range rawURLs {
		parsed, err := validateHTTPURL(raw)
		value := raw
		if err == nil {
			value = canonicalResearchURL(parsed)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		canonical = append(canonical, value)
	}
	if len(canonical) == 0 {
		return nil, fmt.Errorf("没有可获取的 HTTP(S) 来源 URL")
	}

	type indexedSnapshot struct {
		index int
		item  researchSnapshot
	}
	jobs := make(chan indexedSnapshot)
	results := make(chan indexedSnapshot, len(canonical))
	workers := minInt(4, len(canonical))
	for worker := 0; worker < workers; worker++ {
		go func() {
			for job := range jobs {
				job.item = loadResearchSnapshotWithOptions(ctx, job.item.URL, maxBytes, cacheTTL, options)
				results <- job
			}
		}()
	}
	go func() {
		for index, raw := range canonical {
			jobs <- indexedSnapshot{index: index, item: researchSnapshot{URL: raw}}
		}
		close(jobs)
	}()

	items := make([]researchSnapshot, len(canonical))
	for range canonical {
		result := <-results
		items[result.index] = result.item
	}
	contentOwners := make(map[string]string)
	cacheHits := 0
	errorCount := 0
	for index := range items {
		if items[index].IsError {
			errorCount++
			continue
		}
		if items[index].CacheHit {
			cacheHits++
		}
		if items[index].ContentSHA256 == "" {
			continue
		}
		if owner := contentOwners[items[index].ContentSHA256]; owner != "" {
			items[index].DuplicateOf = owner
			continue
		}
		contentOwners[items[index].ContentSHA256] = items[index].CanonicalURL
	}
	return map[string]any{
		"requested_count": len(rawURLs), "unique_url_count": len(canonical),
		"unique_content_count": len(contentOwners), "cache_hit_count": cacheHits,
		"snapshots": items, "error_count": errorCount, "success_count": len(items) - errorCount,
	}, nil
}

func loadResearchSnapshot(rawURL string, maxBytes int, cacheTTL time.Duration) researchSnapshot {
	return loadResearchSnapshotWithOptions(context.Background(), rawURL, maxBytes, cacheTTL, researchOptions{})
}

func loadResearchSnapshotWithOptions(ctx context.Context, rawURL string, maxBytes int, cacheTTL time.Duration, options researchOptions) researchSnapshot {
	item := researchSnapshot{URL: rawURL, IsError: true, CacheProvenance: "none", Freshness: "unknown"}
	if err := ctx.Err(); err != nil {
		item.Error = err.Error()
		return item
	}
	parsed, err := options.validateURL(rawURL)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	canonical := canonicalResearchURL(parsed)
	item.CanonicalURL = canonical
	item.Domain = strings.ToLower(parsed.Hostname())
	cachePath := researchCachePath(canonical)
	if cacheTTL > 0 && !options.refresh {
		if cached, ok := readResearchSnapshotCache(cachePath, cacheTTL); ok && cached.CanonicalURL == canonical && cached.MaxBytes == maxBytes && cached.FinalURL != "" && len(cached.RedirectChain) > 0 {
			allowed := true
			for _, raw := range append(append([]string{}, cached.RedirectChain...), cached.FinalURL) {
				if _, err := options.validateURL(raw); err != nil {
					allowed = false
					break
				}
			}
			if allowed {
				cached.CacheHit = true
				cached.CacheProvenance = "cache"
				cached.Freshness = "cached_unvalidated"
				return cached
			}
		}
	}
	page, err := fetchDocument(ctx, parsed, maxBytes, options)
	if err != nil {
		item.Status, item.FinalURL, item.FetchedAt = page.status, page.finalURL, page.fetchedAt
		item.RedirectChain = page.redirectChain
		if page.status != 0 {
			item.AttemptStatuses = []int{page.status}
			item.CacheProvenance = "network"
		}
		item.Error = err.Error()
		return item
	}
	attempts := []int{page.status}
	if page.status == http.StatusServiceUnavailable {
		retry, retryErr := fetchDocument(ctx, parsed, maxBytes, options)
		if retryErr != nil {
			item.Status, item.FinalURL, item.FetchedAt = page.status, page.finalURL, page.fetchedAt
			item.RedirectChain, item.AttemptStatuses = page.redirectChain, attempts
			item.CacheProvenance = "network"
			item.Error = retryErr.Error()
			return item
		}
		page = retry
		attempts = append(attempts, page.status)
	}
	plainText := compactText(stripHTML(page.content), defaultResearchTextChars)
	final, _ := validateHTTPURL(page.finalURL)
	item = researchSnapshot{
		CacheVersion: researchSnapshotCacheVersion,
		URL:          rawURL, CanonicalURL: canonical, FinalURL: page.finalURL, RedirectChain: page.redirectChain,
		Domain: strings.ToLower(final.Hostname()), Status: page.status,
		ContentType: page.contentType, Title: extractHTMLTitle(page.content), Text: plainText,
		Excerpt: compactText(plainText, 1200), ContentSHA256: textSHA256(page.content),
		FetchedAt: page.fetchedAt, LastModified: page.lastModified,
		ResponseDate: page.responseDate, ETag: page.etag, BytesRead: page.bytesRead, Truncated: page.truncated,
		AttemptStatuses: attempts, CacheProvenance: "network", Freshness: "live_fetch", MaxBytes: maxBytes,
	}
	if page.status < http.StatusOK || page.status >= http.StatusMultipleChoices {
		item.IsError = true
		item.Error = fmt.Sprintf("来源返回 HTTP %d，未写入研究缓存", page.status)
		return item
	}
	_ = writeResearchSnapshotCache(cachePath, item)
	return item
}

func canonicalResearchURL(parsed *url.URL) string {
	clone := *parsed
	clone.Fragment = ""
	clone.Scheme = strings.ToLower(clone.Scheme)
	clone.Host = strings.ToLower(clone.Host)
	clone.RawQuery = clone.Query().Encode()
	return clone.String()
}

func researchCachePath(canonical string) string {
	root := strings.TrimSpace(os.Getenv("GOFLOW_RESEARCH_CACHE_DIR"))
	if root == "" {
		if cacheRoot, err := os.UserCacheDir(); err == nil {
			root = filepath.Join(cacheRoot, "goflow", "web-research")
		} else {
			root = filepath.Join(os.TempDir(), "goflow-web-research")
		}
	}
	return filepath.Join(root, textSHA256(canonical)+".json")
}

func readResearchSnapshotCache(path string, ttl time.Duration) (researchSnapshot, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return researchSnapshot{}, false
	}
	var item researchSnapshot
	if json.Unmarshal(data, &item) != nil || item.CacheVersion != researchSnapshotCacheVersion || item.IsError || item.Status < 200 || item.Status >= 300 {
		return researchSnapshot{}, false
	}
	fetched, err := time.Parse(time.RFC3339, item.FetchedAt)
	if err != nil || time.Since(fetched) < 0 || time.Since(fetched) > ttl {
		return researchSnapshot{}, false
	}
	return item, true
}

func writeResearchSnapshotCache(path string, item researchSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func compileResearchEvidence(topic string, claims, sources []map[string]any, staleDays int) (map[string]any, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" || len(claims) == 0 || len(sources) == 0 {
		return nil, fmt.Errorf("研究主题、结论和来源不能为空")
	}
	normalizedSources := normalizeResearchSources(sources, staleDays)
	sourceByID := make(map[string]map[string]any, len(normalizedSources))
	sourceByURL := make(map[string]string, len(normalizedSources))
	for _, source := range normalizedSources {
		id := stringValue(source["id"])
		sourceByID[id] = source
		sourceByURL[stringValue(source["canonical_url"])] = id
	}

	normalizedClaims := make([]map[string]any, 0, len(claims))
	unsupported, conflicted, staleClaims := 0, 0, 0
	for index, claim := range claims {
		id := stringValue(claim["id"])
		if id == "" {
			id = fmt.Sprintf("C%03d", index+1)
		}
		text := strings.TrimSpace(stringValue(claim["text"]))
		if text == "" {
			continue
		}
		refs := uniqueStrings(stringListValue(claim["source_ids"]))
		for _, raw := range stringListValue(claim["source_urls"]) {
			if parsed, err := validateHTTPURL(raw); err == nil {
				if sourceID := sourceByURL[canonicalResearchURL(parsed)]; sourceID != "" {
					refs = append(refs, sourceID)
				}
			}
		}
		refs = uniqueStrings(refs)
		validRefs := make([]string, 0, len(refs))
		domains := make(map[string]struct{})
		allStale := len(refs) > 0
		contradictions := make([]string, 0)
		for _, ref := range refs {
			source := sourceByID[ref]
			if source == nil {
				continue
			}
			validRefs = append(validRefs, ref)
			if domain := stringValue(source["domain"]); domain != "" {
				domains[domain] = struct{}{}
			}
			if stale, _ := source["stale"].(bool); !stale {
				allStale = false
			}
			if containsString(stringListValue(source["contradicts"]), id) {
				contradictions = append(contradictions, ref)
			}
		}
		status := "已支持"
		if len(validRefs) == 0 {
			status = "缺少来源"
			unsupported++
		} else if len(contradictions) > 0 {
			status = "存在冲突"
			conflicted++
		} else if allStale {
			status = "来源可能过期"
			staleClaims++
		}
		importance := normalizeResearchImportance(stringValue(claim["importance"]))
		normalizedClaims = append(normalizedClaims, map[string]any{
			"id": id, "text": text, "importance": importance, "confidence": stringValue(claim["confidence"]),
			"source_ids": validRefs, "independent_domain_count": len(domains), "contradicting_source_ids": contradictions,
			"status": status, "notes": stringValue(claim["notes"]),
		})
	}
	if len(normalizedClaims) == 0 {
		return nil, fmt.Errorf("没有有效的研究结论")
	}

	reviewStatus := "通过"
	for _, claim := range normalizedClaims {
		importance := stringValue(claim["importance"])
		status := stringValue(claim["status"])
		if (importance == "key" || importance == "关键") && (status == "缺少来源" || status == "存在冲突") {
			reviewStatus = "不通过"
			break
		}
		if status != "已支持" || numberValue(claim["independent_domain_count"]) < 2 {
			reviewStatus = "需复核"
		}
	}

	compiled := map[string]any{
		"topic": topic, "generated_at": time.Now().UTC().Format(time.RFC3339), "review_status": reviewStatus,
		"claims": normalizedClaims, "sources": normalizedSources,
		"summary": map[string]any{"claim_count": len(normalizedClaims), "source_count": len(normalizedSources), "unsupported_claims": unsupported, "conflicted_claims": conflicted, "stale_claims": staleClaims},
	}
	jsonBytes, _ := json.MarshalIndent(compiled, "", "  ")
	markdown := researchMarkdown(topic, reviewStatus, normalizedClaims, normalizedSources)
	csvText := researchCSV(normalizedClaims, sourceByID)
	return map[string]any{
		"review_status": reviewStatus, "summary": compiled["summary"], "claims": normalizedClaims, "sources": normalizedSources,
		"markdown": markdown, "json": string(jsonBytes), "csv": csvText,
	}, nil
}

func normalizeResearchImportance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "key", "关键", "high", "高":
		return "key"
	case "normal", "一般", "medium", "low", "中", "低", "":
		return "normal"
	default:
		return "normal"
	}
}

func normalizeResearchSources(sources []map[string]any, staleDays int) []map[string]any {
	out := make([]map[string]any, 0, len(sources))
	seenURL := make(map[string]struct{})
	seenHash := make(map[string]struct{})
	for _, source := range sources {
		if source["is_error"] == true || (numberValue(source["status"]) != 0 && (numberValue(source["status"]) < 200 || numberValue(source["status"]) >= 300)) {
			continue
		}
		rawURL := stringValue(source["url"])
		parsed, err := validateHTTPURL(rawURL)
		if err != nil {
			continue
		}
		canonical := canonicalResearchURL(parsed)
		hash := strings.TrimSpace(stringValue(source["content_sha256"]))
		if _, exists := seenURL[canonical]; exists {
			continue
		}
		if hash != "" {
			if _, exists := seenHash[hash]; exists {
				continue
			}
			seenHash[hash] = struct{}{}
		}
		seenURL[canonical] = struct{}{}
		id := strings.TrimSpace(stringValue(source["id"]))
		if id == "" {
			id = fmt.Sprintf("S%03d", len(out)+1)
		}
		domain := strings.ToLower(parsed.Hostname())
		finalURL := stringValue(source["final_url"])
		if final, err := validateHTTPURL(finalURL); err == nil {
			domain = strings.ToLower(final.Hostname())
		}
		freshness := "unknown"
		if hit, _ := source["cache_hit"].(bool); hit || source["cache_provenance"] == "cache" {
			freshness = "cached_unvalidated"
		} else if source["cache_provenance"] == "network" && stringValue(source["fetched_at"]) != "" {
			freshness = "live_fetch"
		}
		publishedAt := stringValue(source["published_at"])
		stale := sourceIsStale(publishedAt, staleDays)
		sourceType := strings.ToLower(strings.TrimSpace(stringValue(source["source_type"])))
		out = append(out, map[string]any{
			"id": id, "url": rawURL, "canonical_url": canonical, "title": stringValue(source["title"]),
			"domain": domain, "published_at": publishedAt, "fetched_at": stringValue(source["fetched_at"]),
			"last_modified": stringValue(source["last_modified"]),
			"source_type":   sourceType, "credibility_level": researchCredibility(sourceType), "stale": stale,
			"excerpt": stringValue(source["excerpt"]), "content_sha256": hash,
			"final_url": finalURL, "status": numberValue(source["status"]),
			"cache_hit": source["cache_hit"] == true, "cache_provenance": stringValue(source["cache_provenance"]), "freshness": freshness,
			"redirect_chain": stringListValue(source["redirect_chain"]), "bytes_read": numberValue(source["bytes_read"]), "truncated": source["truncated"] == true,
			"supports": uniqueStrings(stringListValue(source["supports"])), "contradicts": uniqueStrings(stringListValue(source["contradicts"])),
		})
	}
	return out
}

func researchCredibility(sourceType string) string {
	switch sourceType {
	case "official", "government", "standard", "academic", "官方", "政府", "标准", "学术":
		return "高"
	case "vendor", "news", "industry", "厂商", "新闻", "行业":
		return "中"
	default:
		return "待核验"
	}
}

func sourceIsStale(raw string, staleDays int) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339, time.RFC1123, time.RFC1123Z, "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return time.Since(parsed) > time.Duration(staleDays)*24*time.Hour
		}
	}
	return false
}

func researchMarkdown(topic, status string, claims, sources []map[string]any) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n## 研究结论\n\n", topic)
	for _, claim := range claims {
		fmt.Fprintf(&builder, "- **%s** %s（%s；来源：%s）\n", stringValue(claim["id"]), stringValue(claim["text"]), stringValue(claim["status"]), strings.Join(stringListValue(claim["source_ids"]), "、"))
	}
	fmt.Fprintf(&builder, "\n## 来源清单\n\n")
	for _, source := range sources {
		title := stringValue(source["title"])
		if title == "" {
			title = stringValue(source["domain"])
		}
		fmt.Fprintf(&builder, "- [%s] %s：[%s](%s)，可信度：%s，时效状态：%s\n", stringValue(source["id"]), title, stringValue(source["url"]), stringValue(source["url"]), stringValue(source["credibility_level"]), map[bool]string{true: "可能过期", false: "可用"}[source["stale"] == true])
		fmt.Fprintf(&builder, "  获取时间：%s；最终 URL：%s；HTTP：%d；SHA256：%s；缓存来源：%s；获取状态：%s；HTTP Last-Modified：%s\n", stringValue(source["fetched_at"]), stringValue(source["final_url"]), numberValue(source["status"]), stringValue(source["content_sha256"]), stringValue(source["cache_provenance"]), stringValue(source["freshness"]), stringValue(source["last_modified"]))
	}
	fmt.Fprintf(&builder, "\n## 质量结论\n\n%s。关键结论必须保留来源映射；冲突、过期和单一来源限制不得隐藏。\n", status)
	return builder.String()
}

func researchCSV(claims []map[string]any, sourceByID map[string]map[string]any) string {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"结论编号", "结论", "结论状态", "来源编号", "来源标题", "来源 URL", "来源域名", "可信度", "发布时间", "获取时间", "最终 URL", "HTTP 状态", "SHA256", "缓存来源", "获取状态", "HTTP Last-Modified"})
	for _, claim := range claims {
		refs := stringListValue(claim["source_ids"])
		if len(refs) == 0 {
			_ = writer.Write([]string{stringValue(claim["id"]), stringValue(claim["text"]), stringValue(claim["status"]), "", "", "", "", "", "", "", "", "", "", "", "", ""})
			continue
		}
		for _, ref := range refs {
			source := sourceByID[ref]
			_ = writer.Write([]string{stringValue(claim["id"]), stringValue(claim["text"]), stringValue(claim["status"]), ref, stringValue(source["title"]), stringValue(source["url"]), stringValue(source["domain"]), stringValue(source["credibility_level"]), stringValue(source["published_at"]), stringValue(source["fetched_at"]), stringValue(source["final_url"]), strconv.Itoa(numberValue(source["status"])), stringValue(source["content_sha256"]), stringValue(source["cache_provenance"]), stringValue(source["freshness"]), stringValue(source["last_modified"])})
		}
	}
	writer.Flush()
	return buffer.String()
}

func optionalObjectList(args map[string]any, key string, maximum int) []map[string]any {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(map[string]any); ok {
			out = append(out, value)
			if maximum > 0 && len(out) >= maximum {
				break
			}
		}
	}
	return out
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func stringListValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func numberValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
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
	return parseSearchResults(body, limit, researchOptions{})
}

func parseSearchResults(body string, limit int, options researchOptions) []map[string]any {
	matches := resultLinkRe.FindAllStringSubmatch(body, -1)
	results := make([]map[string]any, 0, limit)
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(results) >= limit {
			break
		}
		link := html.UnescapeString(match[1])
		title := compactText(stripHTML(html.UnescapeString(match[2])), 240)
		if title == "" {
			continue
		}
		if redirect, err := url.Parse(link); err == nil {
			if raw := redirect.Query().Get("uddg"); raw != "" {
				link = raw
			}
		}
		parsed, err := options.validateURL(link)
		if err != nil || parsed.Hostname() == "duckduckgo.com" || strings.HasSuffix(parsed.Hostname(), ".duckduckgo.com") {
			continue
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

func optionalStringList(args map[string]any, key string, maximum int) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if maximum > 0 && len(out) >= maximum {
			break
		}
	}
	return out
}

func write(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}
