package persistence

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreTombstoneAndMigration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	store := New(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := store.SetTombstone("skills", "builtin-review", true); err != nil {
		t.Fatalf("set tombstone: %v", err)
	}
	if hidden, err := store.IsTombstoned("skills", "builtin-review"); err != nil || !hidden {
		t.Fatalf("expected tombstone, hidden=%t err=%v", hidden, err)
	}
	if err := store.SetTombstone("skills", "builtin-review", false); err != nil {
		t.Fatalf("clear tombstone: %v", err)
	}
	if hidden, err := store.IsTombstoned("skills", "builtin-review"); err != nil || hidden {
		t.Fatalf("expected cleared tombstone, hidden=%t err=%v", hidden, err)
	}

	legacy := filepath.Join(t.TempDir(), "legacy.yaml")
	if err := os.WriteFile(legacy, []byte("name: migrated\n"), 0o640); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	migrated, err := store.MigrateFile(legacy, filepath.Join("resources", "agents", "migrated.yaml"))
	if err != nil || !migrated {
		t.Fatalf("migrate: migrated=%t err=%v", migrated, err)
	}
	migrated, err = store.MigrateFile(legacy, filepath.Join("resources", "agents", "migrated.yaml"))
	if err != nil || migrated {
		t.Fatalf("migration must be idempotent: migrated=%t err=%v", migrated, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "resources", "agents", "migrated.yaml"))
	if err != nil || string(data) != "name: migrated\n" {
		t.Fatalf("unexpected migrated content %q err=%v", data, err)
	}
}

func TestStoreBackupRestoreRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	store := New(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	path := filepath.Join(root, "resources", "workflows", "review", "workflow.yaml")
	if err := WriteFileAtomic(path, []byte("name: before\n"), 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}
	backup, err := store.CreateBackup()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("name: after\n"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if err := store.RestoreBackup(backup); err != nil {
		t.Fatalf("restore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "name: before\n" {
		t.Fatalf("unexpected restored content %q err=%v", data, err)
	}
}

func TestRestoreRejectsTraversalWithoutChangingData(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	store := New(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	path := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	archive := filepath.Join(parent, "invalid.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	writer, _ := zw.Create("../escape.txt")
	_, _ = writer.Write([]byte("escape"))
	_ = zw.Close()
	_ = file.Close()
	if err := store.RestoreBackup(archive); err == nil {
		t.Fatal("expected traversal restore rejection")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("current data changed after rejected restore: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped file, stat err=%v", err)
	}
}

func TestWriteFileAtomicRejectsDirectoryTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	marker := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write directory marker: %v", err)
	}

	if err := WriteFileAtomic(target, []byte("replace"), 0o644); err == nil {
		t.Fatal("expected directory target to be rejected")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatalf("directory target changed after rejected write: %q err=%v", data, err)
	}
}

func TestPathWithinRootBoundaries(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	relative := filepath.Join("notifications", "outbox", "event.json")
	if got, err := store.PathWithinRoot(relative); err != nil || got != filepath.Join(root, relative) {
		t.Fatalf("valid data path: %q, %v", got, err)
	}
	for _, invalid := range []string{".", "..", filepath.Join("..", "outside.json"), filepath.Join(root, "absolute.json"), filepath.VolumeName(root) + "outside.json"} {
		if invalid == "outside.json" { // No volume on Unix.
			continue
		}
		if _, err := store.PathWithinRoot(invalid); err == nil {
			t.Errorf("accepted invalid relative path %q", invalid)
		}
	}
	if _, err := New("").PathWithinRoot(relative); err == nil {
		t.Fatal("accepted unconfigured data root")
	}
	link := filepath.Join(root, "notifications")
	target := t.TempDir()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		// Directory junctions exercise the boundary without symlink privileges.
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("create test junction: %v\n%s", err, output)
		}
	}
	if _, err := store.PathWithinRoot(relative); err == nil {
		t.Fatal("accepted a symbolic link below the data root")
	}
}
