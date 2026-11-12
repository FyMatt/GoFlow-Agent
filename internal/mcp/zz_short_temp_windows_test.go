//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"strings"
)

// init normalises 8.3 short TEMP/TMP names (for example RUNNER~1 on CI) so
// tests and the code under test agree on one path form. Product code still
// resolves and validates real paths; this only removes an environment quirk.
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
