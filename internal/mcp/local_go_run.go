package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const localMCPBuildTimeout = 2 * time.Minute

var localMCPBuildMu sync.Mutex

// prepareLocalGoRunCommand replaces the repository's built-in `go run`
// development commands with a source-addressed binary in a stable cache.
// Custom commands and release binaries pass through unchanged.
func prepareLocalGoRunCommand(command string, args []string, workDir string, env []string) (string, []string, error) {
	packagePath, runtimeArgs, ok := localGoRunSpec(command, args)
	if !ok {
		return command, append([]string(nil), args...), nil
	}
	absRoot, packageDir, buildPackage, err := resolveLocalMCPPackage(workDir, packagePath)
	if err != nil {
		return "", nil, err
	}
	fingerprint, err := localGoPackageFingerprint(absRoot, packageDir)
	if err != nil {
		return "", nil, err
	}
	name := filepath.Base(packageDir)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	cacheDir, err := localMCPBinaryCacheDir(absRoot)
	if err != nil {
		return "", nil, err
	}
	target := filepath.Join(cacheDir, strings.TrimSuffix(name, filepath.Ext(name))+"-"+fingerprint[:16]+filepath.Ext(name))
	if regularFile(target) {
		return target, runtimeArgs, nil
	}

	localMCPBuildMu.Lock()
	defer localMCPBuildMu.Unlock()
	if regularFile(target) {
		return target, runtimeArgs, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create MCP binary cache: %w", err)
	}
	tempName := fmt.Sprintf(".%s-%d.tmp", filepath.Base(target), os.Getpid())
	if runtime.GOOS == "windows" {
		tempName += ".exe"
	}
	tempPath := filepath.Join(cacheDir, tempName)
	_ = os.Remove(tempPath)
	ctx, cancel := context.WithTimeout(context.Background(), localMCPBuildTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, command, "build", "-buildvcs=false", "-o", tempPath, buildPackage)
	build.Dir = absRoot
	build.Env = env
	output, buildErr := build.CombinedOutput()
	if buildErr != nil {
		_ = os.Remove(tempPath)
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("build cached MCP server %s: %w", packagePath, ctx.Err())
		}
		return "", nil, fmt.Errorf("build cached MCP server %s: %w: %s", packagePath, buildErr, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(tempPath, target); err != nil {
		if !regularFile(target) {
			_ = os.Remove(tempPath)
			return "", nil, fmt.Errorf("publish cached MCP server %s: %w", packagePath, err)
		}
		_ = os.Remove(tempPath)
	}
	return target, runtimeArgs, nil
}

func localMCPBinaryCacheDir(absRoot string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("GOFLOW_MCP_BIN_CACHE_DIR")); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("GOFLOW_MCP_BIN_CACHE_DIR must be an absolute path")
		}
		return filepath.Clean(override), nil
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		return filepath.Join(absRoot, ".runtime-mcp-bin"), nil
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absRoot))))
	return filepath.Join(cacheRoot, "GoFlow", "mcp-bin", fmt.Sprintf("%x", digest[:8])), nil
}

func localGoRunSpec(command string, args []string) (string, []string, bool) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.TrimSpace(command)), ".exe"))
	if name != "go" || len(args) < 2 || strings.TrimSpace(args[0]) != "run" {
		return "", nil, false
	}
	packagePath := strings.TrimSpace(args[1])
	normalized := filepath.ToSlash(packagePath)
	if strings.HasSuffix(strings.ToLower(normalized), ".go") || hasParentPathSegment(normalized) {
		return "", nil, false
	}
	if filepath.IsAbs(packagePath) {
		if !pathContainsMCPServerPackage(packagePath) {
			return "", nil, false
		}
	} else if !strings.HasPrefix(normalized, "./mcp_servers/") {
		return "", nil, false
	}
	return packagePath, append([]string(nil), args[2:]...), true
}

func resolveLocalMCPPackage(workDir, packagePath string) (string, string, string, error) {
	base := strings.TrimSpace(workDir)
	if base == "" {
		base = "."
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve MCP build root: %w", err)
	}
	packageDir := filepath.Clean(packagePath)
	if !filepath.IsAbs(packageDir) {
		packageDir = filepath.Join(absBase, packageDir)
	}
	root, ok := findGoModuleRoot(packageDir)
	if !ok {
		root, ok = findGoModuleRoot(absBase)
	}
	if !ok {
		return "", "", "", fmt.Errorf("locate Go module for built-in MCP package %s", packageDir)
	}
	relative, err := filepath.Rel(root, packageDir)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve built-in MCP package path: %w", err)
	}
	normalized := filepath.ToSlash(relative)
	if !strings.HasPrefix(normalized, "mcp_servers/") || hasParentPathSegment(normalized) {
		return "", "", "", fmt.Errorf("built-in MCP package escapes the module mcp_servers directory: %s", packageDir)
	}
	return root, packageDir, "./" + normalized, nil
}

func findGoModuleRoot(start string) (string, bool) {
	current := filepath.Clean(start)
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if regularFile(filepath.Join(current, "go.mod")) {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func pathContainsMCPServerPackage(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for index := 0; index+1 < len(parts); index++ {
		if parts[index] == "mcp_servers" && strings.TrimSpace(parts[index+1]) != "" {
			return true
		}
	}
	return false
}

func hasParentPathSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func localGoPackageFingerprint(root, packageDir string) (string, error) {
	paths := make([]string, 0, 16)
	for _, name := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(root, name)
		if regularFile(path) {
			paths = append(paths, path)
		}
	}
	err := filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect built-in MCP source %s: %w", packageDir, err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("built-in MCP source has no Go files: %s", packageDir)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read MCP build input %s: %w", path, err)
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
