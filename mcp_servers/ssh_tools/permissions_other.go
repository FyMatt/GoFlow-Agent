//go:build !windows

package main

import "os"

func secureSSHDirectory(path string) error {
	return os.Chmod(path, 0o700)
}
