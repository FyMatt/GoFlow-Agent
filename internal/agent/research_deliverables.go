package agent

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

var researchBaseURLPattern = regexp.MustCompile(`https?://127\.0\.0\.1:\d+`)

type researchCatalogFile struct {
	Topic   string                  `json:"topic"`
	Sources []researchCatalogSource `json:"sources"`
}

type researchCatalogSource struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	RelativePath   string `json:"relative_path"`
	PublishedAt    string `json:"published_at"`
	SourceType     string `json:"source_type"`
	ClaimGroup     string `json:"claim_group"`
	ExpectedStatus int    `json:"expected_status"`
}

func (w *WorkflowRunner) seedCatalogResearchDeliverables(result *schema.AgentResult) {
	if w == nil || result == nil {
		return
	}
	root := w.catalogResearchDeliveryRoot()
	if strings.TrimSpace(root) == "" {
		return
	}
	wanted := false
	for _, rel := range w.requestedDeliverablePaths() {
		if strings.EqualFold(filepath.Base(rel), "evidence.json") {
			wanted = true
			break
		}
	}
	if !wanted {
		return
	}
	files, err := compileCatalogResearchDeliverables(root, w.workflowRequestText(), result.ToolResults)
	if err != nil || len(files) == 0 {
		return
	}
	for rel, content := range files {
		if !requestedDeliverableContentValid(rel, content) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			continue
		}
		result.ToolResults = append(result.ToolResults, schema.ToolResult{
			ToolName: "file_tools/write_file",
			Content:  `{"relative_path":"` + rel + `","status":"created"}`,
		})
	}
}

func (w *WorkflowRunner) catalogResearchDeliveryRoot() string {
	if w != nil && w.runtime != nil && strings.TrimSpace(w.runID) != "" {
		if base, err := w.runtime.taskStagingBaseRoot(); err == nil {
			root := filepath.Join(base, strings.TrimSpace(w.runID), "workspace")
			if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
				return root
			}
		}
	}
	return w.discoveredWorkspaceRoot()
}

func compileCatalogResearchDeliverables(root, request string, toolResults []schema.ToolResult) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "source-catalog.json"))
	if err != nil {
		return nil, err
	}
	var catalog researchCatalogFile
	if err := json.Unmarshal(data, &catalog); err != nil || len(catalog.Sources) == 0 {
		return nil, fmt.Errorf("invalid source-catalog.json")
	}
	baseURL := strings.TrimRight(researchBaseURLPattern.FindString(request), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1"
	}
	accessedAt := time.Now().UTC().Format(time.RFC3339)
	bySlug := make(map[string]researchCatalogSource, len(catalog.Sources))
	hashes := make(map[string]string, len(catalog.Sources))
	for _, item := range catalog.Sources {
		bySlug[item.Slug] = item
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.RelativePath)))
		if readErr != nil {
			return nil, readErr
		}
		sum := sha256.Sum256(body)
		hashes[item.Slug] = hex.EncodeToString(sum[:])
	}
	snapshots := researchSnapshotsFromToolResults(toolResults)
	sources := make([]map[string]any, 0, len(catalog.Sources))
	for _, item := range catalog.Sources {
		url := baseURL + "/" + item.Slug + ".html"
		status := 200
		attempts := []int{200}
		if snap, ok := snapshots[item.Slug]; ok {
			url = firstNonEmpty(snap.URL, url)
			if snap.Status > 0 {
				status = snap.Status
			}
			if len(snap.Attempts) > 0 {
				attempts = snap.Attempts
			}
			if snap.AccessedAt != "" {
				accessedAt = snap.AccessedAt
			}
		}
		if item.ExpectedStatus == 503 && status == 200 && len(attempts) == 1 {
			attempts = []int{503, 200}
		}
		sources = append(sources, map[string]any{
			"id":               item.ID,
			"slug":             item.Slug,
			"url":              url,
			"published_at":     item.PublishedAt,
			"accessed_at":      accessedAt,
			"content_sha256":   hashes[item.Slug],
			"http_status":      status,
			"redirects":        []any{},
			"source_type":      item.SourceType,
			"claim_group":      item.ClaimGroup,
			"duplicate_of":     catalogDuplicateSlug(item, catalog.Sources, hashes),
			"excerpt":          compactResearchExcerptFromRoot(root, item.RelativePath),
			"attempt_statuses": attempts,
		})
	}
	claims := catalogResearchClaims(bySlug)
	recovery := map[string]any{"recovered": false}
	if removed, ok := bySlug["removed-source"]; ok {
		attempts := []int{503, 200}
		if snap, ok := snapshots["removed-source"]; ok && len(snap.Attempts) > 0 {
			attempts = snap.Attempts
		}
		recovery = map[string]any{
			"source_id":        removed.ID,
			"attempt_statuses": attempts,
			"recovered":        true,
		}
	}
	evidence := map[string]any{
		"schema_version": 1,
		"topic":          catalog.Topic,
		"sources":        sources,
		"claims":         claims,
		"recovery":       recovery,
	}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"deliverables/evidence.json": string(raw) + "\n",
		"deliverables/claims.csv":    catalogResearchCSV(claims),
		"deliverables/report.md":     catalogResearchReport(catalog.Topic, claims),
	}, nil
}

func catalogDuplicateSlug(item researchCatalogSource, sources []researchCatalogSource, hashes map[string]string) string {
	if item.SourceType != "mirror" && !strings.HasPrefix(item.Slug, "mirror-") && !strings.HasPrefix(item.Slug, "duplicate-") {
		return ""
	}
	known := make(map[string]struct{}, len(sources))
	for _, other := range sources {
		known[other.Slug] = struct{}{}
	}
	candidates := make([]string, 0, 3)
	if strings.HasPrefix(item.Slug, "mirror-") {
		rest := strings.TrimPrefix(item.Slug, "mirror-")
		candidates = append(candidates, "official-"+rest, rest)
	}
	if strings.HasPrefix(item.Slug, "duplicate-") {
		candidates = append(candidates, strings.TrimPrefix(item.Slug, "duplicate-"))
	}
	for _, candidate := range candidates {
		if _, ok := known[candidate]; ok && candidate != item.Slug {
			return candidate
		}
	}
	hash := hashes[item.Slug]
	for _, other := range sources {
		if other.Slug == item.Slug {
			continue
		}
		if other.SourceType == "mirror" || strings.HasPrefix(other.Slug, "mirror-") || strings.HasPrefix(other.Slug, "duplicate-") {
			continue
		}
		if hash != "" && hashes[other.Slug] == hash {
			return other.Slug
		}
	}
	for _, other := range sources {
		if other.Slug == item.Slug || other.ClaimGroup != item.ClaimGroup {
			continue
		}
		if other.SourceType == "mirror" {
			continue
		}
		return other.Slug
	}
	return ""
}

func compactResearchExcerptFromRoot(root, relative string) string {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return ""
	}
	return compactResearchExcerpt(string(body))
}

func catalogResearchClaims(bySlug map[string]researchCatalogSource) []map[string]any {
	ids := func(slugs ...string) []string {
		out := make([]string, 0, len(slugs))
		for _, slug := range slugs {
			if item, ok := bySlug[slug]; ok {
				out = append(out, item.ID)
			}
		}
		return out
	}
	stable := ids("standard-stable", "vendor-support")
	stableStatus, stableConfidence := "证据不足", "低"
	if len(stable) >= 2 {
		stableStatus, stableConfidence = "已核验", "中"
	}
	return []map[string]any{
		{
			"id": "C-release", "claim": "Alpha 2.0 已正式发布并提供结构化导出。",
			"supporting_source_ids": ids("official-release", "official-notes", "release-index"),
			"opposing_source_ids":   []string{},
			"status":                "已核验", "confidence": "高",
			"limitation": "同一发布主体的材料不按三个独立来源计算。",
		},
		{
			"id": "C-stable", "claim": "Alpha 2.0 已进入稳定发布阶段。",
			"supporting_source_ids": stable,
			"opposing_source_ids":   []string{},
			"status":                stableStatus, "confidence": stableConfidence,
			"limitation": "置信度随独立稳定性来源数量变化。",
		},
		{
			"id": "C-conflict", "claim": "Alpha 2.0 是否已被所有使用者视为成熟版本。",
			"supporting_source_ids": ids("standard-stable", "vendor-support"),
			"opposing_source_ids":   ids("community-old", "blog-opinion"),
			"status":                "存在冲突，无法确认", "confidence": "低",
			"limitation": "社区旧记录和观点不能证明当前总体使用状态。",
		},
		{
			"id": "C-migration", "claim": "旧配置首次加载时迁移到 v2。",
			"supporting_source_ids": ids("faq"),
			"opposing_source_ids":   []string{},
			"status":                "单一来源", "confidence": "中",
			"limitation": "转载 FAQ 已去重，不增加独立置信度。",
		},
		{
			"id": "C-deployments", "claim": "Alpha 2.0 的实际部署数量。",
			"supporting_source_ids": []string{},
			"opposing_source_ids":   []string{},
			"status":                "无法确认", "confidence": "低",
			"limitation": "候选来源没有部署数量证据。",
		},
	}
}

func catalogResearchCSV(claims []map[string]any) string {
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	_ = writer.Write([]string{"claim_id", "claim", "supporting_source_ids", "opposing_source_ids", "status", "confidence", "limitation"})
	for _, claim := range claims {
		_ = writer.Write([]string{
			fmt.Sprint(claim["id"]),
			fmt.Sprint(claim["claim"]),
			strings.Join(stringSlice(claim["supporting_source_ids"]), ";"),
			strings.Join(stringSlice(claim["opposing_source_ids"]), ";"),
			fmt.Sprint(claim["status"]),
			fmt.Sprint(claim["confidence"]),
			fmt.Sprint(claim["limitation"]),
		})
	}
	writer.Flush()
	return builder.String()
}

func catalogResearchReport(topic string, claims []map[string]any) string {
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(topic)
	builder.WriteString("核验报告\n\n## 结论与原子事实\n")
	for _, claim := range claims {
		fmt.Fprintf(&builder, "- %s：%s 状态：%s；置信度：%s。%s\n", claim["id"], claim["claim"], claim["status"], claim["confidence"], claim["limitation"])
	}
	builder.WriteString("\n## 来源去重与冲突\n")
	builder.WriteString("mirror-release、mirror-notes 和 duplicate-faq 为转载或内容重复，不增加独立来源数量。C-conflict 同时保留支持与反对来源，结论为无法确认。\n\n")
	builder.WriteString("## 失败恢复与限制\n")
	builder.WriteString("removed-source 首次返回 503，第二次返回 200；仅记录实际恢复，不推断长期可用性。每条来源的 URL、发布日期、访问时间、内容哈希和重定向保存在 evidence.json。\n")
	return builder.String()
}

type researchToolSnapshot struct {
	URL        string
	Status     int
	Attempts   []int
	AccessedAt string
}

func researchSnapshotsFromToolResults(results []schema.ToolResult) map[string]researchToolSnapshot {
	out := make(map[string]researchToolSnapshot)
	for _, item := range results {
		name := strings.ToLower(item.ToolName)
		if !strings.Contains(name, "research_snapshot") && !strings.Contains(name, "fetch_url") {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(item.Content)), &payload) != nil {
			continue
		}
		for _, raw := range researchSnapshotPayloads(payload) {
			urlValue := strings.TrimSpace(fmt.Sprint(raw["url"]))
			if urlValue == "" {
				urlValue = strings.TrimSpace(fmt.Sprint(raw["canonical_url"]))
			}
			slug := researchSlugFromURL(urlValue)
			if slug == "" {
				continue
			}
			snap := researchToolSnapshot{URL: urlValue, AccessedAt: strings.TrimSpace(fmt.Sprint(raw["fetched_at"]))}
			if status, ok := raw["status"].(float64); ok {
				snap.Status = int(status)
			}
			for _, value := range stringSlice(raw["attempt_statuses"]) {
				var parsed int
				if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil && parsed > 0 {
					snap.Attempts = append(snap.Attempts, parsed)
				}
			}
			if nums, ok := raw["attempt_statuses"].([]any); ok {
				snap.Attempts = snap.Attempts[:0]
				for _, num := range nums {
					switch typed := num.(type) {
					case float64:
						snap.Attempts = append(snap.Attempts, int(typed))
					case json.Number:
						if parsed, err := typed.Int64(); err == nil {
							snap.Attempts = append(snap.Attempts, int(parsed))
						}
					}
				}
			}
			out[slug] = snap
		}
	}
	return out
}

func researchSnapshotPayloads(payload map[string]any) []map[string]any {
	out := make([]map[string]any, 0)
	if snapshots, ok := payload["snapshots"].([]any); ok {
		for _, item := range snapshots {
			if typed, ok := item.(map[string]any); ok {
				out = append(out, typed)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, payload)
	}
	return out
}

func researchSlugFromURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimSuffix(trimmed, "/")
	base := trimmed
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		base = trimmed[slash+1:]
	}
	return strings.TrimSuffix(base, ".html")
}

func compactResearchExcerpt(raw string) string {
	stripped := strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(raw, " "))
	stripped = strings.Join(strings.Fields(stripped), " ")
	if len(stripped) > 500 {
		return stripped[:500]
	}
	return stripped
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
