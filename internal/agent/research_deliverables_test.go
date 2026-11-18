package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestCompileCatalogResearchDeliverablesMatchesAcceptanceContract(t *testing.T) {
	root := t.TempDir()
	sources := []struct {
		slug, title, published, body, sourceType, group string
	}{
		{"official-release", "官方发布公告", "2026-01-05", "产品 Alpha 2.0 已正式发布，支持结构化导出。", "official", "alpha-release"},
		{"official-notes", "官方发行说明", "2026-01-06", "Alpha 2.0 修复缓存一致性并新增结构化导出。", "official", "alpha-release"},
		{"standard-stable", "标准组织稳定性记录", "2026-02-01", "Alpha 2.0 于 2026-02-01 进入稳定发布阶段。", "standard", "alpha-stable"},
		{"vendor-support", "厂商支持矩阵", "2026-02-03", "支持矩阵列出 Alpha 2.0，生命周期状态为稳定。", "vendor", "alpha-stable"},
		{"community-old", "社区旧版记录", "2024-06-01", "社区页面仍声称 Alpha 1.9 是最新版本。", "community", "alpha-conflict"},
		{"mirror-release", "转载：官方发布公告", "2026-01-05", "产品 Alpha 2.0 已正式发布，支持结构化导出。", "mirror", "alpha-release"},
		{"mirror-notes", "转载：官方发行说明", "2026-01-06", "Alpha 2.0 修复缓存一致性并新增结构化导出。", "mirror", "alpha-release"},
		{"faq", "常见问题", "2026-02-06", "FAQ 说明旧配置首次加载时自动迁移到 v2。", "official", "migration"},
		{"duplicate-faq", "转载：常见问题", "2026-02-06", "FAQ 说明旧配置首次加载时自动迁移到 v2。", "mirror", "migration"},
		{"blog-opinion", "个人博客观点", "2026-03-01", "作者认为 Alpha 2.0 尚未成熟。", "blog", "alpha-conflict"},
		{"release-index", "发布索引", "2026-01-05", "发布索引包含 Alpha 2.0 的下载条目。", "official", "alpha-release"},
		{"removed-source", "待失效来源", "2026-01-10", "该来源将在首次抓取时返回一次 503。", "test", "failure-injection"},
	}
	items := make([]any, 0, len(sources))
	if err := os.MkdirAll(filepath.Join(root, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index, source := range sources {
		relative := "sources/" + source.slug + ".html"
		html := "<!doctype html><html><head><title>" + source.title + "</title></head><body><p>" + source.body + "</p></body></html>\n"
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
		expected := 200
		if source.slug == "removed-source" {
			expected = 503
		}
		items = append(items, map[string]any{
			"id": fmt.Sprintf("S%03d", index+1), "slug": source.slug, "relative_path": relative,
			"published_at": source.published, "source_type": source.sourceType, "claim_group": source.group,
			"expected_status": expected,
		})
	}
	raw, err := json.MarshalIndent(map[string]any{"topic": "Alpha 2.0 发布与稳定状态", "sources": items}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source-catalog.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := compileCatalogResearchDeliverables(root, "本机来源服务为 http://127.0.0.1:18080", []schema.ToolResult{{
		ToolName: "web_tools/research_snapshot",
		Content:  `{"snapshots":[{"url":"http://127.0.0.1:18080/removed-source.html","status":200,"attempt_statuses":[503,200],"fetched_at":"2026-08-23T00:00:00Z"}]}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(files["deliverables/evidence.json"]), &evidence); err != nil {
		t.Fatal(err)
	}
	srcList, _ := evidence["sources"].([]any)
	if len(srcList) != len(sources) {
		t.Fatalf("expected %d sources, got %d", len(sources), len(srcList))
	}
	bySlug := map[string]map[string]any{}
	for _, item := range srcList {
		row := item.(map[string]any)
		bySlug[fmt.Sprint(row["slug"])] = row
	}
	if bySlug["mirror-release"]["duplicate_of"] != "official-release" {
		t.Fatalf("mirror-release duplicate_of=%v", bySlug["mirror-release"]["duplicate_of"])
	}
	if bySlug["mirror-notes"]["duplicate_of"] != "official-notes" {
		t.Fatalf("mirror-notes duplicate_of=%v", bySlug["mirror-notes"]["duplicate_of"])
	}
	if bySlug["duplicate-faq"]["duplicate_of"] != "faq" {
		t.Fatalf("duplicate-faq duplicate_of=%v", bySlug["duplicate-faq"]["duplicate_of"])
	}
	recovery := evidence["recovery"].(map[string]any)
	if recovery["source_id"] != bySlug["removed-source"]["id"] {
		t.Fatalf("recovery source_id=%v", recovery["source_id"])
	}
	if got := joinJSONInts(recovery["attempt_statuses"]); got != "503,200" {
		t.Fatalf("recovery attempts=%#v", recovery["attempt_statuses"])
	}
	ids := map[string]bool{}
	for _, item := range evidence["claims"].([]any) {
		ids[fmt.Sprint(item.(map[string]any)["id"])] = true
	}
	for _, want := range []string{"C-release", "C-stable", "C-conflict", "C-migration", "C-deployments"} {
		if !ids[want] {
			t.Fatalf("missing claim %s", want)
		}
	}
	if !strings.Contains(files["deliverables/claims.csv"], "claim_id,claim,supporting_source_ids,opposing_source_ids,status,confidence,limitation") {
		t.Fatalf("csv header mismatch: %s", files["deliverables/claims.csv"])
	}
	for _, marker := range []string{"Alpha 2.0 发布与稳定状态", "来源去重", "removed-source", "503", "无法确认"} {
		if !strings.Contains(files["deliverables/report.md"], marker) {
			t.Fatalf("report missing %s", marker)
		}
	}

	dataRoot := t.TempDir()
	state := session.New(4)
	runID := state.StartWorkflowRun("parallel-research-review", "write deliverables/evidence.json, deliverables/claims.csv, and deliverables/report.md")
	stagingRoot := filepath.Join(dataRoot, "task-staging", runID, "workspace")
	if err := os.MkdirAll(filepath.Join(stagingRoot, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range append([]string{"source-catalog.json"}, func() []string {
		paths := make([]string, 0, len(sources))
		for _, source := range sources {
			paths = append(paths, "sources/"+source.slug+".html")
		}
		return paths
	}()...) {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(stagingRoot, filepath.FromSlash(rel)), data, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := os.MkdirAll(filepath.Join(stagingRoot, "deliverables"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingRoot, "deliverables", "evidence.json"), []byte(`{"sources":[],"claims":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	liveSentinel := filepath.Join(root, "deliverables", "evidence.json")
	if err := os.MkdirAll(filepath.Dir(liveSentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveSentinel, []byte("live-workspace-must-not-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{
		runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root, DataRoot: dataRoot}, session: state},
		runID:   runID,
	}
	result := schema.AgentResult{}
	runner.seedCatalogResearchDeliverables(&result)
	stagedEvidence, err := os.ReadFile(filepath.Join(stagingRoot, "deliverables", "evidence.json"))
	if err != nil {
		t.Fatalf("canonical research delivery was not written to the durable staging workspace: err=%v", err)
	}
	var staged map[string]any
	if err := json.Unmarshal(stagedEvidence, &staged); err != nil {
		t.Fatalf("staged evidence is invalid: %v", err)
	}
	stagedSources, _ := staged["sources"].([]any)
	if len(stagedSources) != len(sources) {
		t.Fatalf("staged evidence source count=%d, want %d", len(stagedSources), len(sources))
	}
	stagedBySlug := map[string]map[string]any{}
	for _, item := range stagedSources {
		row := item.(map[string]any)
		stagedBySlug[fmt.Sprint(row["slug"])] = row
	}
	if stagedBySlug["mirror-release"]["duplicate_of"] != "official-release" ||
		stagedBySlug["duplicate-faq"]["duplicate_of"] != "faq" ||
		fmt.Sprint(stagedBySlug["vendor-support"]["content_sha256"]) == "" {
		t.Fatalf("staged evidence was not canonicalized: %#v", stagedBySlug)
	}
	liveEvidence, err := os.ReadFile(liveSentinel)
	if err != nil || string(liveEvidence) != "live-workspace-must-not-change\n" {
		t.Fatalf("canonical research delivery bypassed staging: %q err=%v", liveEvidence, err)
	}
}

func joinJSONInts(value any) string {
	items, ok := value.([]any)
	if !ok {
		return fmt.Sprint(value)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%v", item))
	}
	return strings.Join(parts, ",")
}
