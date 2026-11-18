//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
)

func init() {
	for _, key := range []string{"TEMP", "TMP"} {
		value := os.Getenv(key)
		if value == "" || !strings.Contains(value, "~") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(value)
		if err != nil || resolved == value {
			continue
		}
		_ = os.Setenv(key, resolved)
	}
}
