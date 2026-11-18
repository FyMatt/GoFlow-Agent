//go:build linux

package mcp

import (
	"fmt"
	"path/filepath"
	"strings"
)

type linuxNetworkNamespaceConfig struct {
	Command string
	Args    []string
	Options map[string]string
	Env     []string
}

func buildLinuxNetworkNamespaceCommand(cfg linuxNetworkNamespaceConfig) (string, []string, []string, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return "", nil, nil, fmt.Errorf("linux_netns isolation requires an MCP command")
	}
	options := normalizeLinuxNetworkNamespaceOptions(cfg.Options)
	unshareCommand := strings.TrimSpace(options["unshare_command"])
	if unshareCommand == "" {
		unshareCommand = "unshare"
	}
	if !validLinuxNetworkNamespaceCommand(unshareCommand) {
		return "", nil, nil, fmt.Errorf("linux_netns isolation_options.unshare_command must be unshare or an absolute path")
	}
	args := []string{"--net"}
	if linuxNetworkOptionBool(options["map_root_user"]) {
		args = append(args, "--map-root-user")
	}
	args = append(args, "--", command)
	args = append(args, cfg.Args...)
	return unshareCommand, args, append([]string(nil), cfg.Env...), nil
}

func linuxNetworkOptionBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeLinuxNetworkNamespaceOptions(options map[string]string) map[string]string {
	out := make(map[string]string, len(options))
	for key, value := range options {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func validLinuxNetworkNamespaceCommand(value string) bool {
	value = strings.TrimSpace(value)
	if value == "unshare" {
		return true
	}
	return filepath.IsAbs(value)
}
