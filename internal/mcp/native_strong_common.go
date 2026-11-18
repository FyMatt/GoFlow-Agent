package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/dependencycache"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const nativeSandboxCLIFlag = "--internal-native-sandbox-spec"

const (
	nativeStrongScriptMaxFiles = 4096
	nativeStrongScriptMaxBytes = int64(256 << 20)
)

func (c *Client) nativeStrongTaskScope(command string) (schema.TaskWorkspaceScope, bool, error) {
	if strings.TrimSpace(c.taskScope.Root) != "" {
		scope, err := normalizeMCPTaskWorkspaceScope(c.taskScope)
		if err == nil && strings.TrimSpace(scope.Dependencies) == "" {
			scope.Dependencies = filepath.Join(scope.Root, "dependencies")
			err = os.MkdirAll(scope.Dependencies, 0o700)
		}
		return scope, false, err
	}
	base := strings.TrimSpace(c.config.RuntimeHome)
	if base == "" {
		base = os.TempDir()
	}
	base, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return schema.TaskWorkspaceScope{}, false, fmt.Errorf("resolve native MCP discovery root: %w", err)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(c.name) + "\x00" + strings.TrimSpace(command))))
	discoveryRoot := filepath.Join(base, "native-mcp-discovery")
	if err := os.MkdirAll(discoveryRoot, 0o700); err != nil {
		return schema.TaskWorkspaceScope{}, false, fmt.Errorf("create native MCP discovery parent: %w", err)
	}
	root, err := os.MkdirTemp(discoveryRoot, fmt.Sprintf("%x-", digest[:6]))
	if err != nil {
		return schema.TaskWorkspaceScope{}, false, fmt.Errorf("create native MCP discovery root: %w", err)
	}
	input, workspace := filepath.Join(root, "input"), filepath.Join(root, "workspace")
	dependencies := filepath.Join(root, "dependencies")
	for _, path := range []string{input, dependencies, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return schema.TaskWorkspaceScope{}, false, fmt.Errorf("create native MCP discovery directory: %w", err)
		}
	}
	return schema.TaskWorkspaceScope{RunID: fmt.Sprintf("mcp-discovery-%x", digest[:8]), Root: root, Input: input, Dependencies: dependencies, Workspace: workspace}, true, nil
}

func resolveNativeStrongCommand(command string) (string, error) {
	resolved, err := exec.LookPath(strings.TrimSpace(command))
	if err != nil {
		return "", fmt.Errorf("resolve native MCP executable: %w", err)
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "", fmt.Errorf("resolve native MCP executable path: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve native MCP executable links: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect native MCP executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("native MCP command is not a regular executable")
	}
	return resolved, nil
}

func (c *Client) cacheNativeStrongFile(source, destinationDir string) (string, error) {
	store, err := c.nativeStrongDependencyStore()
	if err != nil {
		return "", err
	}
	artifact, err := store.PutFile(source)
	if err != nil {
		return "", err
	}
	return store.Materialize(artifact, destinationDir, artifact.SHA256[:16]+"-"+filepath.Base(source))
}

func (c *Client) cacheNativeStrongExecutable(source, destinationDir string) (string, error) {
	store, err := c.nativeStrongDependencyStore()
	if err != nil {
		return "", err
	}
	artifact, err := store.PutFile(source)
	if err != nil {
		return "", err
	}
	return store.MaterializeExecutable(artifact, destinationDir, artifact.SHA256[:16]+"-"+filepath.Base(source))
}

func (c *Client) nativeStrongDependencyStore() (*dependencycache.Store, error) {
	cacheRoot := strings.TrimSpace(c.config.RuntimeHome)
	if cacheRoot == "" {
		cacheRoot = os.TempDir()
	}
	return dependencycache.New(filepath.Join(cacheRoot, "dependency-cache"))
}

func (c *Client) stageNativeStrongArguments(args []string, scope schema.TaskWorkspaceScope) ([]string, error) {
	if strings.TrimSpace(scope.Dependencies) == "" {
		return append([]string(nil), args...), nil
	}
	staged := append([]string(nil), args...)
	for index, argument := range staged {
		original := strings.Trim(strings.TrimSpace(argument), "\"")
		prefix := ""
		candidate := original
		if equal := strings.Index(candidate, "="); equal >= 0 {
			prefix = candidate[:equal+1]
			candidate = strings.Trim(strings.TrimSpace(candidate[equal+1:]), "\"")
		}
		if candidate == "" {
			continue
		}
		resolved := candidate
		if !filepath.IsAbs(resolved) && strings.TrimSpace(c.workDir) != "" {
			resolved = filepath.Join(c.workDir, resolved)
		}
		resolved, err := filepath.Abs(filepath.Clean(resolved))
		if err != nil {
			continue
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if nativeStrongScriptPath(resolved) {
			materialized, err := c.stageNativeStrongScriptBundle(resolved, scope.Dependencies)
			if err != nil {
				return nil, fmt.Errorf("cache native MCP script bundle %s: %w", filepath.Base(resolved), err)
			}
			staged[index] = prefix + materialized
			continue
		}
		materialized, err := c.cacheNativeStrongFile(resolved, scope.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("cache native MCP argument %s: %w", filepath.Base(resolved), err)
		}
		staged[index] = prefix + materialized
	}
	return staged, nil
}

type nativeStrongScriptFile struct {
	relative string
	artifact dependencycache.Artifact
}

func (c *Client) stageNativeStrongScriptBundle(entrypoint, dependencies string) (string, error) {
	root := filepath.Dir(entrypoint)
	store, err := c.nativeStrongDependencyStore()
	if err != nil {
		return "", err
	}
	files := make([]nativeStrongScriptFile, 0, 32)
	var total int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if nativeStrongScriptIgnoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("script bundle contains unsupported path: %s", relative)
		}
		if nativeStrongScriptSensitiveFile(entry.Name()) {
			return nil
		}
		if len(files) >= nativeStrongScriptMaxFiles {
			return fmt.Errorf("script bundle exceeds %d files; package the MCP in a dedicated directory", nativeStrongScriptMaxFiles)
		}
		total += info.Size()
		if total > nativeStrongScriptMaxBytes {
			return fmt.Errorf("script bundle exceeds %d bytes; package the MCP in a dedicated directory", nativeStrongScriptMaxBytes)
		}
		artifact, putErr := store.PutFile(path)
		if putErr != nil {
			return putErr
		}
		files = append(files, nativeStrongScriptFile{relative: filepath.Clean(relative), artifact: artifact})
		return nil
	})
	if err != nil {
		return "", err
	}
	entryRelative, err := filepath.Rel(root, entrypoint)
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	hash := sha256.New()
	entryFound := false
	for _, file := range files {
		if file.relative == entryRelative {
			entryFound = true
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(file.relative)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.artifact.SHA256))
		_, _ = hash.Write([]byte{0})
	}
	if !entryFound {
		return "", fmt.Errorf("script entrypoint was excluded from its dependency bundle")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	bundleRoot := filepath.Join(dependencies, "scripts", digest[:16]+"-"+nativeStrongName(filepath.Base(root)))
	for _, file := range files {
		if _, err := store.MaterializeRelative(file.artifact, bundleRoot, file.relative); err != nil {
			return "", err
		}
	}
	if err := ensureNativeStrongScriptPackageBoundary(bundleRoot, entryRelative); err != nil {
		return "", err
	}
	return filepath.Join(bundleRoot, entryRelative), nil
}

func ensureNativeStrongScriptPackageBoundary(bundleRoot, entryRelative string) error {
	switch strings.ToLower(filepath.Ext(entryRelative)) {
	case ".js", ".cjs", ".mjs", ".ts", ".cts", ".mts":
	default:
		return nil
	}
	packagePath := filepath.Join(bundleRoot, "package.json")
	info, err := os.Stat(packagePath)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("script bundle package boundary is not a regular file")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect script bundle package boundary: %w", err)
	}
	if err := os.WriteFile(packagePath, []byte("{\"private\":true}\n"), 0o600); err != nil {
		return fmt.Errorf("create script bundle package boundary: %w", err)
	}
	return nil
}

func nativeStrongScriptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".pyw", ".js", ".cjs", ".mjs", ".ts", ".cts", ".mts", ".sh", ".ps1", ".cmd", ".bat":
		return true
	default:
		return false
	}
}

func nativeStrongScriptIgnoredDirectory(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".hg", ".svn", ".goflow", ".cache", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".idea", ".vscode":
		return true
	default:
		return false
	}
}

func nativeStrongScriptSensitiveFile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == ".env.example" || name == ".env.sample" || name == ".env.template" {
		return false
	}
	return name == ".env" || strings.HasPrefix(name, ".env.") || name == "id_rsa" || name == "id_ed25519" || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".pfx") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".key")
}

func validateNativeStrongArguments(args []string, scope schema.TaskWorkspaceScope, toolDir string) error {
	allowed := []string{scope.Input, scope.Dependencies, scope.Workspace, toolDir}
	for _, argument := range args {
		candidate := strings.Trim(strings.TrimSpace(argument), "\"")
		if index := strings.Index(candidate, "="); index >= 0 {
			candidate = strings.Trim(strings.TrimSpace(candidate[index+1:]), "\"")
		}
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		candidate = filepath.Clean(candidate)
		permitted := false
		for _, root := range allowed {
			if strings.TrimSpace(root) != "" && mcpPathWithin(root, candidate) {
				permitted = true
				break
			}
		}
		if !permitted {
			return fmt.Errorf("native MCP argument path escapes the task boundary: %s", candidate)
		}
	}
	return nil
}

func nativeStrongRunID(scope schema.TaskWorkspaceScope, serverName, command string) string {
	if runID := strings.TrimSpace(scope.RunID); runID != "" {
		return runID + "-mcp-" + nativeStrongName(serverName)
	}
	digest := sha256.Sum256([]byte(serverName + "\x00" + command + "\x00" + scope.Root))
	return fmt.Sprintf("mcp-%x", digest[:12])
}

func nativeStrongName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
	}
	if builder.Len() == 0 {
		return "server"
	}
	return builder.String()
}

func removeNativeStrongPath(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
