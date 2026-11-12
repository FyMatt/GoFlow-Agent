package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func mustWorkspaceRoot() string {
	root := strings.TrimSpace(os.Getenv("GOFLOW_WORKSPACE_ROOT"))
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			panic(err)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		panic(err)
	}
	return filepath.Clean(abs)
}

func resolveWorkspacePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspaceRoot, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析路径失败：%w", err)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(workspaceRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("路径必须位于当前工作区内")
	}
	return abs, nil
}

func relativeWorkspacePath(path string) string {
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func stringArg(args map[string]any, name string, required bool) (string, error) {
	value, _ := args[name].(string)
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s 不能为空", name)
	}
	return value, nil
}

func boolArg(args map[string]any, name string, fallback bool) bool {
	value, ok := args[name].(bool)
	if !ok {
		return fallback
	}
	return value
}

func intArg(args map[string]any, name string, fallback, minValue, maxValue int) int {
	value := fallback
	switch raw := args[name].(type) {
	case float64:
		value = int(raw)
	case int:
		value = raw
	case string:
		if parsed, err := strconv.Atoi(raw); err == nil {
			value = parsed
		}
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

type writeTarget struct {
	path       string
	backupPath string
	mode       os.FileMode
}

func prepareWriteTarget(args map[string]any, expectedExt string) (writeTarget, error) {
	raw, err := stringArg(args, "path", true)
	if err != nil {
		return writeTarget{}, err
	}
	path, err := resolveWorkspacePath(raw)
	if err != nil {
		return writeTarget{}, err
	}
	if expectedExt != "" && !strings.EqualFold(filepath.Ext(path), expectedExt) {
		return writeTarget{}, fmt.Errorf("产物路径必须使用 %s 扩展名", expectedExt)
	}
	if err := ensureNoSymlinkParent(path); err != nil {
		return writeTarget{}, err
	}
	target := writeTarget{path: path, mode: 0o644}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return target, nil
	}
	if err != nil {
		return writeTarget{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		return writeTarget{}, fmt.Errorf("拒绝覆盖目录或符号链接")
	}
	if !boolArg(args, "overwrite", false) {
		return writeTarget{}, fmt.Errorf("文件已存在；确认覆盖时请设置 overwrite=true")
	}
	expected, _ := args["expected_sha256"].(string)
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return writeTarget{}, fmt.Errorf("覆盖现有文件时必须提供 expected_sha256")
	}
	actual, _, err := fileSHA256(path)
	if err != nil {
		return writeTarget{}, err
	}
	if actual != expected {
		return writeTarget{}, fmt.Errorf("文件已被其他操作修改：SHA-256 不匹配")
	}
	target.mode = info.Mode().Perm()
	backupDir := filepath.Join(workspaceRoot, ".goflow", "backups", "office")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return writeTarget{}, err
	}
	target.backupPath = filepath.Join(backupDir, fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405.000000000"), filepath.Base(path)))
	return target, nil
}

func ensureNoSymlinkParent(path string) error {
	current := filepath.Dir(path)
	for {
		if current == workspaceRoot {
			return nil
		}
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝通过符号链接写入工作区")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("路径不在工作区内")
		}
		current = parent
	}
}

func (target writeTarget) commit(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target.path), ".goflow-office-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(target.mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if target.backupPath != "" {
		if err := os.Rename(target.path, target.backupPath); err != nil {
			return fmt.Errorf("创建修改前备份失败：%w", err)
		}
	}
	if err := os.Rename(tmpPath, target.path); err != nil {
		if target.backupPath != "" {
			_ = os.Rename(target.backupPath, target.path)
		}
		return fmt.Errorf("保存产物失败：%w", err)
	}
	cleanup = false
	return nil
}

func artifactPayload(path, mime string, extra map[string]any) (map[string]any, error) {
	hash, size, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"artifact_path": relativeWorkspacePath(path),
		"mime":          mime,
		"sha256":        hash,
		"size":          size,
	}
	for key, value := range extra {
		result[key] = value
	}
	return result, nil
}
