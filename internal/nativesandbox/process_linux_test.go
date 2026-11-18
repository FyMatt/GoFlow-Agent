//go:build linux

package nativesandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	linuxNativeAcceptanceRequired = "GOFLOW_REQUIRE_LINUX_NATIVE_SANDBOX"
	linuxNativeLaunchHelper       = "GOFLOW_LINUX_NATIVE_LAUNCH_HELPER"
	linuxNativeMaliciousChild     = "GOFLOW_LINUX_NATIVE_MALICIOUS_CHILD"
)

func TestNormalizeLinuxSandboxProcessSpec(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	tools := filepath.Join(root, "tools")
	for _, path := range []string{workspace, tools} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command := filepath.Join(tools, "server")
	if err := os.WriteFile(command, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	spec, err := normalizeLinuxSandboxProcessSpec(LinuxSandboxProcessSpec{TaskID: "task", Command: command, WorkDir: workspace, ReadOnlyRoots: []string{tools}, WritableRoot: workspace, NetworkDisabled: true})
	if err != nil {
		t.Fatalf("normalize Linux sandbox spec: %v", err)
	}
	if spec.Command != command || spec.ProcessLimit != defaultLinuxSandboxProcessLimit || spec.MemoryLimitBytes != defaultLinuxSandboxMemoryLimit || spec.CPUSeconds != defaultLinuxSandboxCPUSeconds {
		t.Fatalf("unexpected normalized spec: %#v", spec)
	}
}

func TestNormalizeLinuxSandboxProcessSpecRejectsEscapesAndOverlap(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(outside, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := normalizeLinuxSandboxProcessSpec(LinuxSandboxProcessSpec{TaskID: "task", Command: outside, WritableRoot: workspace})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected command escape rejection, got %v", err)
	}
	inside := filepath.Join(workspace, "server")
	if err := os.WriteFile(inside, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	_, err = normalizeLinuxSandboxProcessSpec(LinuxSandboxProcessSpec{TaskID: "task", Command: inside, WritableRoot: workspace, ReadOnlyRoots: []string{workspace}})
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected root overlap rejection, got %v", err)
	}
}

func TestDecodeLinuxSandboxProcessSpecRejectsUnknownAndTrailingData(t *testing.T) {
	for _, payload := range []string{`{"task_id":"task","unknown":true}`, `{"task_id":"task"}{}`} {
		if _, err := DecodeLinuxSandboxProcessSpec([]byte(payload)); err == nil {
			t.Fatalf("expected invalid payload rejection: %s", payload)
		}
	}
}

func TestLinuxSeccompFilterChecksArchitectureAndDeniesSyscalls(t *testing.T) {
	filter := linuxSeccompFilter()
	if len(filter) < 8 || filter[0].K != 4 || filter[1].K != auditArchX8664 || filter[len(filter)-1].K != seccompRetAllow {
		t.Fatalf("unexpected seccomp filter: %#v", filter)
	}
	denials := 0
	for _, instruction := range filter {
		if instruction.Code == bpfRET|bpfK && instruction.K&0xffff0000 == seccompRetErrno {
			denials++
		}
	}
	if denials < 10 {
		t.Fatalf("seccomp filter has too few explicit denials: %d", denials)
	}
}

func TestLinuxNativeSandboxMaliciousFixture(t *testing.T) {
	if os.Getenv(linuxNativeAcceptanceRequired) != "1" {
		t.Skip("set GOFLOW_REQUIRE_LINUX_NATIVE_SANDBOX=1 on a Linux x86_64 acceptance host")
	}
	report := Probe()
	if !report.StrongReady {
		t.Fatalf("Linux native strong sandbox capability gate failed: %s", report.RefusalReason)
	}
	root := t.TempDir()
	tools := filepath.Join(root, "tools")
	dependencies := filepath.Join(root, "dependencies")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{tools, dependencies, workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(tools, "sandbox-fixture")
	if err := copyLinuxAcceptanceExecutable(os.Args[0], helper); err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(dependencies, "dependency.txt")
	if err := os.WriteFile(dependency, []byte("immutable-dependency\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	taskID := fmt.Sprintf("linux-malicious-%d", time.Now().UnixNano())
	spec := LinuxSandboxProcessSpec{
		TaskID:          taskID,
		Command:         helper,
		Args:            []string{"-test.run=^TestLinuxNativeSandboxMaliciousChild$", "-test.v"},
		WorkDir:         workspace,
		ReadOnlyRoots:   []string{tools, dependencies},
		WritableRoot:    workspace,
		NetworkDisabled: true,
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	launcher := exec.Command(os.Args[0], "-test.run=^TestLinuxNativeSandboxLaunchHelper$", "-test.v")
	launcher.SysProcAttr = LinuxSandboxLauncherSysProcAttr(spec.NetworkDisabled)
	launcher.Env = append(os.Environ(),
		linuxNativeLaunchHelper+"=1",
		linuxNativeMaliciousChild+"=1",
		"GOFLOW_LINUX_NATIVE_SPEC="+base64.StdEncoding.EncodeToString(payload),
		"GOFLOW_LINUX_NATIVE_WORKSPACE="+workspace,
		"GOFLOW_LINUX_NATIVE_DEPENDENCY="+dependency,
		"GOFLOW_LINUX_NATIVE_OUTSIDE="+outside,
	)
	output, err := launcher.CombinedOutput()
	if err != nil {
		t.Fatalf("Linux native malicious fixture failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "GOFLOW_LINUX_NATIVE_SANDBOX_OK") {
		t.Fatalf("Linux native malicious fixture did not produce success evidence:\n%s", output)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "inside.txt")); err != nil || string(content) != "inside-ok\n" {
		t.Fatalf("sandbox did not preserve its allowed workspace output: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote outside its task workspace: %v", err)
	}
	if content, err := os.ReadFile(dependency); err != nil || string(content) != "immutable-dependency\n" {
		t.Fatalf("sandbox changed a read-only dependency: content=%q err=%v", content, err)
	}
	if cgroup, _, err := LinuxTaskCgroupPath(taskID); err == nil {
		if removeErr := os.Remove(cgroup); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Fatalf("remove exited sandbox cgroup: %s err=%v", cgroup, removeErr)
		}
		if _, statErr := os.Stat(cgroup); !os.IsNotExist(statErr) {
			t.Fatalf("sandbox cgroup was not cleaned up: %s err=%v", cgroup, statErr)
		}
	}
}

func TestLinuxNativeSandboxLaunchHelper(t *testing.T) {
	if os.Getenv(linuxNativeLaunchHelper) != "1" {
		return
	}
	payload, err := base64.StdEncoding.DecodeString(os.Getenv("GOFLOW_LINUX_NATIVE_SPEC"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := DecodeLinuxSandboxProcessSpec(payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exitCode, err := RunLinuxSandboxLaunchSpec(ctx, spec)
	if err != nil {
		t.Fatalf("run Linux sandbox launch spec: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("Linux sandbox child exited with %d", exitCode)
	}
}

func TestLinuxNativeSandboxMaliciousChild(t *testing.T) {
	if os.Getenv(linuxNativeMaliciousChild) != "1" {
		return
	}
	workspace := os.Getenv("GOFLOW_LINUX_NATIVE_WORKSPACE")
	dependency := os.Getenv("GOFLOW_LINUX_NATIVE_DEPENDENCY")
	outside := os.Getenv("GOFLOW_LINUX_NATIVE_OUTSIDE")
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
		t.Fatal("network-disabled sandbox unexpectedly opened an external connection")
	}
	mountpoint := filepath.Join(workspace, "mount-test")
	if err := os.MkdirAll(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("none", mountpoint, "tmpfs", 0, ""); err == nil {
		_ = unix.Unmount(mountpoint, 0)
		t.Fatal("seccomp unexpectedly allowed mount")
	}
	fmt.Println("GOFLOW_LINUX_NATIVE_SANDBOX_OK")
}

func copyLinuxAcceptanceExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return os.Chmod(destination, 0o500)
}
