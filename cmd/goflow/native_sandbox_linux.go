//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
)

const nativeSandboxCLIFlag = "--internal-native-sandbox-spec"

func runNativeSandboxCLI(args []string) (bool, int, error) {
	if len(args) == 0 || args[0] != nativeSandboxCLIFlag {
		return false, 0, nil
	}
	if len(args) != 2 {
		return true, 0, fmt.Errorf("%s requires exactly one spec path", nativeSandboxCLIFlag)
	}
	path := filepath.Clean(args[1])
	if !filepath.IsAbs(path) {
		return true, 0, fmt.Errorf("native sandbox spec path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return true, 0, fmt.Errorf("inspect native sandbox spec: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return true, 0, fmt.Errorf("native sandbox spec must be a regular file no larger than 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, 0, fmt.Errorf("read native sandbox spec: %w", err)
	}
	spec, err := nativesandbox.DecodeLinuxSandboxProcessSpec(data)
	if err != nil {
		return true, 0, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exitCode, err := nativesandbox.RunLinuxSandboxLaunchSpec(ctx, spec)
	return true, exitCode, err
}
