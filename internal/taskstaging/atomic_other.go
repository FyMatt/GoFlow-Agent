//go:build !windows

package taskstaging

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
