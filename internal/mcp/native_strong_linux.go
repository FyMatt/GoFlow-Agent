//go:build linux

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
)

func (c *Client) buildNativeStrongServerCommand(command string, args []string) (*exec.Cmd, func() error, error) {
	scope, ownedRoot, err := c.nativeStrongTaskScope(command)
	if err != nil {
		return nil, nil, err
	}
	cleanupRoot := func() error { return nil }
	if ownedRoot {
		cleanupRoot = func() error { return os.RemoveAll(scope.Root) }
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = cleanupRoot()
		}
	}()

	toolParent := filepath.Join(scope.Root, "native-tools")
	if err := os.MkdirAll(toolParent, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create native MCP tool root: %w", err)
	}
	toolDir, err := os.MkdirTemp(toolParent, nativeStrongName(c.name)+"-")
	if err != nil {
		return nil, nil, fmt.Errorf("create native MCP tool directory: %w", err)
	}
	cleanupTool := func() error { return os.RemoveAll(toolDir) }
	defer func() {
		if !prepared {
			_ = cleanupTool()
		}
	}()

	resolvedCommand, err := resolveNativeStrongCommand(command)
	if err != nil {
		return nil, nil, err
	}
	stagedCommand, err := c.cacheNativeStrongExecutable(resolvedCommand, toolDir)
	if err != nil {
		return nil, nil, err
	}
	stagedArgs, err := c.stageNativeStrongArguments(args, scope)
	if err != nil {
		return nil, nil, err
	}
	if err := validateNativeStrongArguments(stagedArgs, scope, toolDir); err != nil {
		return nil, nil, err
	}

	taskID := nativeStrongRunID(scope, c.name, command)
	cgroupPath, _, err := nativesandbox.LinuxTaskCgroupPath(taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare Linux MCP cgroup: %w", err)
	}
	readOnlyRoots := []string{toolDir, scope.Dependencies}
	readOnlyRoots = append(readOnlyRoots, linuxNativeStrongLibraryRoots(c.env)...)
	if strings.TrimSpace(scope.Input) != "" {
		readOnlyRoots = append(readOnlyRoots, scope.Input)
	}
	launch := nativesandbox.LinuxSandboxProcessSpec{
		TaskID:          taskID,
		Command:         stagedCommand,
		Args:            append([]string(nil), stagedArgs...),
		Env:             append([]string(nil), c.env...),
		WorkDir:         scope.Workspace,
		ReadOnlyRoots:   readOnlyRoots,
		WritableRoot:    scope.Workspace,
		NetworkDisabled: true,
	}
	metadataDir := filepath.Join(scope.Root, "metadata")
	if err := os.MkdirAll(metadataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create native MCP metadata directory: %w", err)
	}
	launchFile, err := os.CreateTemp(metadataDir, "native-mcp-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("create native MCP launch spec: %w", err)
	}
	launchPath := launchFile.Name()
	encoder := json.NewEncoder(launchFile)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(launch); err != nil {
		_ = launchFile.Close()
		_ = os.Remove(launchPath)
		return nil, nil, fmt.Errorf("write native MCP launch spec: %w", err)
	}
	if err := launchFile.Sync(); err != nil {
		_ = launchFile.Close()
		_ = os.Remove(launchPath)
		return nil, nil, fmt.Errorf("sync native MCP launch spec: %w", err)
	}
	if err := launchFile.Close(); err != nil {
		_ = os.Remove(launchPath)
		return nil, nil, fmt.Errorf("close native MCP launch spec: %w", err)
	}

	launcher, err := os.Executable()
	if err != nil {
		_ = os.Remove(launchPath)
		return nil, nil, fmt.Errorf("resolve GoFlow native sandbox launcher: %w", err)
	}
	launcher, err = filepath.Abs(launcher)
	if err != nil {
		_ = os.Remove(launchPath)
		return nil, nil, fmt.Errorf("resolve GoFlow launcher path: %w", err)
	}
	cmd := exec.Command(launcher, nativeSandboxCLIFlag, launchPath)
	cmd.Env = append([]string(nil), c.env...)
	cmd.Dir = scope.Workspace
	cmd.SysProcAttr = nativesandbox.LinuxSandboxLauncherSysProcAttr(launch.NetworkDisabled)

	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			cgroupErr := os.Remove(cgroupPath)
			if os.IsNotExist(cgroupErr) {
				cgroupErr = nil
			}
			cleanupErr = errors.Join(removeNativeStrongPath(launchPath), cgroupErr, cleanupTool(), cleanupRoot())
		})
		return cleanupErr
	}
	prepared = true
	return cmd, cleanup, nil
}

// linuxNativeStrongLibraryRoots returns only existing absolute directories
// explicitly supplied through the allowlisted dynamic-loader environment.
// This keeps custom runtimes usable without opening the whole host filesystem.
func linuxNativeStrongLibraryRoots(environment []string) []string {
	seen := make(map[string]struct{})
	var roots []string
	for _, item := range environment {
		if !strings.HasPrefix(item, "LD_LIBRARY_PATH=") {
			continue
		}
		for _, value := range strings.Split(strings.TrimPrefix(item, "LD_LIBRARY_PATH="), ":") {
			value = filepath.Clean(strings.TrimSpace(value))
			if value == "." || !filepath.IsAbs(value) {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			info, err := os.Stat(value)
			if err != nil || !info.IsDir() {
				continue
			}
			seen[value] = struct{}{}
			roots = append(roots, value)
		}
	}
	return roots
}
