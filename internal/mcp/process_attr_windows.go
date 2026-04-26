//go:build windows

package mcp

import "syscall"

func newSysProcAttr(isolation string) *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{}
	if isolation == "process_group" || isolation == "windows_job" {
		attr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
	}
	return attr
}
