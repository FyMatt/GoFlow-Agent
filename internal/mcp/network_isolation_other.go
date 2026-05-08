//go:build !linux

package mcp

import "fmt"

type linuxNetworkNamespaceConfig struct {
	Command string
	Args    []string
	Options map[string]string
	Env     []string
}

func buildLinuxNetworkNamespaceCommand(linuxNetworkNamespaceConfig) (string, []string, []string, error) {
	return "", nil, nil, fmt.Errorf("linux_netns isolation is only supported on Linux")
}
