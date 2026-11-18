//go:build !windows && !linux

package main

func runNativeSandboxCLI([]string) (bool, int, error) {
	return false, 0, nil
}
