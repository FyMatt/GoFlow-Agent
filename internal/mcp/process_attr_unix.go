//go:build !windows

package mcp

import "syscall"

func newSysProcAttr(isolation string) (*syscall.SysProcAttr, func() error, error) {
	attr := &syscall.SysProcAttr{}
	if isolation == "process_group" {
		attr.Setpgid = true
	}
	return attr, nil, nil
}
