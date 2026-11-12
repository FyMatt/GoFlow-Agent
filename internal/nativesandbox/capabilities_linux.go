//go:build linux

package nativesandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func platformProbe() Report {
	landlock := linuxLandlockAvailable()
	userNamespace := linuxUserNamespaceAvailable()
	mountNamespace := userNamespace && fileExists("/proc/self/ns/mnt")
	networkNamespace := userNamespace && fileExists("/proc/self/ns/net")
	seccomp := procStatusFieldAvailable("Seccomp:")
	cgroup := linuxCgroupWritable()
	return Report{Features: []Feature{
		{Name: "landlock", Available: landlock, Implemented: true, Enforced: landlock, Required: true},
		{Name: "user_namespace", Available: userNamespace, Implemented: true, Enforced: userNamespace, Required: true},
		{Name: "mount_namespace", Available: mountNamespace, Implemented: true, Enforced: mountNamespace, Required: true},
		{Name: "network_namespace", Available: networkNamespace, Implemented: true, Enforced: networkNamespace, Required: true},
		{Name: "seccomp", Available: seccomp, Implemented: true, Enforced: seccomp, Required: true},
		{Name: "rlimit", Available: true, Implemented: true, Enforced: true, Required: true},
		{Name: "cgroup_v2", Available: cgroup, Implemented: true, Enforced: cgroup, Required: true},
	}}
}

func linuxLandlockAvailable() bool {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, landlockCreateRulesetVersion)
	return errno == 0 && abi >= 1
}

func procStatusFieldAvailable(field string) bool {
	data, err := os.ReadFile("/proc/self/status")
	return err == nil && strings.Contains(string(data), field)
}

func linuxUserNamespaceAvailable() bool {
	if !fileExists("/proc/self/ns/user") {
		return false
	}
	data, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone")
	if err == nil && strings.TrimSpace(string(data)) == "0" && os.Geteuid() != 0 {
		return false
	}
	return true
}

func linuxCgroupWritable() bool {
	_, parent, err := LinuxTaskCgroupPath("capability-probe")
	if err != nil {
		return false
	}
	name := fmt.Sprintf(".goflow-probe-%d-%d", os.Getpid(), time.Now().UnixNano())
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return false
	}
	defer os.Remove(root)
	checks := map[string]string{"pids.max": "4", "memory.max": strconv.FormatUint(64<<20, 10), "cpu.max": "100000 100000"}
	for file, value := range checks {
		if err := os.WriteFile(filepath.Join(root, file), []byte(value), 0o600); err != nil {
			return false
		}
	}
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
