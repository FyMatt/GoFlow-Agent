package main

import (
	"bufio"
	"context"
	"crypto/sha256"
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
		{
			Name:        "browser_snapshot",
			Description: "Open an authorized HTTP(S) target in a local headless browser and return a compact DOM, form, network-surface, signal, and optional screenshot evidence summary. This tool is passive by default and requires an explicit allowed host list.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20},"authorized_scope":{"type":"boolean"},"wait_ms":{"type":"integer","minimum":500,"maximum":10000},"timeout_seconds":{"type":"integer","minimum":2,"maximum":30},"max_dom_chars":{"type":"integer","minimum":500,"maximum":20000},"screenshot":{"type":"boolean"}},"required":["url","allowed_hosts","authorized_scope"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "browser_probe_points",
			Description: "Open an authorized HTTP(S) target in a local headless browser, enumerate compact XSS/SQLi-oriented probe surfaces, and optionally run a small approval-gated GET canary probe set. This tool never brute-forces, crawls broadly, or submits POST forms.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20},"authorized_scope":{"type":"boolean"},"active_probe_approved":{"type":"boolean"},"probe_types":{"type":"array","items":{"type":"string","enum":["reflection","sql_error"]},"maxItems":2},"wait_ms":{"type":"integer","minimum":500,"maximum":10000},"timeout_seconds":{"type":"integer","minimum":2,"maximum":30},"max_dom_chars":{"type":"integer","minimum":500,"maximum":20000},"max_probes":{"type":"integer","minimum":0,"maximum":20}},"required":["url","allowed_hosts","authorized_scope","active_probe_approved"],"additionalProperties":false}`),
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
	case "browser_snapshot":
		rawURL, _ := input.Arguments["url"].(string)
		allowedHosts := optionalStringList(input.Arguments, "allowed_hosts", 20)
		authorizedScope, _ := input.Arguments["authorized_scope"].(bool)
		waitMS := optionalInt(input.Arguments, "wait_ms", 2000, 500, 10000)
		timeoutSeconds := optionalInt(input.Arguments, "timeout_seconds", 15, 2, 30)
		maxDOMChars := optionalInt(input.Arguments, "max_dom_chars", 4000, 500, 20000)
		screenshot, _ := input.Arguments["screenshot"].(bool)
		payload, err = browserSnapshot(rawURL, allowedHosts, authorizedScope, waitMS, timeoutSeconds, maxDOMChars, screenshot)
	case "browser_probe_points":
		rawURL, _ := input.Arguments["url"].(string)
		allowedHosts := optionalStringList(input.Arguments, "allowed_hosts", 20)
		authorizedScope, _ := input.Arguments["authorized_scope"].(bool)
		activeApproved, _ := input.Arguments["active_probe_approved"].(bool)
		probeTypes := optionalStringList(input.Arguments, "probe_types", 2)
		waitMS := optionalInt(input.Arguments, "wait_ms", 2000, 500, 10000)
		timeoutSeconds := optionalInt(input.Arguments, "timeout_seconds", 15, 2, 30)
		maxDOMChars := optionalInt(input.Arguments, "max_dom_chars", 4000, 500, 20000)
		maxProbes := optionalInt(input.Arguments, "max_probes", 6, 0, 20)
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

func browserSnapshot(rawURL string, allowedHosts []string, authorizedScope bool, waitMS, timeoutSeconds, maxDOMChars int, screenshot bool) (map[string]any, error) {
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
	dom, screenshotPath, runErr := runHeadlessBrowserSnapshot(ctx, browser, parsed.String(), waitMS, screenshot)
	if runErr != nil {
		return nil, runErr
	}
	analysis := analyzeBrowserDOM(parsed, dom, maxDOMChars)
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
		"token_note":       "probe output is compact; active probes return canary reflections, error signatures, DOM hashes, and previews only",
	}
	if !activeApproved {
		payload["active_probe_count"] = 0
		payload["active_probe_skipped"] = "active_probe_approved must be true before GET canary probes run"
		return payload, nil
	}
	results := runBrowserCanaryProbes(browser, candidates, normalizeProbeTypes(probeTypes), allowedHosts, waitMS, timeoutSeconds)
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
		output, err := cmd.CombinedOutput()
		if err == nil {
			return string(output), screenshotPath, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v: %s", headlessFlag, err, compactText(string(output), 1000)))
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
		"token_note":       "compact browser evidence only; load full screenshots or DOM externally when needed",
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
	formRe       = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	inputNameRe  = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)
	linkHrefRe   = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>`)
	attrValueRe  = regexp.MustCompile(`(?is)\b([a-zA-Z0-9_-]+)=["']([^"']*)["']`)
	scriptTagRe  = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
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

func runBrowserCanaryProbes(browser string, candidates []browserProbeCandidate, probeTypes []string, allowedHosts []string, waitMS, timeoutSeconds int) []map[string]any {
	results := make([]map[string]any, 0, len(candidates)*len(probeTypes))
	for _, candidate := range candidates {
		parsed, err := validateHTTPURL(candidate.URL)
		if err != nil || !hostAllowed(parsed.Hostname(), allowedHosts) {
			continue
		}
		for _, probeType := range probeTypes {
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
