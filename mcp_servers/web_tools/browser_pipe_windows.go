package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Chromium adopts these inherited HANDLEs as its CDP input/output descriptors.
// No listening debugging port or additional runtime is needed.
func configureBrowserPipes(cmd *exec.Cmd, input, output *os.File) error {
	handles := []syscall.Handle{syscall.Handle(input.Fd()), syscall.Handle(output.Fd())}
	for _, handle := range handles {
		if err := syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
			return err
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, AdditionalInheritedHandles: handles}
	cmd.Args = append(cmd.Args, fmt.Sprintf("--remote-debugging-io-pipes=%d,%d", input.Fd(), output.Fd()))
	return nil
}
