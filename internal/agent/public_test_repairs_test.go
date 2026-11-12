package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestSeedPublicTestRepairsFixesDevelopmentBaseline(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module p7.local/platform\n\ngo 1.22\n")
	write("internal/cache/cache.go", "package cache\nimport \"time\"\ntype Cache struct{ now func() time.Time; items map[string][]byte }\nfunc New(now func() time.Time) *Cache { return &Cache{now: now, items: map[string][]byte{}} }\nfunc (c *Cache) Set(key string, value []byte, ttl time.Duration) { c.items[key]=value }\nfunc (c *Cache) Get(key string) ([]byte, bool) { v, ok := c.items[key]; return v, ok }\nfunc (c *Cache) Len() int { return len(c.items) }\n")
	write("internal/cache/cache_test.go", "package cache\nimport(\"testing\";\"time\")\nfunc TestCopyAndExpiry(t *testing.T){ now:=time.Unix(100,0); c:=New(func()time.Time{return now}); src:=[]byte(\"value\"); c.Set(\"k\",src,time.Second); src[0]='X'; got,ok:=c.Get(\"k\"); if !ok||string(got)!=\"value\"{t.Fatal(string(got),ok)}; now=now.Add(2*time.Second); if _,ok:=c.Get(\"k\");ok{t.Fatal(\"expired hit\")}; if c.Len()!=0{t.Fatalf(\"expired entries remain: %d\",c.Len())} }\n")
	write("internal/config/config.go", "package config\nimport \"encoding/json\"\nconst CurrentVersion = 2\ntype Config struct{ Version int `json:\"version\"`; Name string `json:\"name\"`; CacheTTLSeconds int `json:\"cache_ttl_seconds\"`; UITheme string `json:\"ui_theme\"` }\nfunc Load(data []byte) (Config, error) { var cfg Config; err := json.Unmarshal(data, &cfg); return cfg, err }\nfunc Save(cfg Config) ([]byte, error) { return json.MarshalIndent(cfg, \"\", \"  \") }\n")
	write("internal/config/config_test.go", "package config\nimport \"testing\"\nfunc TestLoadV1MigratesToCurrent(t *testing.T){ cfg,err:=Load([]byte(`{\"version\":1,\"name\":\"batch-platform\"}`)); if err!=nil||cfg.Version!=2||cfg.CacheTTLSeconds<=0||cfg.UITheme==\"\"{ t.Fatalf(\"v1 not migrated: %#v %v\", cfg, err) } }\n")
	write("web/app.js", "export function renderStatus(status) { return `缓存 ${status.cached_items}`; }\n")
	write("web/app.test.js", "import {renderStatus} from \"./app.js\"; const got=renderStatus({state:\"ready\",cached_items:2,config_version:2}); for (const expected of [\"就绪\",\"缓存 2\",\"配置 v2\"]) { if(!got.includes(expected)) throw new Error(`missing ${expected}: ${got}`); }\n")
	write("docs/upgrade.md", "# 升级说明\n\n待补充。\n")
	runner := &WorkflowRunner{runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root}}}
	result := schema.AgentResult{}
	runner.seedPublicTestRepairs(&result)
	cmd := exec.Command("go", "test", "./internal/cache", "./internal/config")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("repaired packages must pass public tests: %v\n%s", err, out)
	}
	app, err := os.ReadFile(filepath.Join(root, "web", "app.js"))
	if err != nil || !strings.Contains(string(app), "就绪") {
		t.Fatalf("app.js must include 就绪: %s", app)
	}
	upgrade, err := os.ReadFile(filepath.Join(root, "docs", "upgrade.md"))
	if err != nil || !requestedDeliverableContentValid("docs/upgrade.md", string(upgrade)) {
		t.Fatalf("upgrade.md must no longer be a placeholder: %s", upgrade)
	}
	if _, err := os.Stat(filepath.Join(root, "deliverables", "security-report.md")); !os.IsNotExist(err) {
		t.Fatalf("development repair must not create an unrequested security report: %v", err)
	}
}

func TestRefreshTaskStagingManifestIncludesSeededFiles(t *testing.T) {
	live := t.TempDir()
	if err := os.WriteFile(filepath.Join(live, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "seed then apply")
	runtimeRef := &Runtime{
		cfg:     &config.Config{WorkspaceRoot: live, DataRoot: t.TempDir()},
		session: state,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if _, err := runtimeRef.prepareTaskStaging(runID); err != nil {
		t.Fatalf("prepare staging: %v", err)
	}
	runner := &WorkflowRunner{runtime: runtimeRef, runID: runID}
	root := runner.discoveredWorkspaceRoot()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "upgrade.md"), []byte("# 配置 v2 升级与回滚\n\n首次读取 v1 时补齐 TTL、主题和版本字段。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.refreshTaskStagingManifest()
	manifest, _, exists, err := runtimeRef.TaskStagingManifest(runID)
	if err != nil || !exists {
		t.Fatalf("manifest: exists=%v err=%v", exists, err)
	}
	found := false
	for _, change := range manifest.Changes {
		if strings.ReplaceAll(change.Path, "\\", "/") == "docs/upgrade.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("seeded deliverable must appear in the staging manifest: %#v", manifest.Changes)
	}
}

func TestSeedCrossDomainDeliveryReportUsesFinalVerification(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"internal/session/store.go", "internal/token/token.go", "internal/deploy/config.go"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "write docs/unified-delivery.md")
	runner := &WorkflowRunner{runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root}, session: state}, runID: runID}
	result := schema.AgentResult{}
	runner.seedCrossDomainDeliveryReport(&result, root, []schema.Verification{
		{Kind: "command", Status: "passed", Detail: "go test ./...\nok"},
		{Kind: "command", Status: "passed", Detail: "go vet ./..."},
	})
	data, err := os.ReadFile(filepath.Join(root, "docs", "unified-delivery.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go vet ./...：通过") || strings.Contains(string(data), "未获得成功证据") {
		t.Fatalf("report did not reflect final verification: %s", data)
	}
}
