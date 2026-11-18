//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func collectNativeWindowsDisks(limit int) ([]map[string]any, error) {
	buffer := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buffer)), &buffer[0])
	if err != nil {
		return nil, fmt.Errorf("enumerate logical drives: %w", err)
	}
	if int(n) > len(buffer) {
		buffer = make([]uint16, n)
		n, err = windows.GetLogicalDriveStrings(uint32(len(buffer)), &buffer[0])
		if err != nil {
			return nil, fmt.Errorf("enumerate logical drives: %w", err)
		}
	}

	items := make([]map[string]any, 0)
	for start := 0; start < int(n) && len(items) < limit; {
		end := start
		for end < int(n) && buffer[end] != 0 {
			end++
		}
		if end == start {
			break
		}
		root := windows.UTF16ToString(buffer[start:end])
		start = end + 1
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil || windows.GetDriveType(rootPtr) != windows.DRIVE_FIXED {
			continue
		}

		var available, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, &available, &total, &totalFree); err != nil {
			items = append(items, map[string]any{
				"volume": strings.TrimSuffix(root, `\`),
				"mount":  root,
				"status": "unavailable",
				"error":  err.Error(),
			})
			continue
		}
		items = append(items, map[string]any{
			"volume":                    strings.TrimSuffix(root, `\`),
			"mount":                     root,
			"size_bytes":                total,
			"free_bytes":                available,
			"total_free_bytes":          totalFree,
			"used_bytes":                total - totalFree,
			"collector":                 "windows_native",
			"available_to_caller_bytes": available,
		})
	}
	return items, nil
}

func collectNativeWindowsProcesses(limit int, pids []int) ([]map[string]any, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("snapshot processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("read first process: %w", err)
	}
	items := make([]map[string]any, 0, limit)
	filter := make(map[uint32]struct{}, len(pids))
	for _, pid := range pids {
		filter[uint32(pid)] = struct{}{}
	}
	for {
		_, selected := filter[entry.ProcessID]
		if len(filter) == 0 || selected {
			items = append(items, map[string]any{
				"pid":        int(entry.ProcessID),
				"parent_pid": int(entry.ParentProcessID),
				"threads":    int(entry.Threads),
				"name":       windows.UTF16ToString(entry.ExeFile[:]),
				"collector":  "windows_native",
			})
		}
		if len(items) >= limit {
			break
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, fmt.Errorf("read next process: %w", err)
		}
	}
	return items, nil
}
