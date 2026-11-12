package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedJSONChangeBacksUpVerifiesAndRestores(t *testing.T) {
	managedRoot := t.TempDir()
	backupRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\n  \"service\": {\n    \"enabled\": false,\n    \"workers\": 2\n  }\n}\n")
	if err := os.WriteFile(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", backupRoot)

	inspection, err := inspectManagedJSON(diagnosticInput{Path: path})
	if err != nil || inspection["values_redacted"] != true || inspection["sha256"] != managedSHA256(before) {
		t.Fatalf("unexpected managed inspection: payload=%#v err=%v", inspection, err)
	}
	change, err := setManagedJSONValue(diagnosticInput{
		Path: path, JSONPointer: "/service/enabled", Value: true,
		ExpectedSHA: managedSHA256(before), ChangeReason: "enable the fixture service",
		BackupRequired: true, PostCheckRequired: true,
	})
	if err != nil || change["status"] != "committed" || change["backup_stored"] != true || change["post_check_passed"] != true || change["read_only"] != false {
		t.Fatalf("unexpected managed change: payload=%#v err=%v", change, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(after, &decoded); err != nil || decoded["service"].(map[string]any)["enabled"] != true {
		t.Fatalf("managed change was not applied: data=%s err=%v", after, err)
	}
	changeID := change["change_id"].(string)
	recovery, err := restoreManagedJSONChange(diagnosticInput{
		ChangeID: changeID, ExpectedSHA: change["after_sha256"].(string),
		RecoveryReason: "restore fixture baseline", PostCheckRequired: true,
	})
	if err != nil || recovery["status"] != "restored" || recovery["restored_sha256"] != managedSHA256(before) || recovery["read_only"] != false {
		t.Fatalf("unexpected managed recovery: payload=%#v err=%v", recovery, err)
	}
	restored, err := os.ReadFile(path)
	if err != nil || string(restored) != string(before) {
		t.Fatalf("managed backup was not restored: data=%s err=%v", restored, err)
	}
}

func TestManagedJSONChangeRejectsUnsafeOrStaleRequests(t *testing.T) {
	managedRoot := t.TempDir()
	backupRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\"enabled\":false}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", backupRoot)

	if _, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/enabled", Value: true, ExpectedSHA: managedSHA256(before), ChangeReason: "test", PostCheckRequired: true}); err == nil {
		t.Fatal("expected missing backup requirement rejection")
	}
	if _, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/enabled", Value: true, ExpectedSHA: managedSHA256([]byte("stale")), ChangeReason: "test", BackupRequired: true, PostCheckRequired: true}); err == nil {
		t.Fatal("expected stale SHA rejection")
	}
	if _, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/missing", Value: true, ExpectedSHA: managedSHA256(before), ChangeReason: "test", BackupRequired: true, PostCheckRequired: true}); err == nil {
		t.Fatal("expected missing key rejection")
	}
	if _, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/enabled~2invalid", Value: true, ExpectedSHA: managedSHA256(before), ChangeReason: "test", BackupRequired: true, PostCheckRequired: true}); err == nil {
		t.Fatal("expected invalid JSON pointer escape rejection")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectManagedJSON(diagnosticInput{Path: outside}); err == nil {
		t.Fatal("expected managed root rejection")
	}
}

func TestManagedJSONRecoveryRejectsConcurrentEdit(t *testing.T) {
	managedRoot := t.TempDir()
	backupRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\"enabled\":false}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", backupRoot)
	change, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/enabled", Value: true, ExpectedSHA: managedSHA256(before), ChangeReason: "test", BackupRequired: true, PostCheckRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"enabled\":true,\"external\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = restoreManagedJSONChange(diagnosticInput{ChangeID: change["change_id"].(string), ExpectedSHA: change["after_sha256"].(string), RecoveryReason: "test", PostCheckRequired: true})
	if err == nil {
		t.Fatal("expected concurrent edit recovery rejection")
	}
}

func TestManagedJSONToolsDeclareWriteRisk(t *testing.T) {
	kinds := map[string]string{}
	for _, item := range builtinTools() {
		kinds[item.Name] = item.Kind
	}
	if kinds["inspect_managed_json"] != "read" || kinds["plan_windows_json_change"] != "read" || kinds["set_managed_json_value"] != "write" || kinds["restore_managed_json_change"] != "write" {
		t.Fatalf("unexpected managed JSON tool kinds: %#v", kinds)
	}
}

func TestManagedJSONBackupRootMustBeSeparate(t *testing.T) {
	managedRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\"enabled\":false}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", filepath.Join(managedRoot, "backups"))
	_, err := setManagedJSONValue(diagnosticInput{Path: path, JSONPointer: "/enabled", Value: true, ExpectedSHA: managedSHA256(before), ChangeReason: "test", BackupRequired: true, PostCheckRequired: true})
	if err == nil {
		t.Fatal("expected overlapping backup root rejection")
	}
}

func TestManagedJSONPostCheckFailureRollsBackAndRetainsEvidence(t *testing.T) {
	managedRoot := t.TempDir()
	backupRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\"enabled\":false}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", backupRoot)
	originalVerify := verifyManagedJSONStateCall
	verifyManagedJSONStateCall = func(path, expectedSHA, pointer string, expectedValue any) error {
		if pointer != "" {
			return errors.New("injected post-check failure")
		}
		return originalVerify(path, expectedSHA, pointer, expectedValue)
	}
	t.Cleanup(func() { verifyManagedJSONStateCall = originalVerify })

	_, err := setManagedJSONValue(diagnosticInput{
		Path: path, JSONPointer: "/enabled", Value: true, ExpectedSHA: managedSHA256(before),
		ChangeReason: "exercise rollback", BackupRequired: true, PostCheckRequired: true,
	})
	if err == nil {
		t.Fatal("expected injected post-check failure")
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil || string(current) != string(before) {
		t.Fatalf("post-check failure did not restore the before image: data=%q err=%v", current, readErr)
	}
	entries, readDirErr := os.ReadDir(backupRoot)
	if readDirErr != nil || len(entries) != 1 {
		t.Fatalf("expected one retained change journal: entries=%v err=%v", entries, readDirErr)
	}
	record, manifestErr := readManagedChangeManifest(backupRoot, entries[0].Name())
	if manifestErr != nil || record.Status != "rolled_back" || record.RollbackStatus != "passed" || record.PostCheckPassed {
		t.Fatalf("unexpected rollback evidence: record=%#v err=%v", record, manifestErr)
	}
	backup, backupErr := os.ReadFile(managedChangeBackupPath(backupRoot, record.ID))
	if backupErr != nil || managedSHA256(backup) != record.BeforeSHA256 {
		t.Fatalf("retained backup failed verification: err=%v", backupErr)
	}
}

func TestManagedJSONRecoveryReconcilesPreparedChange(t *testing.T) {
	managedRoot := t.TempDir()
	backupRoot := t.TempDir()
	path := filepath.Join(managedRoot, "app.json")
	before := []byte("{\"enabled\":false}\n")
	after := []byte("{\n  \"enabled\": true\n}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_MANAGED_CONFIG_ROOTS", managedRoot)
	t.Setenv("GOFLOW_HOST_CHANGE_BACKUP_ROOT", backupRoot)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	record := managedConfigChange{
		Version: managedConfigJournalVersion, ID: "0123456789abcdef01234567", TargetID: localTargetID,
		Path: path, JSONPointer: "/enabled", BeforeSHA256: managedSHA256(before), AfterSHA256: managedSHA256(after),
		BeforeMode: uint32(info.Mode().Perm()), Status: "prepared", ChangeReason: "interrupted fixture", BackupStored: true,
		RecoveryScope: "managed_json_file",
	}
	if err := storeManagedChange(backupRoot, record, before); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteManagedFile(path, after, 0o600); err != nil {
		t.Fatal(err)
	}
	recovery, err := restoreManagedJSONChange(diagnosticInput{
		ChangeID: record.ID, ExpectedSHA: record.AfterSHA256,
		RecoveryReason: "reconcile interrupted change", PostCheckRequired: true,
	})
	if err != nil || recovery["status"] != "restored" || recovery["post_check"] != "passed" || recovery["recovery_post_check_passed"] != true {
		t.Fatalf("prepared change was not recoverable: payload=%#v err=%v", recovery, err)
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil || string(current) != string(before) {
		t.Fatalf("prepared change did not restore the before image: data=%q err=%v", current, readErr)
	}
}
