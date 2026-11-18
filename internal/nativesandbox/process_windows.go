//go:build windows

package nativesandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	defaultWindowsSandboxProcessLimit       = 128
	defaultWindowsSandboxMemoryLimit        = 2 << 30
)

type windowsSecurityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// WindowsSandboxProcessSpec describes one executable copied into the writable
// task staging root. AppContainer processes receive no network capabilities.
type WindowsSandboxProcessSpec struct {
	Command          string
	Args             []string
	WorkDir          string
	Stdin            *os.File
	Stdout           *os.File
	Stderr           *os.File
	ProcessLimit     uint32
	MemoryLimitBytes uint64
}

// WindowsSandboxLaunchSpec is the serializable contract used by the internal
// launcher process. Standard streams are inherited from the launcher.
type WindowsSandboxLaunchSpec struct {
	AppContainerSID  string   `json:"app_container_sid"`
	WritableRoot     string   `json:"writable_root"`
	ExecutableRoots  []string `json:"executable_roots,omitempty"`
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	WorkDir          string   `json:"workdir"`
	ProcessLimit     uint32   `json:"process_limit,omitempty"`
	MemoryLimitBytes uint64   `json:"memory_limit_bytes,omitempty"`
}

// RunProcess starts a child inside the task AppContainer and Job Object. The
// command and working directory must both be inside the task staging root.
func (boundary *WindowsTaskBoundary) RunProcess(ctx context.Context, spec WindowsSandboxProcessSpec) (int, error) {
	if boundary == nil || boundary.Profile == nil || boundary.Profile.SID == nil {
		return -1, fmt.Errorf("Windows task boundary is not prepared")
	}
	boundary.mu.Lock()
	if boundary.closed {
		boundary.mu.Unlock()
		return -1, fmt.Errorf("Windows task boundary is closed")
	}
	boundary.active++
	boundary.mu.Unlock()
	defer func() {
		boundary.mu.Lock()
		boundary.active--
		boundary.mu.Unlock()
	}()

	normalized, err := normalizeWindowsSandboxProcessSpec(boundary.writable, boundary.readOnly, spec)
	if err != nil {
		return -1, err
	}
	return runWindowsAppContainerProcess(ctx, boundary.Profile.SID, normalized)
}

// RunWindowsSandboxLaunchSpec validates and executes a serialized launcher
// contract using the already provisioned AppContainer SID and ACL boundary.
func RunWindowsSandboxLaunchSpec(ctx context.Context, launch WindowsSandboxLaunchSpec) (int, error) {
	sid, err := windows.StringToSid(strings.TrimSpace(launch.AppContainerSID))
	if err != nil || sid == nil || !sid.IsValid() {
		return -1, fmt.Errorf("invalid AppContainer SID")
	}
	process := WindowsSandboxProcessSpec{
		Command:          launch.Command,
		Args:             append([]string(nil), launch.Args...),
		WorkDir:          launch.WorkDir,
		ProcessLimit:     launch.ProcessLimit,
		MemoryLimitBytes: launch.MemoryLimitBytes,
	}
	normalized, err := normalizeWindowsSandboxProcessSpec(launch.WritableRoot, launch.ExecutableRoots, process)
	if err != nil {
		return -1, err
	}
	return runWindowsAppContainerProcess(ctx, sid, normalized)
}

// DecodeWindowsSandboxLaunchSpec rejects trailing data and oversized launcher
// contracts before any native process API is called.
func DecodeWindowsSandboxLaunchSpec(data []byte) (WindowsSandboxLaunchSpec, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return WindowsSandboxLaunchSpec{}, fmt.Errorf("native sandbox launch spec must be between 1 byte and 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var spec WindowsSandboxLaunchSpec
	if err := decoder.Decode(&spec); err != nil {
		return WindowsSandboxLaunchSpec{}, fmt.Errorf("decode native sandbox launch spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return WindowsSandboxLaunchSpec{}, fmt.Errorf("native sandbox launch spec contains trailing data")
	}
	return spec, nil
}

func normalizeWindowsSandboxProcessSpec(writableRoot string, executableRoots []string, spec WindowsSandboxProcessSpec) (WindowsSandboxProcessSpec, error) {
	writableRoot = filepath.Clean(strings.TrimSpace(writableRoot))
	if writableRoot == "." || !filepath.IsAbs(writableRoot) {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("Windows task boundary has no absolute writable root")
	}
	command := filepath.Clean(strings.TrimSpace(spec.Command))
	if command == "." || !filepath.IsAbs(command) || !windowsSandboxCommandAllowed(command, writableRoot, executableRoots) {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("sandbox command must be an absolute file inside an allowed task tool root")
	}
	info, err := os.Stat(command)
	if err != nil {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("inspect sandbox command %s: %w", command, err)
	}
	if info.IsDir() {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("sandbox command must be a file: %s", command)
	}
	workDir := filepath.Clean(strings.TrimSpace(spec.WorkDir))
	if workDir == "." || workDir == "" {
		workDir = writableRoot
	}
	if !filepath.IsAbs(workDir) || !windowsPathContains(writableRoot, workDir) {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("sandbox working directory must be inside the writable task staging root")
	}
	workInfo, err := os.Stat(workDir)
	if err != nil {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("inspect sandbox working directory %s: %w", workDir, err)
	}
	if !workInfo.IsDir() {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("sandbox working directory must be a directory: %s", workDir)
	}
	if spec.ProcessLimit == 0 {
		spec.ProcessLimit = defaultWindowsSandboxProcessLimit
	}
	if spec.MemoryLimitBytes == 0 {
		spec.MemoryLimitBytes = defaultWindowsSandboxMemoryLimit
	}
	if spec.MemoryLimitBytes > uint64(^uintptr(0)) {
		return WindowsSandboxProcessSpec{}, fmt.Errorf("sandbox memory limit is too large for this platform")
	}
	spec.Command = command
	spec.WorkDir = workDir
	spec.Args = append([]string(nil), spec.Args...)
	if spec.Stdin == nil {
		spec.Stdin = os.Stdin
	}
	if spec.Stdout == nil {
		spec.Stdout = os.Stdout
	}
	if spec.Stderr == nil {
		spec.Stderr = os.Stderr
	}
	return spec, nil
}

func windowsSandboxCommandAllowed(command, writableRoot string, executableRoots []string) bool {
	if windowsPathContains(writableRoot, command) {
		return true
	}
	for _, root := range executableRoots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root != "." && filepath.IsAbs(root) && windowsPathContains(root, command) {
			return true
		}
	}
	return false
}

func runWindowsAppContainerProcess(ctx context.Context, sid *windows.SID, spec WindowsSandboxProcessSpec) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	attributeList, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return -1, fmt.Errorf("allocate AppContainer process attributes: %w", err)
	}
	defer attributeList.Delete()

	securityCapabilities := windowsSecurityCapabilities{AppContainerSID: sid}
	if err := attributeList.Update(
		procThreadAttributeSecurityCapabilities,
		unsafe.Pointer(&securityCapabilities),
		unsafe.Sizeof(securityCapabilities),
	); err != nil {
		return -1, fmt.Errorf("set AppContainer security capabilities: %w", err)
	}

	standardHandles := []windows.Handle{
		windows.Handle(spec.Stdin.Fd()),
		windows.Handle(spec.Stdout.Fd()),
		windows.Handle(spec.Stderr.Fd()),
	}
	handles := uniqueWindowsHandles(standardHandles)
	for _, handle := range handles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return -1, fmt.Errorf("mark sandbox standard handle inheritable: %w", err)
		}
		defer windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0)
	}
	if err := attributeList.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&handles[0]),
		unsafe.Sizeof(handles[0])*uintptr(len(handles)),
	); err != nil {
		return -1, fmt.Errorf("set AppContainer inherited handle list: %w", err)
	}

	application, err := windows.UTF16PtrFromString(spec.Command)
	if err != nil {
		return -1, fmt.Errorf("encode sandbox command: %w", err)
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{spec.Command}, spec.Args...)))
	if err != nil {
		return -1, fmt.Errorf("encode sandbox command line: %w", err)
	}
	workDir, err := windows.UTF16PtrFromString(spec.WorkDir)
	if err != nil {
		return -1, fmt.Errorf("encode sandbox working directory: %w", err)
	}

	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = standardHandles[0]
	startup.StdOutput = standardHandles[1]
	startup.StdErr = standardHandles[2]
	startup.ProcThreadAttributeList = attributeList.List()

	var process windows.ProcessInformation
	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(
		application,
		commandLine,
		nil,
		nil,
		true,
		creationFlags,
		nil,
		workDir,
		&startup.StartupInfo,
		&process,
	); err != nil {
		return -1, fmt.Errorf("create AppContainer process: %w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)

	job, err := createWindowsSandboxJob(spec.ProcessLimit, uintptr(spec.MemoryLimitBytes))
	if err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return -1, err
	}
	defer windows.CloseHandle(job)
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return -1, fmt.Errorf("assign AppContainer process to Job Object: %w", err)
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		return -1, fmt.Errorf("resume AppContainer process: %w", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		result, waitErr := windows.WaitForSingleObject(process.Process, windows.INFINITE)
		if waitErr != nil {
			waitDone <- waitErr
			return
		}
		if result != windows.WAIT_OBJECT_0 {
			waitDone <- fmt.Errorf("unexpected AppContainer wait result 0x%x", result)
			return
		}
		waitDone <- nil
	}()
	select {
	case err := <-waitDone:
		if err != nil {
			return -1, fmt.Errorf("wait for AppContainer process: %w", err)
		}
	case <-ctx.Done():
		_ = windows.TerminateJobObject(job, 1)
		<-waitDone
		return -1, ctx.Err()
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return -1, fmt.Errorf("read AppContainer process exit code: %w", err)
	}
	return int(exitCode), nil
}

func uniqueWindowsHandles(handles []windows.Handle) []windows.Handle {
	unique := make([]windows.Handle, 0, len(handles))
	seen := make(map[windows.Handle]struct{}, len(handles))
	for _, handle := range handles {
		if _, ok := seen[handle]; ok {
			continue
		}
		seen[handle] = struct{}{}
		unique = append(unique, handle)
	}
	return unique
}

func createWindowsSandboxJob(processLimit uint32, memoryLimit uintptr) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create sandbox Job Object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = processLimit
	limits.JobMemoryLimit = memoryLimit
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("set sandbox Job Object limits: %w", err)
	}
	return job, nil
}
