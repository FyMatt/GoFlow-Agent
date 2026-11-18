#!/usr/bin/env python3
"""P7 repository-scale complex cases with behavior-based hidden acceptance."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

try:
    from .p7_office_fixture import DOCUMENTS_HIDDEN, build_documents_fixture
except ImportError:  # direct script execution
    from p7_office_fixture import DOCUMENTS_HIDDEN, build_documents_fixture


@dataclass(frozen=True)
class P7ComplexCase:
    id: str
    domain: str
    title: str
    workflow: str
    prompt: str
    builder: Callable[[Path], None]
    hidden_files: dict[str, str] = field(default_factory=dict)
    verify_commands: tuple[tuple[str, ...], ...] = ()
    required_deliverables: tuple[str, ...] = ()
    required_paths: tuple[str, ...] = ()
    complexity_signals: tuple[str, ...] = ()
    variant_mutators: tuple[Callable[[Path], None], ...] = ()
    kind: str = "go"
    ready: bool = True
    # Declared per case, because the structural floor of a delivery grows with
    # its stage and change-slice count. The defaults keep the original
    # single-package calibration; multi-module cases declare their own budget.
    llm_call_budget: int = 40
    artifact_budget: int = 30


def write(root: Path, relative: str, content: str) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def write_json(root: Path, relative: str, value: object) -> None:
    write(root, relative, json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def mutate_json(root: Path, relative: str, mutator: Callable[[dict], None]) -> None:
    path = root / relative
    value = json.loads(path.read_text(encoding="utf-8"))
    mutator(value)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def variant_development_compat(root: Path) -> None:
    mutate_json(root, "fixtures/config/v1.json", lambda value: value.update({"name": "edge-platform", "ui_theme": "light"}))
    mutate_json(root, "fixtures/config/v2.json", lambda value: value.update({"cache_ttl_seconds": 45, "ui_theme": "light"}))


def variant_development_short_ttl(root: Path) -> None:
    mutate_json(root, "fixtures/config/v1.json", lambda value: value.update({"name": "batch-platform", "cache_ttl_seconds": 5}))
    mutate_json(root, "fixtures/config/v2.json", lambda value: value.update({"cache_ttl_seconds": 5, "ui_theme": "system"}))


def variant_security_policy_matrix(root: Path) -> None:
    mutate_json(root, "testdata/policies/policy-001.json", lambda value: value.update({"role": "editor", "action": "write", "expected": True, "path": "tenant-3/docs/report.txt"}))


def variant_security_unknown_role(root: Path) -> None:
    mutate_json(root, "testdata/policies/policy-002.json", lambda value: value.update({"role": "guest", "action": "read", "expected": False}))


def variant_operations_latency(root: Path) -> None:
    mutate_json(root, "snapshots/edge-02/sample-001.json", lambda value: value.update({"latency_ms": 180, "healthy": True}))


def variant_operations_target_snapshot(root: Path) -> None:
    mutate_json(root, "snapshots/edge-04/sample-003.json", lambda value: value.update({"version": "v1", "healthy": False}))


def variant_research_reordered(root: Path) -> None:
    path = root / "source-catalog.json"
    value = json.loads(path.read_text(encoding="utf-8"))
    value["sources"] = list(reversed(value.get("sources", [])))
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def variant_research_metadata(root: Path) -> None:
    mutate_json(root, "source-catalog.json", lambda value: value.update({"topic": "Alpha 2.0 兼容性与稳定状态"}))


def variant_documents_findings(root: Path) -> None:
    path = root / "inputs/findings.csv"
    path.write_text(path.read_text(encoding="utf-8").replace("审计日志记录完整令牌", "审计日志记录完整访问令牌"), encoding="utf-8", newline="\n")


def variant_documents_date(root: Path) -> None:
    mutate_json(root, "fixture-manifest.json", lambda value: value.setdefault("variant", {}).update({"review_date": "2026-08-19"}))


def variant_cross_domain_port(root: Path) -> None:
    mutate_json(root, "deploy/service.json", lambda value: value.update({"port": 9090}))


def variant_cross_domain_health(root: Path) -> None:
    mutate_json(root, "deploy/service.json", lambda value: value.update({"health_path": "/ready"}))


def build_development(root: Path) -> None:
    write(root, "go.mod", "module p7.local/platform\n\ngo 1.22\n")
    write(
        root,
        "internal/config/config.go",
        '''package config

import "encoding/json"

const CurrentVersion = 2

type Config struct {
	Version         int    `json:"version"`
	Name            string `json:"name"`
	CacheTTLSeconds int    `json:"cache_ttl_seconds"`
	UITheme         string `json:"ui_theme"`
}

func Load(data []byte) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil { return Config{}, err }
	return cfg, nil
}

func Save(cfg Config) ([]byte, error) { return json.MarshalIndent(cfg, "", "  ") }
''',
    )
    write(
        root,
        "internal/config/config_test.go",
        '''package config
import("os";"testing")
func TestLoadV2(t *testing.T) { cfg,err:=Load([]byte(`{"version":2,"name":"demo","cache_ttl_seconds":30,"ui_theme":"dark"}`)); if err!=nil||cfg.Version!=2||cfg.CacheTTLSeconds!=30 { t.Fatal(cfg,err) } }
func TestLoadV1MigratesToCurrent(t *testing.T) {
	data,err:=os.ReadFile("../../fixtures/config/v1.json")
	if err!=nil { t.Fatal(err) }
	cfg,err:=Load(data)
	if err!=nil||cfg.Version!=2||cfg.CacheTTLSeconds<=0||cfg.UITheme=="" { t.Fatalf("v1 not migrated: %#v %v", cfg, err) }
}
''',
    )
    write(
        root,
        "internal/cache/cache.go",
        '''package cache

import "time"

type entry struct { value []byte; expires time.Time }
type Cache struct { now func() time.Time; items map[string]entry }
func New(now func() time.Time) *Cache { return &Cache{now:now,items:map[string]entry{}} }
func (c *Cache) Set(key string, value []byte, ttl time.Duration) { c.items[key]=entry{value:value,expires:c.now().Add(ttl)} }
func (c *Cache) Get(key string) ([]byte,bool) { item,ok:=c.items[key]; if !ok||!c.now().Before(item.expires){ return nil,false }; return item.value,true }
func (c *Cache) Len() int { return len(c.items) }
''',
    )
    write(
        root,
        "internal/cache/cache_test.go",
        '''package cache
import("testing";"time")
func TestBasic(t *testing.T){ now:=time.Unix(100,0); c:=New(func()time.Time{return now}); c.Set("a",[]byte("v"),time.Minute); if v,ok:=c.Get("a");!ok||string(v)!="v"{t.Fatal(string(v),ok)} }
func TestCopyAndExpiry(t *testing.T){
	now:=time.Unix(100,0)
	c:=New(func()time.Time{return now})
	src:=[]byte("value")
	c.Set("k",src,time.Second)
	src[0]='X'
	got,ok:=c.Get("k")
	if !ok||string(got)!="value"{t.Fatal(string(got),ok)}
	got[0]='Y'
	again,_:=c.Get("k")
	if string(again)!="value"{t.Fatal("returned alias")}
	now=now.Add(2*time.Second)
	if _,ok:=c.Get("k");ok{t.Fatal("expired hit")}
	if c.Len()!=0{t.Fatalf("expired entries remain: %d",c.Len())}
}
''',
    )
    write(
        root,
        "internal/api/status.go",
        '''package api
import "p7.local/platform/internal/cache"
type Status struct { State string `json:"state"`; CachedItems int `json:"cached_items"`; ConfigVersion int `json:"config_version"` }
func Current(c *cache.Cache, version int) Status { return Status{State:"ready",CachedItems:c.Len(),ConfigVersion:version} }
''',
    )
    write(
        root,
        "internal/api/status_test.go",
        '''package api
import("testing";"time";"p7.local/platform/internal/cache")
func TestCurrent(t *testing.T){ c:=cache.New(time.Now); if got:=Current(c,2);got.State!="ready"||got.ConfigVersion!=2{t.Fatal(got)} }
''',
    )
    write(
        root,
        "cmd/platform/main.go",
        '''package main
import("encoding/json";"fmt";"os";"p7.local/platform/internal/config")
func main(){ data,err:=os.ReadFile(os.Args[1]);if err!=nil{panic(err)};cfg,err:=config.Load(data);if err!=nil{panic(err)};out,_:=json.Marshal(cfg);fmt.Println(string(out)) }
''',
    )
    write(root, "web/index.html", '<main><h1>本地平台</h1><p id="cache-state">正在加载</p><script src="app.js"></script></main>\n')
    write(root, "package.json", '{"type":"module"}\n')
    write(
        root,
        ".github/workflows/race.yml",
        "name: race\non:\n  push:\njobs:\n  race:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - run: go test -race ./...\n",
    )
    write(root, "web/app.js", 'export function renderStatus(status) { return `缓存 ${status.cached_items}`; }\n')
    write(root, "web/app.test.js", 'import {renderStatus} from "./app.js"; const got=renderStatus({state:"ready",cached_items:2,config_version:2}); for (const expected of ["就绪","缓存 2","配置 v2"]) { if(!got.includes(expected)) throw new Error(`missing ${expected}: ${got}`); }\n')
    write(root, "docs/upgrade.md", "# 升级说明\n\n待补充。\n")
    for version in (1, 2):
        write_json(root, f"fixtures/config/v{version}.json", {"version": version, "name": "demo", "cache_ttl_seconds": 30, "ui_theme": "system"})
DEVELOPMENT_HIDDEN = {
    "internal/config/p7_hidden_test.go": '''package config
import("encoding/json";"os";"testing")
func TestP7V1MigrationAndStableV2(t *testing.T){for _,name:=range []string{"v1.json","v2.json"}{data,err:=os.ReadFile("../../fixtures/config/"+name);if err!=nil{t.Fatal(err)};cfg,err:=Load(data);if err!=nil{t.Fatal(err)};if cfg.Version!=2||cfg.CacheTTLSeconds<=0||cfg.UITheme==""{t.Fatalf("%s not migrated: %#v",name,cfg)};out,err:=Save(cfg);if err!=nil{t.Fatal(err)};var raw map[string]any;if json.Unmarshal(out,&raw)!=nil||raw["version"]!=float64(2){t.Fatal(string(out))}}}
''',
    "internal/cache/p7_hidden_test.go": '''package cache
import("fmt";"sync";"testing";"time")
func TestP7ConcurrentExpiryAndCopies(t *testing.T){ var mu sync.Mutex;now:=time.Unix(100,0);c:=New(func()time.Time{mu.Lock();defer mu.Unlock();return now});src:=[]byte("value");c.Set("copy",src,time.Second);src[0]='X';got,ok:=c.Get("copy");if !ok||string(got)!="value"{t.Fatal(string(got),ok)};got[0]='Y';again,_:=c.Get("copy");if string(again)!="value"{t.Fatal("returned alias")};var wg sync.WaitGroup;for i:=0;i<20;i++{i:=i;wg.Add(1);go func(){defer wg.Done();for j:=0;j<200;j++{k:=fmt.Sprintf("%d-%d",i,j);c.Set(k,[]byte(k),time.Minute);c.Get(k)}}()};wg.Wait();mu.Lock();now=now.Add(2*time.Minute);mu.Unlock();if _,ok:=c.Get("copy");ok{t.Fatal("expired hit")};if c.Len()!=0{t.Fatalf("expired entries remain: %d",c.Len())} }
''',
    "web/p7_hidden.test.js": '''import {renderStatus} from "./app.js";
const got=renderStatus({state:"ready",cached_items:2,config_version:2});
for(const expected of ["就绪","缓存 2","配置 v2"]){if(!got.includes(expected))throw new Error(`missing ${expected}: ${got}`)}
''',
}


def build_security(root: Path) -> None:
    write(root, "go.mod", "module p7.local/secureapp\n\ngo 1.22\n")
    write(
        root,
        "internal/auth/auth.go",
        '''package auth
type Role string
const(RoleViewer Role="viewer";RoleEditor Role="editor";RoleAdmin Role="admin")
func Allowed(role Role, action string) bool { return role==RoleAdmin || action!="admin" }
''',
    )
    write(root, "internal/auth/auth_test.go", '''package auth
import "testing"
func TestAdmin(t *testing.T){if !Allowed(RoleAdmin,"admin"){t.Fatal()}}
func TestExplicitGrantsAndDefaultDeny(t *testing.T){
	if Allowed(RoleViewer,"write") || Allowed(RoleEditor,"admin") || Allowed(Role("unknown"),"read") || Allowed(RoleAdmin,"unknown-action") {
		t.Fatal("authorization must default deny unknown roles and actions")
	}
	if !Allowed(RoleViewer,"read") || !Allowed(RoleEditor,"write") || !Allowed(RoleAdmin,"read") {
		t.Fatal("known grants must remain allowed")
	}
}
''')
    write(
        root,
        "internal/filesafe/path.go",
        '''package filesafe
import "path/filepath"
func Resolve(root,userPath string)(string,error){ return filepath.Join(root,userPath),nil }
''',
    )
    write(root, "internal/filesafe/path_test.go", '''package filesafe
import("path/filepath";"testing")
func TestRelative(t *testing.T){root:=t.TempDir();got,err:=Resolve(root,"docs/a.txt");if err!=nil||got!=filepath.Join(root,"docs/a.txt"){t.Fatal(got,err)}}
func TestRejectTraversal(t *testing.T){root:=t.TempDir();if _,err:=Resolve(root,"../../outside");err==nil{t.Fatal("traversal accepted")};if _,err:=Resolve(root,filepath.Join(t.TempDir(),"x"));err==nil{t.Fatal("absolute path accepted")}}
''')
    write(
        root,
        "internal/request/header.go",
        '''package request
import "strings"
// NormalizeHeader trims surrounding space. Values containing CR, LF, or NUL must return an error.
func NormalizeHeader(value string)(string,error){ return strings.TrimSpace(value),nil }
''',
    )
    write(root, "internal/request/header_test.go", '''package request
import "testing"
func TestRejectHeaderInjection(t *testing.T){
	if _,err:=NormalizeHeader("ok\\r\\nX-Evil: 1");err==nil{t.Fatal("crlf accepted: values containing CR, LF, or NUL must return error")}
	got,err:=NormalizeHeader("  safe  ");if err!=nil||got!="safe"{t.Fatal(got,err)}
}
''')
    write(
        root,
        "internal/audit/redact.go",
        '''package audit
import "strings"
func Redact(line string) string { return strings.TrimSpace(line) }
''',
    )
    write(root, "internal/audit/redact_test.go", '''package audit
import("strings";"testing")
func TestRedactsSecrets(t *testing.T){
	got:=Redact("user=a token=secret-value password=hunter2")
	if strings.Contains(got,"secret-value")||strings.Contains(got,"hunter2"){t.Fatalf("secret leaked: %s",got)}
	if !strings.Contains(got,"user=a"){t.Fatal(got)}
}
''')
    write(root, "cmd/secureapp/main.go", "package main\nfunc main(){}\n")
    write(root, "SECURITY.md", "# 授权安全评估\n\n范围：本地夹具。\n")
    grants = {"viewer": {"read"}, "editor": {"read", "write"}, "admin": {"read", "write", "admin"}}
    for index in range(12):
        role = ("viewer", "editor", "admin")[index % 3]
        action = ("read", "write", "admin", "unknown-action")[index % 4]
        write_json(root, f"testdata/policies/policy-{index:03d}.json", {
            "role": role,
            "action": action,
            "expected": action in grants[role],
            "path": f"tenant-{index % 8}/file-{index:03d}.txt",
        })
    write(
        root,
        "testdata/policies_test.go",
        '''package testdata
import("encoding/json";"os";"path/filepath";"testing")
func TestPolicyCorpusIsStructured(t *testing.T){paths,_:=filepath.Glob("policies/*.json");if len(paths)<12{t.Fatal(len(paths))};roles:=map[string]bool{};actions:=map[string]bool{};for _,p:=range paths{data,e:=os.ReadFile(p);if e!=nil{t.Fatal(e)};var v map[string]any;if json.Unmarshal(data,&v)!=nil||v["role"]==""||v["action"]==""{t.Fatal(p)};roles[v["role"].(string)]=true;actions[v["action"].(string)]=true};if len(roles)<3||len(actions)<4{t.Fatalf("incomplete policy matrix roles=%v actions=%v",roles,actions)}}
''',
    )


SECURITY_HIDDEN = {
    "internal/auth/p7_hidden_test.go": '''package auth
import("encoding/json";"os";"path/filepath";"testing")
func TestP7DefaultDeny(t *testing.T){if Allowed(RoleViewer,"write")||Allowed(RoleEditor,"admin")||Allowed(Role("unknown"),"read")||Allowed(RoleAdmin,"unknown-action"){t.Fatal("authorization must be explicit and default deny")};if !Allowed(RoleViewer,"read")||!Allowed(RoleEditor,"write")||!Allowed(RoleAdmin,"admin"){t.Fatal("known grants changed")};paths,_:=filepath.Glob("../../testdata/policies/*.json");if len(paths)<12{t.Fatal(len(paths))};for _,path:=range paths{var item struct{Role Role `json:"role"`;Action string `json:"action"`;Expected bool `json:"expected"`};data,err:=os.ReadFile(path);if err!=nil||json.Unmarshal(data,&item)!=nil{t.Fatal(path,err)};if got:=Allowed(item.Role,item.Action);got!=item.Expected{t.Fatalf("%s: got %v want %v",path,got,item.Expected)}}}
''',
    "internal/filesafe/p7_hidden_test.go": '''package filesafe
import("os";"os/exec";"path/filepath";"runtime";"testing")
func makeP7DirectoryLink(t *testing.T,link,target string){t.Helper();symlinkErr:=os.Symlink(target,link);if symlinkErr==nil{return};if runtime.GOOS=="windows"{output,err:=exec.Command("cmd","/c","mklink","/J",link,target).CombinedOutput();if err==nil{return};t.Fatalf("cannot create Windows junction for boundary test: %v: %s",err,output)};t.Fatalf("cannot create symlink for boundary test: %v",symlinkErr)}
func TestP7BoundaryAndSymlink(t *testing.T){root:=t.TempDir();outside:=t.TempDir();if _,err:=Resolve(root,"../../outside");err==nil{t.Fatal("traversal accepted")};if _,err:=Resolve(root,filepath.Join(outside,"x"));err==nil{t.Fatal("absolute path accepted")};link:=filepath.Join(root,"link");makeP7DirectoryLink(t,link,outside);if _,err:=Resolve(root,"link/missing-secret");err==nil{t.Fatal("symlink ancestor escape for nonexistent target accepted")}}
''',
    "internal/request/p7_hidden_test.go": '''package request
import "testing"
func TestP7RejectHeaderInjection(t *testing.T){for _,v:=range []string{"ok\\r\\nX-Evil: 1","ok\\nX: 1","a\\x00b"}{if _,err:=NormalizeHeader(v);err==nil{t.Fatalf("accepted %q",v)}};if got,err:=NormalizeHeader("  safe  ");err!=nil||got!="safe"{t.Fatal(got,err)}}
''',
    "internal/audit/p7_hidden_test.go": '''package audit
import("strings";"testing")
func TestP7RedactsSecrets(t *testing.T){got:=Redact("user=a token=secret-value password=hunter2 api_key=abc123");for _,secret:=range []string{"secret-value","hunter2","abc123"}{if strings.Contains(got,secret){t.Fatalf("secret leaked: %s",got)}};if !strings.Contains(got,"user=a"){t.Fatal(got)}}
''',
}


def build_operations(root: Path) -> None:
    write(root, "go.mod", "module p7.local/fleetops\n\ngo 1.22\n")
    write(
        root,
        "internal/inventory/inventory.go",
        """package inventory

type Target struct {
	ID      string `json:"id"`
	Batch   int    `json:"batch"`
	Desired string `json:"desired"`
}

func Default() []Target {
	return []Target{
		{"edge-01", 1, "v2"},
		{"edge-02", 2, "v2"},
		{"edge-03", 2, "v2"},
		{"edge-04", 3, "v2"},
		{"edge-05", 3, "v2"},
	}
}
""",
    )
    write(
        root,
        "internal/rollout/rollout.go",
        """package rollout

import (
	"context"

	"p7.local/fleetops/internal/inventory"
)

type Executor interface {
	Backup(context.Context, inventory.Target) error
	Apply(context.Context, inventory.Target) error
	Healthy(context.Context, inventory.Target) error
	Rollback(context.Context, inventory.Target) error
}

type Result struct {
	Applied       []string `json:"applied"`
	RolledBack    []string `json:"rolled_back"`
	Failed        string   `json:"failed"`
	StoppedBefore []string `json:"stopped_before"`
}

func Run(ctx context.Context, targets []inventory.Target, exec Executor) (Result, error) {
	var out Result
	for _, target := range targets {
		if err := exec.Backup(ctx, target); err != nil {
			return out, err
		}
		if err := exec.Apply(ctx, target); err != nil {
			return out, err
		}
		out.Applied = append(out.Applied, target.ID)
		if err := exec.Healthy(ctx, target); err != nil {
			return out, err
		}
	}
	return out, nil
}
""",
    )
    write(
        root,
        "internal/rollout/rollout_test.go",
        """package rollout

import (
	"context"
	"errors"
	"testing"

	"p7.local/fleetops/internal/inventory"
)

type okExec struct{}

func (okExec) Backup(context.Context, inventory.Target) error   { return nil }
func (okExec) Apply(context.Context, inventory.Target) error    { return nil }
func (okExec) Healthy(context.Context, inventory.Target) error  { return nil }
func (okExec) Rollback(context.Context, inventory.Target) error { return nil }

func TestSuccess(t *testing.T) {
	got, err := Run(context.Background(), inventory.Default()[:3], okExec{})
	if err != nil || len(got.Applied) != 3 {
		t.Fatal(got, err)
	}
}

type failExec struct{ okExec }

func (failExec) Healthy(_ context.Context, target inventory.Target) error {
	if target.ID == "edge-04" {
		return errors.New("injected health failure")
	}
	return nil
}

func (failExec) Rollback(context.Context, inventory.Target) error { return nil }

func TestStopsOnInjectedHealthFailure(t *testing.T) {
	got, err := Run(context.Background(), inventory.Default(), failExec{})
	if err == nil {
		t.Fatal("expected failure")
	}
	if got.Failed != "edge-04" || len(got.RolledBack) != 1 || got.RolledBack[0] != "edge-04" {
		t.Fatalf("%#v", got)
	}
	if len(got.StoppedBefore) == 0 || got.StoppedBefore[0] != "edge-05" {
		t.Fatalf("did not stop remaining targets: %#v", got)
	}
}
""",
    )
    write(root, "internal/health/summary.go", """package health

func Passed(results map[string]bool) bool {
	for _, ok := range results {
		if !ok {
			return false
		}
	}
	return len(results) > 0
}
""")
    write(root, "cmd/fleetops/main.go", "package main\n\nfunc main() {}\n")
    write(root, "docs/runbook.md", "# 分批发布运行手册\n\n待补充。\n")
    for index in range(10):
        target = 1 + index % 5
        write_json(root, f"snapshots/edge-{target:02d}/sample-{index:03d}.json", {
            "target": f"edge-{target:02d}", "sample": index, "version": "v1",
            "healthy": True, "latency_ms": 10 + index % 13,
        })
    write(root, "snapshots/snapshots_test.go", """package snapshots

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotCorpus(t *testing.T) {
	paths, _ := filepath.Glob("edge-*/*.json")
	if len(paths) < 10 {
		t.Fatal(len(paths))
	}
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		var value map[string]any
		if json.Unmarshal(data, &value) != nil || value["target"] == "" {
			t.Fatal(path)
		}
	}
}
""")


OPERATIONS_HIDDEN = {
    "internal/rollout/p7_hidden_test.go": '''package rollout
import("context";"errors";"reflect";"testing";"p7.local/fleetops/internal/inventory")
type injected struct{calls []string};func(e *injected)Backup(_ context.Context,t inventory.Target)error{e.calls=append(e.calls,"backup:"+t.ID);return nil};func(e *injected)Apply(_ context.Context,t inventory.Target)error{e.calls=append(e.calls,"apply:"+t.ID);return nil};func(e *injected)Healthy(_ context.Context,t inventory.Target)error{e.calls=append(e.calls,"health:"+t.ID);if t.ID=="edge-04"{return errors.New("injected health failure")};return nil};func(e *injected)Rollback(_ context.Context,t inventory.Target)error{e.calls=append(e.calls,"rollback:"+t.ID);return nil}
func TestP7StopsThirdBatchAndRollsBackOnlyFailedTarget(t *testing.T){exec:=&injected{};got,err:=Run(context.Background(),inventory.Default(),exec);if err==nil{t.Fatal("expected injected failure")};if !reflect.DeepEqual(got.Applied,[]string{"edge-01","edge-02","edge-03","edge-04"}){t.Fatalf("applied=%v",got.Applied)};if !reflect.DeepEqual(got.RolledBack,[]string{"edge-04"}){t.Fatalf("rollback=%v",got.RolledBack)};if got.Failed!="edge-04"||!reflect.DeepEqual(got.StoppedBefore,[]string{"edge-05"}){t.Fatalf("result=%#v",got)};want:=[]string{"backup:edge-01","apply:edge-01","health:edge-01","backup:edge-02","apply:edge-02","health:edge-02","backup:edge-03","apply:edge-03","health:edge-03","backup:edge-04","apply:edge-04","health:edge-04","rollback:edge-04"};if !reflect.DeepEqual(exec.calls,want){t.Fatalf("calls=%v",exec.calls)}}
''',
}


def build_cross_domain(root: Path) -> None:
    write(root, "go.mod", "module p7.local/unified\n\ngo 1.22\n")
    write(
        root,
        "internal/token/token.go",
        '''package token
import("crypto/sha256";"encoding/hex")
func Digest(value string)string{sum:=sha256.Sum256([]byte(value));return hex.EncodeToString(sum[:])}
func Equal(stored,value string)bool{return stored==Digest(value)}
''',
    )
    write(
        root,
        "internal/session/store.go",
        '''package session
import "sync"
type Store struct{mu sync.Mutex;items map[string]string}
func New()*Store{return &Store{items:map[string]string{}}}
// Put and Get are called concurrently and must lock mu on every map access.
func(s *Store)Put(id,value string){s.items[id]=value};func(s *Store)Get(id string)(string,bool){v,ok:=s.items[id];return v,ok}
''',
    )
    write(
        root,
        "internal/deploy/config.go",
        '''package deploy
import "encoding/json"
type Config struct{Version int `json:"version"`;Port int `json:"port"`;HealthPath string `json:"health_path"`}
// Parse must upgrade Version < 2 to 2 and rewrite empty or "/health" HealthPath to "/healthz".
func Parse(data []byte)(Config,error){var c Config;err:=json.Unmarshal(data,&c);return c,err}
''',
    )
    write(root, "cmd/service/main.go", "package main\nfunc main(){}\n")
    write(root, "deploy/service.json", "{\"version\":1,\"port\":8080,\"health_path\":\"/health\"}\n")
    # Seed only a title: a placeholder here would survive a partial edit and
    # turn a genuinely finished report into a false "placeholder" finding.
    write(root, "docs/unified-delivery.md", "# 统一交付报告\n")
    write(root, "internal/token/token_test.go", '''package token
import "testing"
func TestDigest(t *testing.T){if !Equal(Digest("x"),"x"){t.Fatal()}}
func TestRejectMismatchedDigest(t *testing.T){if Equal(Digest("correct"),"wrong"){t.Fatal("mismatched token must not compare equal")}}
''')
    write(root, "internal/session/store_test.go", '''package session
import("fmt";"sync";"testing")
func TestConcurrentStore(t *testing.T){s:=New();var wg sync.WaitGroup;for i:=0;i<32;i++{i:=i;wg.Add(1);go func(){defer wg.Done();for j:=0;j<80;j++{id:=fmt.Sprintf("%d-%d",i,j);s.Put(id,id);if v,ok:=s.Get(id);!ok||v!=id{t.Errorf("lost %s",id)}}}()};wg.Wait()}
''')
    write(root, "internal/deploy/config_test.go", '''package deploy
import "testing"
func TestParseMigratesV1(t *testing.T){c,err:=Parse([]byte(`{"port":8080}`));if err!=nil{t.Fatal(err)};if c.Version!=2||c.HealthPath!="/healthz"{t.Fatalf("v1 must migrate: %#v",c)}}
''')
    for index in range(8):
        write_json(root, f"testdata/contracts/contract-{index:03d}.json", {
            "session_id": f"session-{index:03d}", "token_digest": "sha256",
            "deploy_version": 1, "port": 8080 + index % 4, "health_path": "/health",
        })
    write(root, "testdata/contracts_test.go", '''package testdata
import("encoding/json";"os";"path/filepath";"testing")
func TestContracts(t *testing.T){paths,_:=filepath.Glob("contracts/*.json");if len(paths)<8{t.Fatal(len(paths))};for _,p:=range paths{data,_:=os.ReadFile(p);var v map[string]any;if json.Unmarshal(data,&v)!=nil||v["session_id"]==""{t.Fatal(p)}}}
''')


CROSS_DOMAIN_HIDDEN = {
    "internal/token/p7_hidden_test.go": '''package token
import "testing"
func TestP7ConstantTimeContract(t *testing.T){if Equal(Digest("correct"),"wrong"){t.Fatal()};if !Equal(Digest("correct"),"correct"){t.Fatal()}}
''',
    "internal/session/p7_hidden_test.go": '''package session
import("fmt";"sync";"testing")
func TestP7ConcurrentStore(t *testing.T){s:=New();var wg sync.WaitGroup;for i:=0;i<30;i++{i:=i;wg.Add(1);go func(){defer wg.Done();for j:=0;j<100;j++{id:=fmt.Sprintf("%d-%d",i,j);s.Put(id,id);if v,ok:=s.Get(id);!ok||v!=id{t.Errorf("lost %s",id)}}}()};wg.Wait()}
''',
    "internal/deploy/p7_hidden_test.go": '''package deploy
import("os";"testing")
func TestP7V1MigrationDefaults(t *testing.T){c,err:=Parse([]byte(`{"port":8080}`));if err!=nil{t.Fatal(err)};if c.Version!=2||c.HealthPath!="/healthz"{t.Fatalf("not migrated: %#v",c)};data,err:=os.ReadFile("../../deploy/service.json");if err!=nil{t.Fatal(err)};fromFile,err:=Parse(data);if err!=nil{t.Fatal(err)};if fromFile.Version!=2||fromFile.Port==0||fromFile.HealthPath==""{t.Fatalf("fixture migration failed: %#v",fromFile)}}
''',
}


def build_research(root: Path) -> None:
    sources = [
        ("official-release", "官方发布公告", "2026-01-05", "产品 Alpha 2.0 已正式发布，支持结构化导出。", "official", "alpha-release"),
        ("official-notes", "官方发行说明", "2026-01-06", "Alpha 2.0 修复缓存一致性并新增结构化导出。", "official", "alpha-release"),
        ("standard-stable", "标准组织稳定性记录", "2026-02-01", "Alpha 2.0 于 2026-02-01 进入稳定发布阶段。", "standard", "alpha-stable"),
        ("vendor-support", "厂商支持矩阵", "2026-02-03", "支持矩阵列出 Alpha 2.0，生命周期状态为稳定。", "vendor", "alpha-stable"),
        ("community-old", "社区旧版记录", "2024-06-01", "社区页面仍声称 Alpha 1.9 是最新版本。", "community", "alpha-conflict"),
        ("mirror-release", "转载：官方发布公告", "2026-01-05", "产品 Alpha 2.0 已正式发布，支持结构化导出。", "mirror", "alpha-release"),
        ("mirror-notes", "转载：官方发行说明", "2026-01-06", "Alpha 2.0 修复缓存一致性并新增结构化导出。", "mirror", "alpha-release"),
        ("archive-roadmap", "归档路线图", "2025-04-01", "路线图计划在 2026 年发布 Alpha 2.0，但不是发布证明。", "archive", "planning"),
        ("security-advisory", "安全公告", "2026-02-08", "安全公告适用于 Alpha 2.0，不证明部署数量。", "official", "security"),
        ("api-reference", "API 参考", "2026-01-07", "Alpha 2.0 API 包含 export/structured 端点。", "official", "structured-export"),
        ("compatibility", "兼容性清单", "2026-02-04", "Alpha 2.0 与配置 v1 读取兼容。", "vendor", "compatibility"),
        ("blog-opinion", "个人博客观点", "2026-03-01", "作者认为 Alpha 2.0 尚未成熟，该内容为观点。", "blog", "alpha-conflict"),
        ("release-index", "发布索引", "2026-01-05", "发布索引包含 Alpha 2.0 的下载条目。", "official", "alpha-release"),
        ("status-page", "服务状态历史", "2026-02-05", "状态页只记录托管服务可用性，不证明软件版本。", "official", "operations"),
        ("faq", "常见问题", "2026-02-06", "FAQ 说明旧配置首次加载时自动迁移到 v2。", "official", "migration"),
        ("duplicate-faq", "转载：常见问题", "2026-02-06", "FAQ 说明旧配置首次加载时自动迁移到 v2。", "mirror", "migration"),
        ("undated-forum", "无日期论坛摘录", "", "论坛用户称仍在使用 Alpha 1.9，缺少发布日期。", "forum", "usage"),
        ("removed-source", "待失效来源", "2026-01-10", "该来源将在首次抓取时返回一次 503，恢复后内容可用。", "test", "failure-injection"),
    ]
    catalog = []
    for index, (slug, title, published, body, source_type, claim_group) in enumerate(sources, start=1):
        html = (
            "<!doctype html><html lang=\"zh-CN\"><head>"
            f"<title>{title}</title><meta name=\"published\" content=\"{published}\"></head>"
            f"<body><main><h1>{title}</h1><p>{body}</p></main></body></html>\n"
        )
        relative = f"sources/{slug}.html"
        write(root, relative, html)
        catalog.append({
            "id": f"S{index:03d}", "slug": slug, "relative_path": relative,
            "published_at": published, "source_type": source_type, "claim_group": claim_group,
            "expected_status": 503 if slug == "removed-source" else 200,
            # The evaluator hashes the fixture file bytes, while the web tool
            # hashes extracted text. Publishing the byte hash makes the
            # contract reproducible instead of asking the model to compute it.
            "content_sha256": hashlib.sha256(html.encode("utf-8")).hexdigest(),
        })
    write_json(root, "source-catalog.json", {"topic": "Alpha 2.0 发布与稳定状态", "sources": catalog})
    write(root, "README.md", "# 多来源核验夹具\n\n来源由验收器在本机回环 HTTP 服务提供。\n")


RESEARCH_HIDDEN = {
    "acceptance/hidden-research.json": json.dumps({
        "minimum_sources": 15,
        "required_duplicate_groups": ["alpha-release", "migration"],
        "required_conflicts": ["alpha-conflict"],
        "failure_injection": "removed-source returns 503 once, then 200",
        "mutation_gate": "removing standard-stable must lower alpha-stable confidence",
    }, ensure_ascii=False, indent=2) + "\n",
}


def cases() -> list[P7ComplexCase]:
    return [
        P7ComplexCase(
            id="p7-development-platform-migration", domain="development", title="平台配置迁移与并发缓存交付",
            workflow="complex-project-delivery", builder=build_development, hidden_files=DEVELOPMENT_HIDDEN,
            prompt="在这个包含配置、缓存、状态 API、命令行和页面的多模块平台仓库中完成 v1 到 v2 配置迁移、并发安全 TTL 缓存和页面状态更新，保持旧配置兼容；补充公开测试、升级说明和回滚办法；所有交付文档必须写完整，不得出现“待补充”“待生成”“TODO”或 placeholder 等占位内容。必须先生成变更分片，完成 go test ./...、go test -race ./...、go vet ./...、gofmt 和页面复核。普通用户可见的页面状态必须使用简体中文，不得把 ready、active、error 等英文状态词直接展示给用户。如果本机明确因缺少 CGO 或 C 编译器无法启动 race，必须保留普通并发测试通过证据，并新增 .github/workflows/race.yml 在 Ubuntu 执行 go test -race ./...；报告必须明确这是延期门禁，不得宣称本机 race 已通过。",
            verify_commands=(("go", "test", "./..."), ("go", "vet", "./..."), ("gofmt", "-l", "."), ("node", "web/app.test.js"), ("node", "web/p7_hidden.test.js")), required_deliverables=("docs/upgrade.md",),
            required_paths=("internal/config/config.go", "internal/cache/cache.go", "internal/api/status.go", "cmd/platform/main.go", "web/index.html"),
            complexity_signals=("跨模块配置迁移", "并发与过期状态", "页面与命令行产物", "隐藏回归"),
            variant_mutators=(variant_development_compat, variant_development_short_ttl),
            # 12 个阶段 + 6 个必需写入分片：每个分片至少一次读取、一次写入和
            # 一次即时检查，再加计划、验证、修复、恢复验证、审计与最终报告。
            llm_call_budget=70,
            artifact_budget=90,
        ),
        P7ComplexCase(
            id="p7-security-defensive-remediation", domain="security", title="多模块防御性安全修复",
            workflow="security-audit-evidence-gate", builder=build_security, hidden_files=SECURITY_HIDDEN,
            prompt="在明确授权的本地多模块应用中先只读审计认证授权、路径边界、请求头和审计脱敏；审批后只修复有证据的问题，补充公开回归并独立复测。目标实现文件是 internal/auth/auth.go、internal/filesafe/path.go、internal/request/header.go、internal/audit/redact.go，入口文件是 cmd/secureapp/main.go；对应测试位于各包的 *_test.go。testdata/policies 给出授权矩阵：viewer 仅 read，editor 为 read/write，admin 为 read/write/admin，未知角色与未知动作必须默认拒绝。路径解析必须拒绝 .. 穿越、绝对路径和符号链接逃逸；请求头必须拒绝 CR/LF/NUL；审计日志必须脱敏 token/password/api_key，Redact 后不得再包含 secret-value、hunter2、abc123，不能只加 [REDACTED] 前缀。所有补丁必须带齐 import 并使 go test ./... 编译通过。必须实际修改源码并写入 deliverables/security-report.md。修复阶段直接读取上述实现文件和对应测试，不要重复搜索整个目录。",
            verify_commands=(("go", "test", "./..."), ("go", "vet", "./...")), required_deliverables=("deliverables/security-report.md",),
            required_paths=("internal/auth/auth.go", "internal/filesafe/path.go", "internal/request/header.go", "internal/audit/redact.go", "cmd/secureapp/main.go"),
            complexity_signals=("授权默认拒绝", "路径与符号链接边界", "请求协议校验", "敏感信息脱敏"),
            variant_mutators=(variant_security_policy_matrix, variant_security_unknown_role),
            # 四个实现文件 + 安全报告 + 多轮审查/返修/复测证据，产物数高于单包案例。
            artifact_budget=60,
        ),
        P7ComplexCase(
            id="p7-operations-batched-rollout", domain="operations", title="五目标金丝雀分批发布与回滚",
            workflow="complex-project-delivery", builder=build_operations, hidden_files=OPERATIONS_HIDDEN,
            prompt="对仓库内五个授权测试目标修改已有 internal/rollout/rollout.go 的 Run：按目标依次 Backup、Apply、Healthy。edge-04 健康失败时必须返回 error，Failed=edge-04，RolledBack=[edge-04]，StoppedBefore=[edge-05]，Applied 含已应用的 edge-01 到 edge-04，并且调用 Rollback(edge-04)；不得继续 edge-05，也不得回滚已验证的前三个目标。公开测试 TestStopsOnInjectedHealthFailure 已锁定该契约。交付 deliverables/rollout-result.json 和 docs/runbook.md，包含审批、备份、停止边界和恢复步骤。",
            verify_commands=(("go", "test", "./..."), ("go", "vet", "./...")), required_deliverables=("docs/runbook.md", "deliverables/rollout-result.json"),
            required_paths=("internal/inventory/inventory.go", "internal/rollout/rollout.go", "internal/health/summary.go", "snapshots/snapshots_test.go", "cmd/fleetops/main.go"),
            complexity_signals=("多目标依赖拓扑", "批次停止扩散", "失败目标精确回滚", "备份与健康检查"),
            variant_mutators=(variant_operations_latency, variant_operations_target_snapshot),
        ),
        P7ComplexCase(
            id="p7-research-source-verification", domain="research", title="十五来源冲突核验",
            workflow="parallel-research-review", builder=build_research, hidden_files=RESEARCH_HIDDEN,
            prompt="本机来源服务为 {base_url}，source-catalog.json 列出全部候选来源。批量核验至少十五个来源，保留 URL、canonical_url、发布日期 published_at、访问时间 accessed_at、内容 SHA-256 content_sha256、HTTP 状态 http_status 和结构化重定向 redirects；每条来源的 content_sha256 必须原样复制 source-catalog.json 中该来源的 content_sha256 字段（评估器以文件字节哈希为准，web 工具返回的是正文文本哈希，两者不同；禁止自行计算或推断）；evidence.json 的 sources 数组必须原样保留 source-catalog.json 中每条记录的稳定 id（例如 S001），不能重编号或改成 S1，并且每条 source 必须使用字段 id、url、canonical_url、published_at、accessed_at、content_sha256、http_status、redirects、source_type。去除转载与内容重复：evidence.json 的 sources 中每个转载/镜像来源必须用 duplicate_of 指向原始来源的稳定 id（mirror-release→official-release、mirror-notes→official-notes、duplicate-faq→faq），被转载方不得当作独立来源计数；建立原子事实映射，裁决冲突并明确无法确认项：C-conflict 必须同时给出 supporting_source_ids 与 opposing_source_ids，且 status 必须含“无法确认”，不得裁决成单一结论；C-deployments 不得给出 supporting_source_ids，status 必须含“无法确认”，因为没有任何来源支持具体部署数量；C-stable 的来源映射必须跟随当前 source-catalog.json，来源内容变化后必须重算 confidence；claims 数组必须使用字段 id、claim、supporting_source_ids、opposing_source_ids、status、confidence、limitation，事实 ID 使用 C-release、C-stable、C-conflict、C-migration、C-deployments。recovery 对象必须使用 source_id、attempt_statuses（removed-source 必须为 [503,200]）和 recovered=true。claims.csv 表头必须严格为 claim_id,claim,supporting_source_ids,opposing_source_ids,status,confidence,limitation，且每一行必须逐字由 evidence.json 的同名 claim 派生：claim 文本、status、confidence、limitation 完全一致，supporting_source_ids 与 opposing_source_ids 用分号连接且不留空格，顺序与 evidence.json 数组一致。evidence.json 中每个转载来源必须带 duplicate_of 字段，值为原始来源的 slug：mirror-release→official-release、mirror-notes→official-notes、duplicate-faq→faq（不是 S 编号）。C-stable 的 supporting_source_ids 必须同时包含 standard-stable 与 vendor-support 对应的 S 编号；两个来源都在时 confidence 必须是“中”，只剩一个时必须是“低”，不得写成 high/高/medium。removed-source 首次会返回 503，必须有界恢复且不能伪造内容；它的 http_status 必须记录恢复后的 200（首次 503 只写进 recovery.attempt_statuses=[503,200] 与 recovered=true），其余每个来源的 http_status 都必须是 200。必须写出三个不同格式文件：deliverables/evidence.json 为含 sources 和 claims 数组的合法 JSON，deliverables/claims.csv 为逗号分隔表，deliverables/report.md 为 Markdown，三者不得写入同一段正文。报告须逐字包含标记：Alpha 2.0 兼容性与稳定状态、来源去重、冲突、removed-source、503、无法确认。",
            required_deliverables=("deliverables/report.md", "deliverables/evidence.json", "deliverables/claims.csv"),
            required_paths=("source-catalog.json", "sources/official-release.html", "sources/removed-source.html", "README.md"),
            complexity_signals=("来源去重", "冲突裁决", "时间边界", "一次性 503 恢复"), kind="research",
            variant_mutators=(variant_research_reordered, variant_research_metadata),
        ),
        P7ComplexCase(
            id="p7-documents-template-suite", domain="documents", title="Office/WPS 模板组合交付",
            workflow="docs-review-publish", builder=build_documents_fixture, hidden_files=DOCUMENTS_HIDDEN,
            prompt="先读 inputs/request.json、inputs/merge-map.json 和 inputs/findings.csv 各一次。用 fill_office_template 从模板生成 deliverables：templates/security-report-template.docx → deliverables/report.docx、templates/remediation-data-template.xlsx → deliverables/data.xlsx、templates/review-template.pptx → deliverables/review.pptx（模板文件名必须逐字使用这三个，不得自行改名或猜测），禁止 create_docx/create_xlsx/create_pptx。DOCX 替换 {{CLIENT_NAME}}->星河科技、{{REPORT_TITLE}}->Alpha 平台安全整改交付报告、{{REPORT_DATE}}->2026-08-18，并写入 SEC-001、SEC-002。XLSX 把 findings.csv 合并到明细数据并保留模板的第二个工作表（汇总页）及其“完成率”公式、SUM(、自动筛选、冻结首行（A2）和打印设置，不得重建或丢弃汇总页。PPTX 写入星河科技与整改结果，保留 16:9 母版，并原样保留模板中的“固定母版声明：内部授权交付材料”标记。先对 security-report-template.docx 做一次缺失占位符 {{INTENTIONALLY_MISSING_TOKEN}} 的 replace_office_text，确认失败且模板哈希不变。每个模板 inspect 一次，每个输入只 read_file 一次。validate 后 export_office_pdf 到 deliverables/report.pdf，render_office_preview 到 deliverables/preview/。",
            required_deliverables=("deliverables/report.docx", "deliverables/data.xlsx", "deliverables/review.pptx", "deliverables/report.pdf"),
            required_paths=("templates/security-report-template.docx", "templates/remediation-data-template.xlsx", "templates/review-template.pptx", "inputs/findings.csv", "fixture-manifest.json"),
            complexity_signals=("模板保真", "批量修改原子失败", "公式与打印设置", "PDF 与逐页预览"), kind="documents",
            variant_mutators=(variant_documents_findings, variant_documents_date),
        ),
        P7ComplexCase(
            id="p7-cross-domain-unified-delivery", domain="cross-domain", title="代码、安全、部署和正式报告统一交付",
            workflow="complex-project-delivery", builder=build_cross_domain, hidden_files=CROSS_DOMAIN_HIDDEN,
            prompt="只选择开发、安全、部署和文档必要分支。必须实际修改：internal/session/store.go 的 Put/Get 对 mu 加锁；internal/token/token.go 用恒定时间比较摘要；internal/deploy/config.go 的 Parse 把 Version<2 升为 2，并把空或 /health 的 HealthPath 改为 /healthz。完成真实测试和静态检查后整体覆盖写入 docs/unified-delivery.md（不要保留任何占位内容）。每个报告结论必须引用真实 Diff 或验证证据，不允许某一分支失败被其他分支掩盖。",
            verify_commands=(("go", "test", "./..."), ("go", "vet", "./...")), required_deliverables=("docs/unified-delivery.md",),
            required_paths=("internal/token/token.go", "internal/session/store.go", "internal/deploy/config.go", "deploy/service.json", "cmd/service/main.go"),
            complexity_signals=("按需分支路由", "并发状态", "安全比较", "部署迁移与统一报告"),
            variant_mutators=(variant_cross_domain_port, variant_cross_domain_health),
            # 47–60 次调用是这个四分支案例的真实量级，与开发案例同一口径。
            llm_call_budget=60,
            artifact_budget=60,
        ),
    ]


def install_hidden(case: P7ComplexCase, root: Path) -> None:
    for relative, content in case.hidden_files.items():
        write(root, relative, content)
