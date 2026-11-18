//go:build windows

package main

import (
	"os"
	"testing"
)

func TestNativeWindowsCollectorsReturnUsableEvidence(t *testing.T) {
	disks, err := collectNativeWindowsDisks(20)
	if err != nil || len(disks) == 0 {
		t.Fatalf("collectNativeWindowsDisks: items=%#v err=%v", disks, err)
	}
	usableDisk := false
	for _, disk := range disks {
		if numberValue(disk["size_bytes"]) > 0 {
			usableDisk = true
			break
		}
	}
	if !usableDisk {
		t.Fatalf("expected at least one disk with positive capacity, got %#v", disks)
	}

	processes, err := collectNativeWindowsProcesses(500, nil)
	if err != nil || len(processes) == 0 {
		t.Fatalf("collectNativeWindowsProcesses: items=%#v err=%v", processes, err)
	}
	foundSelf := false
	for _, process := range processes {
		if numberValue(process["pid"]) == int64(os.Getpid()) {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		t.Fatalf("expected process snapshot to contain pid %d", os.Getpid())
	}
	filtered, err := collectNativeWindowsProcesses(10, []int{os.Getpid()})
	if err != nil || len(filtered) != 1 || numberValue(filtered[0]["pid"]) != int64(os.Getpid()) {
		t.Fatalf("expected exact pid lookup, got items=%#v err=%v", filtered, err)
	}
}
