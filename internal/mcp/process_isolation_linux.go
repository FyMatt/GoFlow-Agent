//go:build linux

package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultLinuxCgroupParent = "/sys/fs/cgroup/goflow"

type processIsolation struct {
	cgroupPath string
}

func attachProcessIsolation(isolation string, options map[string]string, cmd *exec.Cmd) (*processIsolation, error) {
	if isolation != "linux_cgroup" {
		return nil, nil
	}
	if cmd == nil || cmd.Process == nil {
		return nil, fmt.Errorf("process is not started")
	}
	cgroupPath, err := linuxCgroupPath(options, cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		return nil, fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}
	if err := writeLinuxCgroupLimits(cgroupPath, options); err != nil {
		_ = os.Remove(cgroupPath)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = os.Remove(cgroupPath)
		return nil, fmt.Errorf("attach process to cgroup %s: %w", cgroupPath, err)
	}
	return &processIsolation{cgroupPath: cgroupPath}, nil
}

func terminateProcessIsolation(handle *processIsolation, cmd *exec.Cmd) error {
	return killProcess(cmd)
}

func closeProcessIsolation(handle *processIsolation) error {
	if handle == nil || strings.TrimSpace(handle.cgroupPath) == "" {
		return nil
	}
	err := os.Remove(handle.cgroupPath)
	handle.cgroupPath = ""
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func linuxCgroupPath(options map[string]string, pid int) (string, error) {
	parent := strings.TrimSpace(options["cgroup_parent"])
	if parent == "" {
		parent = defaultLinuxCgroupParent
	}
	name := strings.TrimSpace(options["cgroup_name"])
	if name == "" {
		name = fmt.Sprintf("mcp-%d", pid)
	}
	if !isSafeCgroupName(name) {
		return "", fmt.Errorf("unsafe cgroup_name %q", name)
	}
	parent = filepath.Clean(parent)
	if !filepath.IsAbs(parent) {
		return "", fmt.Errorf("cgroup_parent must be absolute")
	}
	path := filepath.Clean(filepath.Join(parent, name))
	rel, err := filepath.Rel(parent, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cgroup path escapes parent")
	}
	return path, nil
}

func isSafeCgroupName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func writeLinuxCgroupLimits(cgroupPath string, options map[string]string) error {
	files := map[string]string{
		"memory_max": "memory.max",
		"pids_max":   "pids.max",
		"cpu_max":    "cpu.max",
	}
	for option, file := range files {
		value := strings.TrimSpace(options[option])
		if value == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(cgroupPath, file), []byte(value), 0o644); err != nil {
			return fmt.Errorf("write cgroup %s: %w", file, err)
		}
	}
	return nil
}
