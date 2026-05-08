//go:build windows

package mcp

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processIsolation struct {
	job     windows.Handle
	cleanup func() error
}

func attachProcessIsolation(isolation string, _ map[string]string, cmd *exec.Cmd) (*processIsolation, error) {
	if isolation != "windows_job" && isolation != "windows_restricted_token" {
		return nil, nil
	}
	if cmd == nil || cmd.Process == nil {
		return nil, fmt.Errorf("process is not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	attached := false
	defer func() {
		if !attached {
			_ = windows.CloseHandle(job)
		}
	}()

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return nil, fmt.Errorf("set job limits: %w", err)
	}

	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(process)

	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, fmt.Errorf("assign process to job object: %w", err)
	}
	attached = true
	return &processIsolation{job: job}, nil
}

func terminateProcessIsolation(handle *processIsolation, cmd *exec.Cmd) error {
	if handle != nil && handle.job != 0 {
		return windows.TerminateJobObject(handle.job, 1)
	}
	return killProcess(cmd)
}

func closeProcessIsolation(handle *processIsolation) error {
	if handle == nil || handle.job == 0 {
		if handle != nil && handle.cleanup != nil {
			err := handle.cleanup()
			handle.cleanup = nil
			return err
		}
		return nil
	}
	err := windows.CloseHandle(handle.job)
	handle.job = 0
	if handle.cleanup != nil {
		if cleanupErr := handle.cleanup(); err == nil {
			err = cleanupErr
		}
		handle.cleanup = nil
	}
	return err
}
