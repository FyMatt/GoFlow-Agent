//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func configureBrowserPipes(cmd *exec.Cmd, input, output *os.File) error {
	cmd.ExtraFiles = []*os.File{input, output} // Chromium CDP uses fd 3 and 4.
	return nil
}
