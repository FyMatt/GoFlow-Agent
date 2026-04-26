//go:build !windows && !linux

package mcp

import (
	"fmt"
	"os/exec"
)

type processIsolation struct{}

func attachProcessIsolation(isolation string, _ map[string]string, _ *exec.Cmd) (*processIsolation, error) {
	if isolation == "windows_job" {
		return nil, fmt.Errorf("windows_job isolation is only supported on Windows")
	}
	if isolation == "linux_cgroup" {
		return nil, fmt.Errorf("linux_cgroup isolation is only supported on Linux")
	}
	return nil, nil
}

func terminateProcessIsolation(_ *processIsolation, cmd *exec.Cmd) error {
	return killProcess(cmd)
}

func closeProcessIsolation(_ *processIsolation) error {
	return nil
}
