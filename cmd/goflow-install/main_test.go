package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSafeArchivePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, member := range []string{"../../outside.txt", `C:\outside.txt`, `C:/outside.txt`, `C:outside.txt`, `/outside.txt`, `\\server\share\outside.txt`} {
		if _, err := safeArchivePath(root, member); err == nil {
			t.Fatalf("expected unsafe member %q to be rejected", member)
		}
	}
}

func TestRetryableRenameErrorIsLimitedToWindowsSharingErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{5, 32} {
		wrapped := fmt.Errorf("rename: %w", errno)
		if !isRetryableRenameError(wrapped, "windows") {
			t.Fatalf("expected Windows errno %d to be retryable", errno)
		}
		if isRetryableRenameError(wrapped, "linux") {
			t.Fatalf("did not expect errno %d to be retryable on Linux", errno)
		}
	}
	if isRetryableRenameError(syscall.Errno(2), "windows") {
		t.Fatal("file-not-found must not be retried")
	}
}

func TestInstallAndUninstallPreserveDataByDefault(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "goflow-agent_test_windows_amd64.zip")
	createTestArchive(t, archive)
	prefix := filepath.Join(root, "app")
	dataRoot := filepath.Join(root, "data")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := install(archive, prefix, dataRoot, false)
	if err != nil || result.Operation != "install" {
		t.Fatalf("install failed: %#v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin", "goflow.exe")); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	result, err = uninstall(prefix, false)
	if err != nil || result.DataRemoved {
		t.Fatalf("uninstall failed: %#v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "keep.txt")); err != nil {
		t.Fatalf("data was not preserved: %v", err)
	}
}

func TestUpgradeAndRollbackRestoreApplicationAndPreserveData(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "goflow-agent_test_windows_amd64.zip")
	createTestArchive(t, archive)
	prefix := filepath.Join(root, "app")
	dataRoot := filepath.Join(root, "data")
	if _, err := install(archive, prefix, dataRoot, false); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(prefix, "pre-upgrade.txt")
	if err := os.WriteFile(marker, []byte("restore me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := install(archive, prefix, dataRoot, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("upgrade did not replace application payload: %v", err)
	}
	result, err := rollback(prefix, "")
	if err != nil || result.Operation != "rollback" {
		t.Fatalf("rollback failed: %#v %v", result, err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "restore me" {
		t.Fatalf("rollback marker mismatch: %q %v", content, err)
	}
	if result.DataRoot != dataRoot {
		t.Fatalf("rollback changed data root: got %q want %q", result.DataRoot, dataRoot)
	}
}

func TestRollbackRejectsBackupOutsideManagedDirectory(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "app")
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(prefix, installManifest{Product: "GoFlow"}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := rollback(prefix, outside); err == nil {
		t.Fatal("expected an external rollback directory to be rejected")
	}
}

func TestRollbackRejectsBackupForAnotherInstallation(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "app")
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(prefix, installManifest{Product: "GoFlow"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, backupDir, "123-other-app")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := rollback(prefix, backup); err == nil {
		t.Fatal("expected another installation's backup to be rejected")
	}
}

func createTestArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	bundle := zip.NewWriter(file)
	defer bundle.Close()
	for name, content := range map[string]string{
		"goflow-agent_test_windows_amd64/bin/goflow.exe": "binary",
		"goflow-agent_test_windows_amd64/README.md":      "release",
	} {
		writer, err := bundle.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}
