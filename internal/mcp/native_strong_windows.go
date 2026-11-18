//go:build windows

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
	"golang.org/x/sys/windows/registry"
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
	stagedCommand, runtimeEnv, err := c.stageWindowsNativeStrongCommand(resolvedCommand, toolDir)
	if err != nil {
		return nil, nil, err
	}
	stagedArgs, err := c.stageNativeStrongArguments(args, scope)
	if err != nil {
		return nil, nil, err
	}
	stagedArgs = windowsNativeStrongRuntimeArguments(resolvedCommand, stagedArgs)
	if err := validateNativeStrongArguments(stagedArgs, scope, toolDir); err != nil {
		return nil, nil, err
	}

	readOnlyRoots := []string{toolDir, scope.Dependencies}
	if strings.TrimSpace(scope.Input) != "" {
		readOnlyRoots = append(readOnlyRoots, scope.Input)
	}
	boundary, err := nativesandbox.PrepareWindowsTaskBoundary(nativesandbox.WindowsTaskBoundarySpec{
		TaskID:        nativeStrongRunID(scope, c.name, command),
		ReadOnlyRoots: readOnlyRoots,
		WritableRoot:  scope.Workspace,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("prepare native MCP task boundary: %w", err)
	}
	cleanupBoundary := boundary.Close
	defer func() {
		if !prepared {
			_ = cleanupBoundary()
		}
	}()

	metadataDir := filepath.Join(scope.Root, "metadata")
	if err := os.MkdirAll(metadataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create native MCP metadata directory: %w", err)
	}
	launchFile, err := os.CreateTemp(metadataDir, "native-mcp-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("create native MCP launch spec: %w", err)
	}
	launchPath := launchFile.Name()
	launch := nativesandbox.WindowsSandboxLaunchSpec{
		AppContainerSID: boundary.Profile.SID.String(),
		WritableRoot:    scope.Workspace,
		ExecutableRoots: readOnlyRoots,
		Command:         stagedCommand,
		Args:            append([]string(nil), stagedArgs...),
		WorkDir:         scope.Workspace,
	}
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
	cmd.Env, err = windowsNativeStrongEnvironment(c.env, scope.Workspace, runtimeEnv)
	if err != nil {
		_ = os.Remove(launchPath)
		return nil, nil, err
	}
	cmd.Dir = scope.Workspace

	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			cleanupErr = errors.Join(
				removeNativeStrongPath(launchPath),
				cleanupBoundary(),
				cleanupTool(),
				cleanupRoot(),
			)
		})
		return cleanupErr
	}
	prepared = true
	return cmd, cleanup, nil
}

func windowsNativeStrongRuntimeArguments(command string, args []string) []string {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(command), filepath.Ext(command)))
	if name != "node" && name != "nodejs" {
		return args
	}
	prefixed := make([]string, 0, len(args)+2)
	prefixed = append(prefixed, "--preserve-symlinks", "--preserve-symlinks-main")
	return append(prefixed, args...)
}

func (c *Client) stageWindowsNativeStrongCommand(command, toolDir string) (string, map[string]string, error) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(command), filepath.Ext(command)))
	pythonRuntime := strings.HasPrefix(name, "python")
	runtimeRoot := ""
	if pythonRuntime {
		runtimeRoot = filepath.Dir(command)
	}
	if pythonRuntime && !windowsTrustedNativeRuntimeRoot(runtimeRoot) {
		return "", nil, fmt.Errorf("Python runtime %s is not in a system-protected installation root; install a system-wide Python runtime for native strong isolation", runtimeRoot)
	}
	staged, err := c.cacheNativeStrongExecutable(command, toolDir)
	if err != nil {
		return "", nil, err
	}
	if !pythonRuntime {
		return staged, nil, nil
	}
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		return "", nil, fmt.Errorf("inspect Python runtime root: %w", err)
	}
	copiedDLL := false
	for _, entry := range entries {
		lower := strings.ToLower(entry.Name())
		if entry.IsDir() || filepath.Ext(lower) != ".dll" || !(strings.HasPrefix(lower, "python") || strings.HasPrefix(lower, "vcruntime")) {
			continue
		}
		if _, err := c.cacheWindowsNativeStrongRuntimeFile(filepath.Join(runtimeRoot, entry.Name()), toolDir); err != nil {
			return "", nil, fmt.Errorf("cache Python runtime DLL %s: %w", entry.Name(), err)
		}
		copiedDLL = true
	}
	if !copiedDLL {
		return "", nil, fmt.Errorf("Python runtime beside %s has no loadable DLLs", command)
	}
	return staged, map[string]string{
		"PYTHONHOME":              runtimeRoot,
		"PYTHONNOUSERSITE":        "1",
		"PYTHONDONTWRITEBYTECODE": "1",
	}, nil
}

func windowsTrustedNativeRuntimeRoot(root string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	defer key.Close()
	for _, valueName := range []string{"ProgramFilesDir", "ProgramFilesDir (x86)"} {
		value, _, valueErr := key.GetStringValue(valueName)
		if valueErr != nil {
			continue
		}
		protected := filepath.Clean(strings.TrimSpace(value))
		if protected != "." && protected != "" && mcpPathWithin(protected, root) {
			return true
		}
	}
	return false
}

func (c *Client) cacheWindowsNativeStrongRuntimeFile(source, destinationDir string) (string, error) {
	store, err := c.nativeStrongDependencyStore()
	if err != nil {
		return "", err
	}
	artifact, err := store.PutFile(source)
	if err != nil {
		return "", err
	}
	return store.Materialize(artifact, destinationDir, filepath.Base(source))
}

func windowsNativeStrongEnvironment(base []string, workspace string, runtime map[string]string) ([]string, error) {
	profile := filepath.Join(workspace, ".goflow-profile")
	values := map[string]string{
		"USERPROFILE":  profile,
		"HOME":         profile,
		"APPDATA":      filepath.Join(profile, "AppData", "Roaming"),
		"LOCALAPPDATA": filepath.Join(profile, "AppData", "Local"),
		"TEMP":         filepath.Join(workspace, ".goflow-temp"),
		"TMP":          filepath.Join(workspace, ".goflow-temp"),
	}
	for name, value := range runtime {
		values[name] = value
	}
	for _, path := range []string{values["USERPROFILE"], values["APPDATA"], values["LOCALAPPDATA"], values["TEMP"]} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create native MCP task environment directory: %w", err)
		}
	}
	environment := append([]string(nil), base...)
	for name, value := range values {
		environment = upsertWindowsEnvironment(environment, name, value)
	}
	return environment, nil
}

func upsertWindowsEnvironment(environment []string, name, value string) []string {
	prefix := strings.ToLower(name) + "="
	filtered := environment[:0]
	for _, item := range environment {
		if strings.HasPrefix(strings.ToLower(item), prefix) {
			continue
		}
		filtered = append(filtered, item)
	}
	return append(filtered, name+"="+value)
}
