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
	cleanup    func() error
}

func attachProcessIsolation(isolation string, options map[string]string, cmd *exec.Cmd) (*processIsolation, error) {
	if isolation == "linux_netns" {
		return nil, nil
	}
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
	if handle == nil {
		return nil
	}
	if handle.cleanup != nil {
		if err := handle.cleanup(); err != nil {
			return err
		}
		handle.cleanup = nil
	}
	if strings.TrimSpace(handle.cgroupPath) == "" {
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
	limits := []struct {
		option    string
		file      string
		normalize func(string) (string, error)
	}{
		{option: "memory_max", file: "memory.max", normalize: normalizeLinuxCgroupMemoryMax},
		{option: "pids_max", file: "pids.max", normalize: normalizeLinuxCgroupMaxOrPositiveInteger},
		{option: "cpu_max", file: "cpu.max", normalize: normalizeLinuxCgroupCPUMax},
	}
	for _, limit := range limits {
		option := limit.option
		value := strings.TrimSpace(options[option])
		if value == "" {
			continue
		}
		normalized, err := limit.normalize(value)
		if err != nil {
			return fmt.Errorf("invalid cgroup %s: %w", option, err)
		}
		if err := os.WriteFile(filepath.Join(cgroupPath, limit.file), []byte(normalized), 0o644); err != nil {
			return fmt.Errorf("write cgroup %s: %w", limit.file, err)
		}
	}
	return nil
}

func normalizeLinuxCgroupMemoryMax(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "max") {
		return "max", nil
	}
	if value == "" {
		return "", fmt.Errorf("value is required")
	}
	multiplier := int64(1)
	number := value
	switch suffix := strings.ToLower(value[len(value)-1:]); suffix {
	case "k":
		multiplier = 1024
		number = value[:len(value)-1]
	case "m":
		multiplier = 1024 * 1024
		number = value[:len(value)-1]
	case "g":
		multiplier = 1024 * 1024 * 1024
		number = value[:len(value)-1]
	case "t":
		multiplier = 1024 * 1024 * 1024 * 1024
		number = value[:len(value)-1]
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	if err != nil || parsed <= 0 {
		return "", fmt.Errorf("memory_max must be max, a positive byte count, or a positive K/M/G/T size")
	}
	if parsed > (1<<63-1)/multiplier {
		return "", fmt.Errorf("memory_max is too large")
	}
	return strconv.FormatInt(parsed*multiplier, 10), nil
}

func normalizeLinuxCgroupMaxOrPositiveInteger(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "max") {
		return "max", nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return "", fmt.Errorf("value must be max or a positive integer")
	}
	return strconv.FormatInt(parsed, 10), nil
}

func normalizeLinuxCgroupCPUMax(value string) (string, error) {
	fields := strings.Fields(value)
	if len(fields) == 1 && strings.EqualFold(fields[0], "max") {
		return "max", nil
	}
	if len(fields) != 2 {
		return "", fmt.Errorf("cpu_max must be max, or '<quota> <period>'")
	}
	quota := strings.ToLower(fields[0])
	if quota != "max" {
		if _, err := normalizeLinuxCgroupMaxOrPositiveInteger(quota); err != nil {
			return "", fmt.Errorf("quota must be max or a positive integer")
		}
	}
	period, err := normalizeLinuxCgroupMaxOrPositiveInteger(fields[1])
	if err != nil || period == "max" {
		return "", fmt.Errorf("period must be a positive integer")
	}
	return quota + " " + period, nil
}
