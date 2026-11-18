//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

type processTreeGuard struct{}

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(cmd *exec.Cmd) (*processTreeGuard, error) {
	return nil, nil
}

func closeProcessTreeGuard(_ *processTreeGuard) {}

func terminateProcessTree(cmd *exec.Cmd, _ *processTreeGuard) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
