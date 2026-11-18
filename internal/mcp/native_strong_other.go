//go:build !windows && !linux

package mcp

import (
	"fmt"
	"os/exec"
)

func (c *Client) buildNativeStrongServerCommand(string, []string) (*exec.Cmd, func() error, error) {
	return nil, nil, fmt.Errorf("native strong MCP execution is not implemented on %s", runtimeGOOS())
}
