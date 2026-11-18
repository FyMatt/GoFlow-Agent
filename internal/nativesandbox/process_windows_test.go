//go:build windows

package nativesandbox

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const windowsNativeMaliciousChild = "GOFLOW_WINDOWS_NATIVE_MALICIOUS_CHILD"

func TestNormalizeWindowsSandboxProcessSpec(t *testing.T) {
	root := t.TempDir()
	command := filepath.Join(root, "tool.exe")
	if err := os.WriteFile(command, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := normalizeWindowsSandboxProcessSpec(root, nil, WindowsSandboxProcessSpec{Command: command})
	if err != nil {
		t.Fatalf("normalize sandbox process: %v", err)
	}
	if spec.WorkDir != root || spec.ProcessLimit != defaultWindowsSandboxProcessLimit || spec.MemoryLimitBytes != defaultWindowsSandboxMemoryLimit {
		t.Fatalf("unexpected sandbox defaults: %#v", spec)
	}
}

func TestNormalizeWindowsSandboxProcessSpecRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.exe")
	if err := os.WriteFile(outside, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeWindowsSandboxProcessSpec(root, nil, WindowsSandboxProcessSpec{Command: outside}); err == nil {
		t.Fatal("expected a command outside the task staging root to be rejected")
	}
}

func TestNormalizeWindowsSandboxProcessSpecAllowsReadOnlyToolRoot(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	tools := filepath.Join(root, "tools")
	for _, path := range []string{workspace, tools} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command := filepath.Join(tools, "server.exe")
	if err := os.WriteFile(command, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeWindowsSandboxProcessSpec(workspace, []string{tools}, WindowsSandboxProcessSpec{Command: command}); err != nil {
		t.Fatalf("read-only task tool should be executable: %v", err)
	}
}

func TestDecodeWindowsSandboxLaunchSpecRejectsUnknownFields(t *testing.T) {
	_, err := DecodeWindowsSandboxLaunchSpec([]byte(`{"app_container_sid":"S-1-15-2-1","unknown":true}`))
	if err == nil {
		t.Fatal("expected unknown launch fields to be rejected")
	}
}

func TestUniqueWindowsHandles(t *testing.T) {
	unique := uniqueWindowsHandles([]windows.Handle{11, 12, 12, 13, 11})
	if len(unique) != 3 || unique[0] != 11 || unique[1] != 12 || unique[2] != 13 {
		t.Fatalf("unexpected unique handle list: %#v", unique)
	}
}

func TestWindowsNativeSandboxMaliciousFixture(t *testing.T) {
	if !Probe().StrongReady {
		t.Skip("Windows AppContainer strong sandbox is unavailable on this host")
	}
	root := t.TempDir()
	input := filepath.Join(root, "input")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(input, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(workspace, "sandbox-fixture.exe")
	if err := copyWindowsAcceptanceExecutable(os.Args[0], command); err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(input, "dependency.txt")
	if err := os.WriteFile(dependency, []byte("immutable-dependency\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	boundary, err := PrepareWindowsTaskBoundary(WindowsTaskBoundarySpec{
		TaskID:        "process-boundary-" + time.Now().UTC().Format("20060102-150405.000000000"),
		ReadOnlyRoots: []string{input},
		WritableRoot:  workspace,
	})
	if err != nil {
		var nativeErr *WindowsHRESULTError
		if errors.As(err, &nativeErr) && windowsAppContainerUnavailableHRESULT(nativeErr.HRESULT) {
			t.Skipf("AppContainer profile service is unavailable on this test host: %v", err)
		}
		t.Fatalf("prepare Windows task boundary: %v", err)
	}
	t.Cleanup(func() {
		if err := boundary.Close(); err != nil {
			t.Errorf("close Windows task boundary: %v", err)
		}
	})

	outside := filepath.Join(root, "outside.txt")
	outputPath := filepath.Join(workspace, "fixture-output.txt")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	t.Setenv(windowsNativeMaliciousChild, "1")
	t.Setenv("GOFLOW_WINDOWS_NATIVE_WORKSPACE", workspace)
	t.Setenv("GOFLOW_WINDOWS_NATIVE_DEPENDENCY", dependency)
	t.Setenv("GOFLOW_WINDOWS_NATIVE_OUTSIDE", outside)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exitCode, err := boundary.RunProcess(ctx, WindowsSandboxProcessSpec{
		Command: command,
		Args:    []string{"-test.run=^TestWindowsNativeSandboxMaliciousChild$", "-test.v"},
		WorkDir: workspace,
		Stdout:  output,
		Stderr:  output,
	})
	if err != nil {
		t.Fatalf("run AppContainer process: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("AppContainer malicious fixture exited with %d", exitCode)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outputData), "GOFLOW_WINDOWS_NATIVE_SANDBOX_OK") {
		t.Fatalf("AppContainer fixture did not produce success evidence:\n%s", outputData)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "inside.txt")); err != nil || string(content) != "inside-ok\n" {
		t.Fatalf("AppContainer did not preserve its allowed output: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("AppContainer escaped the task staging root; outside file stat error: %v", err)
	}
	if content, err := os.ReadFile(dependency); err != nil || string(content) != "immutable-dependency\n" {
		t.Fatalf("AppContainer changed a read-only dependency: content=%q err=%v", content, err)
	}
}

func TestWindowsNativeSandboxMaliciousChild(t *testing.T) {
	if os.Getenv(windowsNativeMaliciousChild) != "1" {
		return
	}
	workspace := os.Getenv("GOFLOW_WINDOWS_NATIVE_WORKSPACE")
	dependency := os.Getenv("GOFLOW_WINDOWS_NATIVE_DEPENDENCY")
	outside := os.Getenv("GOFLOW_WINDOWS_NATIVE_OUTSIDE")
	if err := os.WriteFile(filepath.Join(workspace, "inside.txt"), []byte("inside-ok\n"), 0o600); err != nil {
		t.Fatalf("write allowed task output: %v", err)
	}
	if content, err := os.ReadFile(dependency); err != nil || string(content) != "immutable-dependency\n" {
		t.Fatalf("read staged dependency: content=%q err=%v", content, err)
	}
	if err := os.WriteFile(dependency, []byte("tampered\n"), 0o600); err == nil {
		t.Fatal("read-only dependency write unexpectedly succeeded")
	}
	if err := os.WriteFile(outside, []byte("escaped\n"), 0o600); err == nil {
		t.Fatal("outside workspace write unexpectedly succeeded")
	}
	connection, err := net.DialTimeout("tcp", "192.0.2.1:9", 400*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("network-disabled AppContainer unexpectedly opened an external connection")
	}
	t.Log("GOFLOW_WINDOWS_NATIVE_SANDBOX_OK")
}

func copyWindowsAcceptanceExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}
