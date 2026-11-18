//go:build windows

package taskstaging

import (
	"os"

	"golang.org/x/sys/windows"
)

func replaceFileAtomic(source, destination string) error {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPath, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(sourcePath, destinationPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	if err == nil {
		return nil
	}
	if _, statErr := os.Lstat(destination); os.IsNotExist(statErr) {
		return windows.MoveFileEx(sourcePath, destinationPath, windows.MOVEFILE_WRITE_THROUGH)
	}
	return err
}
