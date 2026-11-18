package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDataMaintenanceBackupRestore(t *testing.T) {
	runtimeHome := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "data")
	t.Setenv("GOFLOW_DATA_ROOT", dataRoot)
	var output bytes.Buffer
	handled, err := runDataMaintenance(runtimeHome, []string{"--backup-data"}, &output)
	if err != nil || !handled {
		t.Fatalf("backup: handled=%t err=%v", handled, err)
	}
	entries, err := os.ReadDir(filepath.Join(dataRoot, "backups"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("backup files: entries=%#v err=%v", entries, err)
	}
	marker := filepath.Join(dataRoot, "marker.txt")
	if err := os.WriteFile(marker, []byte("changed"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	output.Reset()
	handled, err = runDataMaintenance(runtimeHome, []string{"--restore-data", entries[0].Name()}, &output)
	if err != nil || !handled {
		t.Fatalf("restore: handled=%t err=%v", handled, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("restore did not replace changed data, stat err=%v", err)
	}
	if !strings.Contains(output.String(), "Restart GoFlow") {
		t.Fatalf("missing restart guidance: %s", output.String())
	}
}
