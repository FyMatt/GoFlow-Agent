//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunNativeSandboxCLIRejectsInvalidSpecBeforeLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.json")
	if err := os.WriteFile(path, []byte(`{"app_container_sid":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	handled, _, err := runNativeSandboxCLI([]string{nativeSandboxCLIFlag, path})
	if !handled || err == nil {
		t.Fatalf("expected invalid native launcher spec to be handled and rejected: handled=%v err=%v", handled, err)
	}
}
