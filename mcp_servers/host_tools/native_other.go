//go:build !windows

package main

import "fmt"

func collectNativeWindowsDisks(int) ([]map[string]any, error) {
	return nil, fmt.Errorf("Windows native disk collector is unavailable")
}

func collectNativeWindowsProcesses(int, []int) ([]map[string]any, error) {
	return nil, fmt.Errorf("Windows native process collector is unavailable")
}
