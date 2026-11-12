//go:build linux

package nativesandbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// LinuxSandboxProcessSpec is the checked execution contract for one task.
// The launcher applies every boundary before starting the child process.
type LinuxSandboxProcessSpec struct {
	TaskID           string   `json:"task_id"`
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	Env              []string `json:"env,omitempty"`
	WorkDir          string   `json:"workdir"`
	ReadOnlyRoots    []string `json:"read_only_roots,omitempty"`
	WritableRoot     string   `json:"writable_root"`
	NetworkDisabled  bool     `json:"network_disabled"`
	ProcessLimit     uint32   `json:"process_limit,omitempty"`
	MemoryLimitBytes uint64   `json:"memory_limit_bytes,omitempty"`
	CPUSeconds       uint64   `json:"cpu_seconds,omitempty"`
}

const (
	defaultLinuxSandboxProcessLimit = 128
	defaultLinuxSandboxMemoryLimit  = 2 << 30
	defaultLinuxSandboxCPUSeconds   = 900
	landlockAccessFSExecute         = 1 << 0
	landlockAccessFSWriteFile       = 1 << 1
	landlockAccessFSReadFile        = 1 << 2
	landlockAccessFSReadDir         = 1 << 3
	landlockAccessFSRemoveDir       = 1 << 4
	landlockAccessFSRemoveFile      = 1 << 5
	landlockAccessFSMakeChar        = 1 << 6
	landlockAccessFSMakeDir         = 1 << 7
	landlockAccessFSMakeReg         = 1 << 8
	landlockAccessFSMakeSock        = 1 << 9
	landlockAccessFSMakeFIFO        = 1 << 10
	landlockAccessFSMakeBlock       = 1 << 11
	landlockAccessFSMakeSym         = 1 << 12
	landlockAccessFSRefer           = 1 << 13
	landlockAccessFSTruncate        = 1 << 14
	landlockRuleTypePathBeneath     = 1
	landlockCreateRulesetVersion    = 1
	seccompSetModeFilter            = 1
	seccompFilterFlagTSync          = 1
	seccompRetAllow                 = 0x7fff0000
	seccompRetErrno                 = 0x00050000
	auditArchX8664                  = 0xc000003e

	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20
	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfRET = 0x06
	bpfK   = 0x00
)

type landlockRulesetAttr struct{ HandledAccessFS uint64 }
type landlockPathBeneathAttr struct {
	AllowedAccess uint64
	ParentFD      int32
}

// DecodeLinuxSandboxProcessSpec validates the internal launcher contract.
func DecodeLinuxSandboxProcessSpec(data []byte) (LinuxSandboxProcessSpec, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("native sandbox launch spec must be between 1 byte and 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var spec LinuxSandboxProcessSpec
	if err := decoder.Decode(&spec); err != nil {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("decode native sandbox launch spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("native sandbox launch spec contains trailing data")
	}
	return spec, nil
}

// RunLinuxSandboxLaunchSpec applies the native boundary and waits for a child.
// The parent that launched this process removes the empty task cgroup after
// the launcher exits, matching the MCP client lifecycle cleanup.
func RunLinuxSandboxLaunchSpec(ctx context.Context, spec LinuxSandboxProcessSpec) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	normalized, err := normalizeLinuxSandboxProcessSpec(spec)
	if err != nil {
		return -1, err
	}
	if err := applyLinuxNamespaceBoundary(normalized.NetworkDisabled); err != nil {
		return -1, err
	}
	cleanup, err := applyLinuxResourceBoundary(normalized)
	if err != nil {
		return -1, err
	}
	if err := applyLinuxLandlock(normalized); err != nil {
		_ = cleanup()
		return -1, err
	}
	if err := applyLinuxSeccomp(); err != nil {
		return -1, err
	}
	cmd := exec.Command(normalized.Command, normalized.Args...)
	if len(normalized.Env) > 0 {
		cmd.Env = append([]string(nil), normalized.Env...)
	}
	cmd.Dir = normalized.WorkDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start Linux sandbox child: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0, nil
		}
		if exit, ok := err.(*exec.ExitError); ok {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus(), nil
			}
		}
		return -1, err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return -1, ctx.Err()
	}
}

// LinuxSandboxLauncherSysProcAttr creates namespaces atomically while the
// single-threaded fork/exec path is active. CLONE_NEWUSER cannot be applied
// safely from an already multi-threaded Go launcher process.
func LinuxSandboxLauncherSysProcAttr(networkDisabled bool) *syscall.SysProcAttr {
	flags := uintptr(unix.CLONE_NEWUSER | unix.CLONE_NEWNS)
	if networkDisabled {
		flags |= unix.CLONE_NEWNET
	}
	return &syscall.SysProcAttr{
		Setpgid:                    true,
		Cloneflags:                 flags,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
}

func normalizeLinuxSandboxProcessSpec(spec LinuxSandboxProcessSpec) (LinuxSandboxProcessSpec, error) {
	if strings.TrimSpace(spec.TaskID) == "" {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("Linux sandbox task ID is required")
	}
	root, err := existingLinuxDirectory(spec.WritableRoot, "writable task staging root")
	if err != nil {
		return LinuxSandboxProcessSpec{}, err
	}
	command := filepath.Clean(strings.TrimSpace(spec.Command))
	if command == "." || !filepath.IsAbs(command) {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("sandbox command must be an absolute file")
	}
	readOnly := make([]string, 0, len(spec.ReadOnlyRoots))
	for _, path := range spec.ReadOnlyRoots {
		resolvedRoot, rootErr := existingLinuxDirectory(path, "read-only task root")
		if rootErr != nil {
			return LinuxSandboxProcessSpec{}, rootErr
		}
		if linuxPathWithin(root, resolvedRoot) || linuxPathWithin(resolvedRoot, root) {
			return LinuxSandboxProcessSpec{}, fmt.Errorf("read-only and writable task roots must not overlap")
		}
		readOnly = append(readOnly, resolvedRoot)
	}
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("resolve sandbox command links: %w", err)
	}
	if !linuxPathAllowed(resolved, root, readOnly) {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("sandbox command escapes task roots")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("sandbox command is not a regular file: %s", resolved)
	}
	workDir := filepath.Clean(strings.TrimSpace(spec.WorkDir))
	if workDir == "" || workDir == "." {
		workDir = root
	}
	workDir, err = filepath.EvalSymlinks(workDir)
	if err != nil || !linuxPathWithin(root, workDir) {
		return LinuxSandboxProcessSpec{}, fmt.Errorf("sandbox working directory must be inside task workspace")
	}
	if spec.ProcessLimit == 0 {
		spec.ProcessLimit = defaultLinuxSandboxProcessLimit
	}
	if spec.MemoryLimitBytes == 0 {
		spec.MemoryLimitBytes = defaultLinuxSandboxMemoryLimit
	}
	if spec.CPUSeconds == 0 {
		spec.CPUSeconds = defaultLinuxSandboxCPUSeconds
	}
	spec.WritableRoot, spec.Command, spec.WorkDir = root, resolved, workDir
	spec.ReadOnlyRoots = readOnly
	spec.Env = append([]string(nil), spec.Env...)
	return spec, nil
}

func existingLinuxDirectory(path, label string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be absolute", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory", label)
	}
	return resolved, nil
}

func linuxPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func linuxPathAllowed(path, writable string, readOnly []string) bool {
	if linuxPathWithin(writable, path) {
		return true
	}
	for _, root := range readOnly {
		if root != "" && linuxPathWithin(root, path) {
			return true
		}
	}
	return false
}

func applyLinuxNamespaceBoundary(networkDisabled bool) error {
	isolatedUser, err := linuxHasRestrictedUIDMap()
	if err != nil {
		return fmt.Errorf("Linux sandbox launcher has no isolated user namespace: %w", err)
	}
	if !isolatedUser {
		return fmt.Errorf("Linux sandbox launcher has no isolated user namespace")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make Linux mount namespace private: %w", err)
	}
	return nil
}

func linuxHasRestrictedUIDMap() (bool, error) {
	data, err := os.ReadFile("/proc/self/uid_map")
	if err != nil {
		return false, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return false, fmt.Errorf("invalid uid_map")
	}
	return !(fields[0] == "0" && fields[1] == "0" && fields[2] == "4294967295"), nil
}

func applyLinuxLandlock(spec LinuxSandboxProcessSpec) error {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return fmt.Errorf("Landlock is unavailable: %w", errno)
	}
	if abi < 1 {
		return fmt.Errorf("Landlock ABI is too old: %d", abi)
	}
	handled := uint64(landlockAccessFSExecute | landlockAccessFSWriteFile | landlockAccessFSReadFile | landlockAccessFSReadDir | landlockAccessFSRemoveDir | landlockAccessFSRemoveFile | landlockAccessFSMakeChar | landlockAccessFSMakeDir | landlockAccessFSMakeReg | landlockAccessFSMakeSock | landlockAccessFSMakeFIFO | landlockAccessFSMakeBlock | landlockAccessFSMakeSym)
	if abi >= 2 {
		handled |= landlockAccessFSRefer
	}
	if abi >= 3 {
		handled |= landlockAccessFSTruncate
	}
	attr := landlockRulesetAttr{HandledAccessFS: handled}
	ruleset, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(ruleset))
	add := func(root string, access uint64) error {
		if root == "" {
			return nil
		}
		pathFD, err := unix.Open(root, unix.O_PATH|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
		if err != nil {
			return fmt.Errorf("open Landlock root %s: %w", root, err)
		}
		defer unix.Close(pathFD)
		rule := landlockPathBeneathAttr{AllowedAccess: access, ParentFD: int32(pathFD)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, ruleset, landlockRuleTypePathBeneath, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("add Landlock rule %s: %w", root, errno)
		}
		return nil
	}
	read := uint64(landlockAccessFSExecute | landlockAccessFSReadFile | landlockAccessFSReadDir)
	write := handled
	for _, root := range []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc", "/dev", "/proc"} {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			if err := add(root, read); err != nil {
				return err
			}
		}
	}
	for _, root := range spec.ReadOnlyRoots {
		if err := add(root, read); err != nil {
			return err
		}
	}
	if err := add(spec.WritableRoot, write); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, ruleset, 0, 0)
	if errno != 0 {
		return fmt.Errorf("restrict process with Landlock: %w", errno)
	}
	return nil
}

func applyLinuxSeccomp() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	filter := linuxSeccompFilter()
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	_, _, errno := unix.Syscall6(unix.SYS_SECCOMP, seccompSetModeFilter, seccompFilterFlagTSync, uintptr(unsafe.Pointer(&prog)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("install seccomp filter: %w", errno)
	}
	return nil
}

func linuxSeccompFilter() []unix.SockFilter {
	denied := []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT, unix.SYS_SETNS, unix.SYS_UNSHARE, unix.SYS_PTRACE, unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN, unix.SYS_KEXEC_LOAD, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_REBOOT, unix.SYS_SWAPON, unix.SYS_SWAPOFF, unix.SYS_ACCT, unix.SYS_SETHOSTNAME, unix.SYS_SETDOMAINNAME}
	filter := []unix.SockFilter{{Code: bpfLD | bpfW | bpfABS, K: 4}, {Code: bpfJMP | bpfJEQ | bpfK, Jt: 1, K: auditArchX8664}, {Code: bpfRET | bpfK, K: 0x80000000}, {Code: bpfLD | bpfW | bpfABS, K: 0}}
	for _, syscallNo := range denied {
		filter = append(filter, unix.SockFilter{Code: bpfJMP | bpfJEQ | bpfK, Jf: 1, K: syscallNo}, unix.SockFilter{Code: bpfRET | bpfK, K: seccompRetErrno | 1})
	}
	filter = append(filter, unix.SockFilter{Code: bpfRET | bpfK, K: seccompRetAllow})
	return filter
}

func applyLinuxResourceBoundary(spec LinuxSandboxProcessSpec) (func() error, error) {
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: uint64(spec.ProcessLimit), Max: uint64(spec.ProcessLimit)}); err != nil {
		return nil, fmt.Errorf("set process limit: %w", err)
	}
	if err := unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: spec.MemoryLimitBytes, Max: spec.MemoryLimitBytes}); err != nil {
		return nil, fmt.Errorf("set memory limit: %w", err)
	}
	if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: spec.CPUSeconds, Max: spec.CPUSeconds}); err != nil {
		return nil, fmt.Errorf("set CPU limit: %w", err)
	}
	cgroup, _, restore, err := linuxTaskCgroupPlacement(spec.TaskID)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(cgroup, 0o700); err != nil {
		if !os.IsExist(err) || !linuxRemoveEmptyCgroup(cgroup) {
			return nil, fmt.Errorf("create task cgroup: %w", err)
		}
		if err := os.Mkdir(cgroup, 0o700); err != nil {
			return nil, fmt.Errorf("create task cgroup after stale cleanup: %w", err)
		}
	}
	cleanup := func() error {
		_ = os.WriteFile(filepath.Join(restore, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		if err := os.Remove(cgroup); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	for name, value := range map[string]string{
		"pids.max":   strconv.FormatUint(uint64(spec.ProcessLimit), 10),
		"memory.max": strconv.FormatUint(spec.MemoryLimitBytes, 10),
		"cpu.max":    "100000 100000",
	} {
		if err := os.WriteFile(filepath.Join(cgroup, name), []byte(value), 0o600); err != nil {
			_ = cleanup()
			return nil, fmt.Errorf("configure task cgroup %s: %w", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cgroup, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("join task cgroup: %w", err)
	}
	return cleanup, nil
}

func linuxRemoveEmptyCgroup(path string) bool {
	data, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil || strings.TrimSpace(string(data)) != "" {
		return false
	}
	return os.Remove(path) == nil
}

// LinuxTaskCgroupPath returns a deterministic leaf under the process's current
// cgroup. The parent GoFlow process removes it after the launcher exits.
func LinuxTaskCgroupPath(taskID string) (string, string, error) {
	cgroup, parent, _, err := linuxTaskCgroupPlacement(taskID)
	return cgroup, parent, err
}

func linuxTaskCgroupPlacement(taskID string) (string, string, string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", "", "", fmt.Errorf("task ID is required for cgroup isolation")
	}
	restore, err := linuxCurrentCgroupPath()
	if err != nil {
		return "", "", "", err
	}
	parent, err := linuxDelegatedCgroupRoot(restore)
	if err != nil {
		return "", "", "", err
	}
	digest := sha256.Sum256([]byte(taskID))
	return filepath.Join(parent, fmt.Sprintf("goflow-%x", digest[:8])), parent, restore, nil
}

func linuxCurrentCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read current cgroup: %w", err)
	}
	relative := ""
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			relative = strings.TrimPrefix(strings.TrimSpace(parts[2]), "/")
			break
		}
	}
	current := filepath.Clean(filepath.Join("/sys/fs/cgroup", filepath.FromSlash(relative)))
	if !linuxPathWithin("/sys/fs/cgroup", current) || !regularLinuxFile(filepath.Join(current, "cgroup.procs")) {
		return "", fmt.Errorf("current cgroup v2 path is unavailable")
	}
	return current, nil
}

func linuxDelegatedCgroupRoot(current string) (string, error) {
	root := filepath.Clean("/sys/fs/cgroup")
	for candidate := current; linuxPathWithin(root, candidate); candidate = filepath.Dir(candidate) {
		controllers, err := os.ReadFile(filepath.Join(candidate, "cgroup.controllers"))
		if err == nil && containsLinuxCgroupControllers(string(controllers), "cpu", "memory", "pids") {
			if err := enableLinuxCgroupControllers(candidate, "cpu", "memory", "pids"); err == nil {
				return candidate, nil
			}
		}
		if candidate == root {
			break
		}
	}
	return "", fmt.Errorf("writable delegated cgroup v2 hierarchy with cpu, memory, and pids controllers is unavailable")
}

func enableLinuxCgroupControllers(root string, controllers ...string) error {
	controlPath := filepath.Join(root, "cgroup.subtree_control")
	current, err := os.ReadFile(controlPath)
	if err != nil {
		return err
	}
	enabled := make(map[string]bool)
	for _, controller := range strings.Fields(string(current)) {
		enabled[controller] = true
	}
	var additions []string
	for _, controller := range controllers {
		if !enabled[controller] {
			additions = append(additions, "+"+controller)
		}
	}
	if len(additions) > 0 {
		if err := os.WriteFile(controlPath, []byte(strings.Join(additions, " ")), 0o600); err != nil {
			return err
		}
		current, err = os.ReadFile(controlPath)
		if err != nil {
			return err
		}
	}
	if !containsLinuxCgroupControllers(string(current), controllers...) {
		return fmt.Errorf("cgroup v2 subtree controllers were not enabled")
	}
	return nil
}

func containsLinuxCgroupControllers(value string, required ...string) bool {
	available := make(map[string]bool)
	for _, controller := range strings.Fields(value) {
		available[controller] = true
	}
	for _, controller := range required {
		if !available[controller] {
			return false
		}
	}
	return true
}

func regularLinuxFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
