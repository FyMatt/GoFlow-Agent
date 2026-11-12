#!/usr/bin/env python3
"""Trusted reference solutions used only to prove P7 acceptance fixtures."""

from __future__ import annotations

import csv
import hashlib
import json
import re
import subprocess
from pathlib import Path

from p7_complex_cases import P7ComplexCase, write, write_json


def solve_development(root: Path) -> None:
    write(root, "internal/config/config.go", r'''package config

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
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Version < CurrentVersion {
		cfg.Version = CurrentVersion
	}
	if cfg.CacheTTLSeconds <= 0 {
		cfg.CacheTTLSeconds = 30
	}
	if cfg.UITheme == "" {
		cfg.UITheme = "system"
	}
	return cfg, nil
}

func Save(cfg Config) ([]byte, error) {
	cfg.Version = CurrentVersion
	return json.MarshalIndent(cfg, "", "  ")
}
''')
    write(root, "internal/cache/cache.go", r'''package cache

import (
	"sync"
	"time"
)

type entry struct {
	value   []byte
	expires time.Time
}

type Cache struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]entry
}

func New(now func() time.Time) *Cache {
	return &Cache{now: now, items: map[string]entry{}}
}

func (c *Cache) Set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry{value: append([]byte(nil), value...), expires: c.now().Add(ttl)}
}

func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || !c.now().Before(item.expires) {
		delete(c.items, key)
		return nil, false
	}
	return append([]byte(nil), item.value...), true
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, item := range c.items {
		if !now.Before(item.expires) {
			delete(c.items, key)
		}
	}
	return len(c.items)
}
''')
    write(root, "package.json", '{"type":"module"}\n')
    write(
        root,
        "web/app.js",
        'export function renderStatus(status) { const state=status.state==="ready"?"就绪":"异常"; return `${state} · 缓存 ${status.cached_items} · 配置 v${status.config_version}`; }\n',
    )
    write(
        root,
        "web/app.test.js",
        'import {renderStatus} from "./app.js"; const got=renderStatus({state:"ready",cached_items:2,config_version:2}); if(got!=="就绪 · 缓存 2 · 配置 v2") throw new Error(got);\n',
    )
    write(
        root,
        "docs/upgrade.md",
        "# 配置 v2 升级与回滚\n\n首次读取 v1 或无版本配置时保留名称并补齐 TTL、主题和版本字段。写回前备份原配置；回滚时停止服务、恢复备份并重新启动。\n\n验证：`go test ./...`、`go test -race ./...`、`go vet ./...` 和页面状态测试。\n",
    )


def solve_security(root: Path) -> None:
    write(root, "internal/auth/auth.go", r'''package auth

type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

func Allowed(role Role, action string) bool {
	grants := map[Role]map[string]bool{
		RoleViewer: {"read": true},
		RoleEditor: {"read": true, "write": true},
		RoleAdmin:  {"read": true, "write": true, "admin": true},
	}
	return grants[role][action]
}
''')
    write(root, "internal/filesafe/path.go", r'''package filesafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func Resolve(root, userPath string) (string, error) {
	if userPath == "" || filepath.IsAbs(userPath) {
		return "", errors.New("path must be relative")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Clean(filepath.Join(root, userPath))
	if !inside(root, joined) {
		return "", errors.New("path escapes workspace")
	}
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(joined, root), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !inside(root, resolved) {
				return "", errors.New("symlink escapes workspace")
			}
			current = resolved
		}
	}
	return joined, nil
}
''')
    write(root, "internal/request/header.go", r'''package request

import (
	"errors"
	"strings"
)

func NormalizeHeader(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("invalid control character")
	}
	return strings.TrimSpace(value), nil
}
''')
    write(root, "internal/audit/redact.go", r'''package audit

import (
	"regexp"
	"strings"
)

var secret = regexp.MustCompile(`(?i)(token|password|api_key)=([^\s]+)`)

func Redact(line string) string {
	return strings.TrimSpace(secret.ReplaceAllString(line, "$1=[REDACTED]"))
}
''')
    write(
        root,
        "deliverables/security-report.md",
        "# 授权安全评估与复测\n\n范围仅限本地夹具。证据确认授权默认放行、路径穿越/符号链接逃逸、请求头注入和审计日志泄密。修复采用显式授权矩阵、工作区边界、控制字符拒绝和字段脱敏；公开与隐藏测试及 `go vet ./...` 为复测证据。未执行主动攻击，未发现需要扩大授权范围的剩余项。\n",
    )


def solve_operations(root: Path) -> None:
    write(root, "internal/rollout/rollout.go", r'''package rollout

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
	for index, target := range targets {
		if err := exec.Backup(ctx, target); err != nil {
			out.Failed = target.ID
			out.StoppedBefore = remaining(targets, index+1)
			return out, err
		}
		if err := exec.Apply(ctx, target); err != nil {
			out.Failed = target.ID
			out.StoppedBefore = remaining(targets, index+1)
			return out, err
		}
		out.Applied = append(out.Applied, target.ID)
		if err := exec.Healthy(ctx, target); err != nil {
			out.Failed = target.ID
			out.StoppedBefore = remaining(targets, index+1)
			if rollbackErr := exec.Rollback(ctx, target); rollbackErr == nil {
				out.RolledBack = append(out.RolledBack, target.ID)
			}
			return out, err
		}
	}
	return out, nil
}

func remaining(targets []inventory.Target, start int) []string {
	result := make([]string, 0, len(targets)-start)
	for _, target := range targets[start:] {
		result = append(result, target.ID)
	}
	return result
}
''')
    write(
        root,
        "docs/runbook.md",
        "# 五目标分批发布与恢复运行手册\n\n变更前确认任务级审批并逐目标备份。edge-01 为金丝雀，edge-02/03 为第二批，edge-04/05 为第三批。每个目标依次执行备份、应用和健康检查；edge-04 健康失败后立即停止 edge-05，只回滚 edge-04，保留前三个已验证目标。恢复后重新检查版本、健康状态和延迟，不重复前三个目标的副作用操作。\n",
    )
    write_json(
        root,
        "deliverables/rollout-result.json",
        {
            "approved": True,
            "backups": ["edge-01", "edge-02", "edge-03", "edge-04"],
            "applied": ["edge-01", "edge-02", "edge-03", "edge-04"],
            "verified": ["edge-01", "edge-02", "edge-03"],
            "failed": "edge-04",
            "rolled_back": ["edge-04"],
            "stopped_before": ["edge-05"],
        },
    )


def solve_cross_domain(root: Path) -> None:
    write(root, "internal/token/token.go", r'''package token

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func Equal(stored, value string) bool {
	expected := []byte(Digest(value))
	actual := []byte(stored)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}
''')
    write(root, "internal/session/store.go", r'''package session

import "sync"

type Store struct {
	mu    sync.RWMutex
	items map[string]string
}

func New() *Store { return &Store{items: map[string]string{}} }

func (s *Store) Put(id, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = value
}

func (s *Store) Get(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.items[id]
	return value, ok
}
''')
    write(root, "internal/deploy/config.go", r'''package deploy

import "encoding/json"

type Config struct {
	Version    int    `json:"version"`
	Port       int    `json:"port"`
	HealthPath string `json:"health_path"`
}

func Parse(data []byte) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Version < 2 {
		cfg.Version = 2
	}
	if cfg.HealthPath == "" || cfg.HealthPath == "/health" {
		cfg.HealthPath = "/healthz"
	}
	return cfg, nil
}
''')
    write(
        root,
        "docs/unified-delivery.md",
        "# 代码、安全、部署统一交付\n\n开发分支通过并发会话存储测试；安全分支改为常量时间摘要比较；部署分支将 v1 配置迁移到 v2 并保留显式端口。所有结论分别由源码 Diff、`go test -race ./...` 和 `go vet ./...` 支持。任一分支失败都会阻断统一交付；回滚时恢复原部署配置和上一个发布包。\n",
    )


def _html_text(path: Path) -> str:
    text = path.read_text(encoding="utf-8")
    return re.sub(r"\s+", " ", re.sub(r"<[^>]+>", " ", text)).strip()


def solve_research(root: Path) -> None:
    catalog = json.loads((root / "source-catalog.json").read_text(encoding="utf-8"))
    duplicate_of = {
        "mirror-release": "official-release",
        "mirror-notes": "official-notes",
        "duplicate-faq": "faq",
    }
    sources = []
    by_slug: dict[str, str] = {}
    for item in catalog["sources"]:
        path = root / item["relative_path"]
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        by_slug[item["slug"]] = item["id"]
        sources.append(
            {
                "id": item["id"],
                "slug": item["slug"],
                "url": f"http://127.0.0.1/reference/{item['slug']}.html",
                "published_at": item.get("published_at", ""),
                "accessed_at": "2026-08-18T12:00:00Z",
                "content_sha256": digest,
                "http_status": 200,
                "redirects": [],
                "source_type": item["source_type"],
                "claim_group": item["claim_group"],
                "duplicate_of": duplicate_of.get(item["slug"], ""),
                "excerpt": _html_text(path)[:500],
            }
        )

    stable_support = [slug for slug in ("standard-stable", "vendor-support") if slug in by_slug]
    claims = [
        {
            "id": "C-release",
            "claim": "Alpha 2.0 已正式发布并提供结构化导出。",
            "supporting_source_ids": [by_slug[slug] for slug in ("official-release", "official-notes", "release-index") if slug in by_slug],
            "opposing_source_ids": [],
            "status": "已核验",
            "confidence": "高",
            "limitation": "同一发布主体的材料不按三个独立来源计算。",
        },
        {
            "id": "C-stable",
            "claim": "Alpha 2.0 已进入稳定发布阶段。",
            "supporting_source_ids": [by_slug[slug] for slug in stable_support],
            "opposing_source_ids": [],
            "status": "已核验" if len(stable_support) >= 2 else "证据不足",
            "confidence": "中" if len(stable_support) >= 2 else "低",
            "limitation": "置信度随独立稳定性来源数量变化。",
        },
        {
            "id": "C-conflict",
            "claim": "Alpha 2.0 是否已被所有使用者视为成熟版本。",
            "supporting_source_ids": [by_slug[slug] for slug in ("standard-stable", "vendor-support") if slug in by_slug],
            "opposing_source_ids": [by_slug[slug] for slug in ("community-old", "blog-opinion") if slug in by_slug],
            "status": "存在冲突，无法确认",
            "confidence": "低",
            "limitation": "社区旧记录和观点不能证明当前总体使用状态。",
        },
        {
            "id": "C-migration",
            "claim": "旧配置首次加载时迁移到 v2。",
            "supporting_source_ids": [by_slug["faq"]] if "faq" in by_slug else [],
            "opposing_source_ids": [],
            "status": "单一来源",
            "confidence": "中",
            "limitation": "转载 FAQ 已去重，不增加独立置信度。",
        },
        {
            "id": "C-deployments",
            "claim": "Alpha 2.0 的实际部署数量。",
            "supporting_source_ids": [],
            "opposing_source_ids": [],
            "status": "无法确认",
            "confidence": "低",
            "limitation": "候选来源没有部署数量证据。",
        },
    ]
    evidence = {
        "schema_version": 1,
        "topic": catalog["topic"],
        "sources": sources,
        "claims": claims,
        "recovery": {"source_id": by_slug.get("removed-source"), "attempt_statuses": [503, 200], "recovered": True},
    }
    write_json(root, "deliverables/evidence.json", evidence)
    csv_path = root / "deliverables/claims.csv"
    csv_path.parent.mkdir(parents=True, exist_ok=True)
    with csv_path.open("w", encoding="utf-8", newline="") as stream:
        writer = csv.DictWriter(
            stream,
            fieldnames=("claim_id", "claim", "supporting_source_ids", "opposing_source_ids", "status", "confidence", "limitation"),
        )
        writer.writeheader()
        for claim in claims:
            writer.writerow(
                {
                    "claim_id": claim["id"],
                    "claim": claim["claim"],
                    "supporting_source_ids": ";".join(claim["supporting_source_ids"]),
                    "opposing_source_ids": ";".join(claim["opposing_source_ids"]),
                    "status": claim["status"],
                    "confidence": claim["confidence"],
                    "limitation": claim["limitation"],
                }
            )
    report_lines = [
        f"# {catalog['topic']}核验报告",
        "",
        "## 结论与原子事实",
    ]
    report_lines.extend(f"- {claim['id']}：{claim['claim']} 状态：{claim['status']}；置信度：{claim['confidence']}。{claim['limitation']}" for claim in claims)
    report_lines.extend(
        [
            "",
            "## 来源去重与冲突",
            "mirror-release、mirror-notes 和 duplicate-faq 为转载或内容重复，不增加独立来源数量。C-conflict 同时保留支持与反对来源，结论为无法确认。",
            "",
            "## 失败恢复与限制",
            "removed-source 首次返回 503，第二次返回 200；仅记录实际恢复，不推断长期可用性。每条来源的 URL、发布日期、访问时间、内容哈希和重定向保存在 evidence.json。",
        ]
    )
    write(root, "deliverables/report.md", "\n".join(report_lines) + "\n")


SOLVERS = {
    "p7-development-platform-migration": solve_development,
    "p7-security-defensive-remediation": solve_security,
    "p7-operations-batched-rollout": solve_operations,
    "p7-research-source-verification": solve_research,
    "p7-cross-domain-unified-delivery": solve_cross_domain,
}


def apply_reference_solution(case: P7ComplexCase, root: Path) -> None:
    solver = SOLVERS.get(case.id)
    if solver is None:
        raise KeyError(f"no reference solution for {case.id}")
    solver(root)
    if case.id == "p7-development-platform-migration":
        subprocess.run(["gofmt", "-w", "."], cwd=root, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
