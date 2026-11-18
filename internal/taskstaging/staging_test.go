package taskstaging

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBuildManifestApplyAndRestore(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "existing.txt"), "one\ntwo\nthree\n")
	writeTestFile(t, filepath.Join(workspace, "delete.txt"), "remove me\n")
	writeTestFile(t, filepath.Join(workspace, ".env"), "SECRET=value\n")
	writeTestFile(t, filepath.Join(workspace, ".env.example"), "SECRET=\n")

	task, err := Prepare(workspace, t.TempDir(), "run-1")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !task.LiveWorkspace() || !samePath(task.Workspace, workspace) {
		t.Fatalf("task must execute in the live workspace: %#v", task)
	}
	if _, err := os.Stat(filepath.Join(task.Input, ".env")); !os.IsNotExist(err) {
		t.Fatalf("secret file should be excluded from the recovery snapshot, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Input, ".env.example")); err != nil {
		t.Fatalf("example environment file should remain available: %v", err)
	}
	inputInfo, err := os.Stat(filepath.Join(task.Input, "existing.txt"))
	if err != nil || inputInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("input snapshot should be read-only, info=%v err=%v", inputInfo, err)
	}

	writeTestFile(t, filepath.Join(task.Workspace, "existing.txt"), "one\nchanged\nthree\n")
	assertTestFile(t, filepath.Join(task.Input, "existing.txt"), "one\ntwo\nthree\n")
	writeTestFile(t, filepath.Join(task.Workspace, "added.txt"), "new\n")
	if err := os.Remove(filepath.Join(task.Workspace, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	manifest, err := task.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if manifest.FilesAdded != 1 || manifest.FilesModified != 1 || manifest.FilesDeleted != 1 || len(manifest.Changes) != 3 {
		t.Fatalf("unexpected manifest summary: %#v", manifest)
	}
	modified := manifestChange(t, manifest, "existing.txt")
	if !strings.Contains(modified.Diff, "@@") || !strings.Contains(modified.Diff, "-two") || !strings.Contains(modified.Diff, "+changed") {
		t.Fatalf("expected inspectable unified diff, got %q", modified.Diff)
	}

	result, err := task.Apply(manifest)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state, ok, err := task.CurrentApply()
	if err != nil || !ok || state.Status != "applied" || state.ApplyID != result.ApplyID {
		t.Fatalf("unexpected durable apply state: state=%#v ok=%v err=%v", state, ok, err)
	}
	if _, err := task.Apply(manifest); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("expected duplicate apply rejection, got %v", err)
	}
	assertTestFile(t, filepath.Join(workspace, "existing.txt"), "one\nchanged\nthree\n")
	assertTestFile(t, filepath.Join(workspace, "added.txt"), "new\n")
	if _, err := os.Stat(filepath.Join(workspace, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file should be absent, stat err=%v", err)
	}

	if err := Restore(result.RecoveryPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	state, ok, err = task.CurrentApply()
	if err != nil || !ok || state.Status != "restored" {
		t.Fatalf("unexpected restored apply state: state=%#v ok=%v err=%v", state, ok, err)
	}
	assertTestFile(t, filepath.Join(workspace, "existing.txt"), "one\ntwo\nthree\n")
	assertTestFile(t, filepath.Join(workspace, "delete.txt"), "remove me\n")
	if _, err := os.Stat(filepath.Join(workspace, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("restored added file should be absent, stat err=%v", err)
	}
	if err := Cleanup(task); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Metadata, "baseline.json")); err != nil {
		t.Fatalf("cleanup must preserve bounded audit metadata: %v", err)
	}
}

func TestManifestExcludesRuntimeMetadataAndPythonCaches(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "web", "style.css"), "before\n")
	writeTestFile(t, filepath.Join(workspace, "web", ".venv", "Lib", "site.py"), "dependency\n")
	writeTestFile(t, filepath.Join(workspace, "web", "blog.db"), "original database\n")
	writeTestFile(t, filepath.Join(workspace, "交付证据", "report.json"), "{}\n")
	writeTestFile(t, filepath.Join(workspace, "人工验收", "notes.md"), "notes\n")
	task, err := Prepare(workspace, t.TempDir(), "run-runtime-noise")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Cleanup(task) })
	if _, err := os.Stat(filepath.Join(task.Input, "web", ".venv")); !os.IsNotExist(err) {
		t.Fatalf("virtual environment must not be copied into the recovery snapshot: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Workspace, "web", ".venv")); err != nil {
		t.Fatalf("live project dependencies must remain available: %v", err)
	}
	writeTestFile(t, filepath.Join(task.Workspace, "web", "style.css"), "after\n")
	writeTestFile(t, filepath.Join(task.Workspace, ".goflow", "file-journal", "entry.json"), "{}\n")
	writeTestFile(t, filepath.Join(task.Workspace, "web", "__pycache__", "app.pyc"), "bytecode")
	writeTestFile(t, filepath.Join(task.Workspace, "web", ".venv", "Lib", "__pycache__", "site.pyc"), "bytecode")
	writeTestFile(t, filepath.Join(task.Workspace, "web", "blog.db"), "migrated database copy\n")
	writeTestFile(t, filepath.Join(task.Workspace, "web", "blog.db-wal"), "runtime wal\n")
	writeTestFile(t, filepath.Join(task.Workspace, "交付证据", "generated.json"), "{}\n")
	writeTestFile(t, filepath.Join(task.Workspace, "人工验收", "generated.md"), "generated\n")
	manifest, err := task.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Changes) != 1 || manifest.Changes[0].Path != "web/style.css" {
		t.Fatalf("runtime files must not enter the delivery manifest: %#v", manifest.Changes)
	}
}

func TestApplyRejectsWorkspaceConflictBeforeAnyWrite(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "a.txt"), "a0\n")
	writeTestFile(t, filepath.Join(workspace, "b.txt"), "b0\n")
	task, err := Prepare(workspace, t.TempDir(), "run-conflict")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Cleanup(task) })
	writeTestFile(t, filepath.Join(task.Workspace, "a.txt"), "a1\n")
	writeTestFile(t, filepath.Join(task.Workspace, "b.txt"), "b1\n")
	manifest, err := task.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(workspace, "b.txt"), "user changed b\n")

	if _, err := task.Apply(manifest); err == nil || !strings.Contains(err.Error(), "changed after manifest") {
		t.Fatalf("expected stale live-workspace rejection, got %v", err)
	}
	assertTestFile(t, filepath.Join(workspace, "a.txt"), "a1\n")
	assertTestFile(t, filepath.Join(workspace, "b.txt"), "user changed b\n")
}

func TestManifestRejectsStagedOutputMutation(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "a.txt"), "before\n")
	task, err := Prepare(workspace, t.TempDir(), "run-stale")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Cleanup(task) })
	writeTestFile(t, filepath.Join(task.Workspace, "a.txt"), "manifest value\n")
	manifest, err := task.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(task.Workspace, "a.txt"), "changed after manifest\n")
	if _, err := task.Apply(manifest); err == nil || !strings.Contains(err.Error(), "changed after manifest") {
		t.Fatalf("expected stale staged output rejection, got %v", err)
	}
	assertTestFile(t, filepath.Join(workspace, "a.txt"), "changed after manifest\n")
}

func TestApplyRejectsSensitiveOutputPath(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "README.md"), "ok\n")
	task, err := Prepare(workspace, t.TempDir(), "run-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Cleanup(task) })
	writeTestFile(t, filepath.Join(task.Workspace, ".env"), "TOKEN=secret\n")
	manifest, err := task.BuildManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Changes) != 0 {
		t.Fatalf("sensitive files must not enter version manifests: %#v", manifest.Changes)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".env")); err != nil {
		t.Fatalf("direct workspace mutation should remain visible for explicit cleanup, stat err=%v", err)
	}
}

func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replace.txt")
	writeTestFile(t, path, "before\n")
	if err := writeFileAtomic(path, []byte("after\n"), 0o640); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	assertTestFile(t, path, "after\n")
}

func TestPrepareGitWorkspaceUsesLiveRepositoryAndExternalSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	workspace := t.TempDir()
	runGit(t, workspace, "init")
	runGit(t, workspace, "config", "user.email", "test@example.invalid")
	runGit(t, workspace, "config", "user.name", "GoFlow Test")
	writeTestFile(t, filepath.Join(workspace, "tracked.txt"), "committed\n")
	runGit(t, workspace, "add", "tracked.txt")
	runGit(t, workspace, "commit", "-m", "initial")
	writeTestFile(t, filepath.Join(workspace, "tracked.txt"), "dirty\n")
	writeTestFile(t, filepath.Join(workspace, "untracked.txt"), "new\n")

	task, err := Prepare(workspace, t.TempDir(), "run-git")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = Cleanup(task) })
	if task.Baseline.Strategy != LiveWorkspaceStrategy || !samePath(task.Workspace, workspace) {
		t.Fatalf("expected live Git workspace, got strategy=%q workspace=%q", task.Baseline.Strategy, task.Workspace)
	}
	assertTestFile(t, filepath.Join(task.Workspace, "tracked.txt"), "dirty\n")
	assertTestFile(t, filepath.Join(task.Workspace, "untracked.txt"), "new\n")
	status := runGit(t, task.Workspace, "status", "--short")
	if !strings.Contains(status, "M tracked.txt") || !strings.Contains(status, "?? untracked.txt") {
		t.Fatalf("task Git status does not reflect the current workspace snapshot: %q", status)
	}
	if _, err := os.Stat(filepath.Join(task.Input, ".git")); !os.IsNotExist(err) {
		t.Fatalf("recovery snapshot must not duplicate Git metadata, err=%v", err)
	}
}

func TestLiveWorkspaceVersionsRetainThreeAndRollbackInOrder(t *testing.T) {
	workspace := t.TempDir()
	stagingRoot := t.TempDir()
	path := filepath.Join(workspace, "version.txt")
	writeTestFile(t, path, "v0\n")
	results := make(map[string]ApplyResult)
	for index := 1; index <= 4; index++ {
		runID := fmt.Sprintf("run-%d", index)
		task, err := Prepare(workspace, stagingRoot, runID)
		if err != nil {
			t.Fatalf("Prepare %s: %v", runID, err)
		}
		writeTestFile(t, path, fmt.Sprintf("v%d\n", index))
		manifest, err := task.BuildManifest()
		if err != nil {
			t.Fatalf("BuildManifest %s: %v", runID, err)
		}
		result, err := task.Apply(manifest)
		if err != nil {
			t.Fatalf("Apply %s: %v", runID, err)
		}
		results[runID] = result
		if err := PruneVersions(stagingRoot, workspace, DefaultVersionRetention); err != nil {
			t.Fatalf("PruneVersions %s: %v", runID, err)
		}
	}
	versions, err := ListVersions(stagingRoot, workspace, DefaultVersionRetention)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].RunID != "run-4" || versions[1].RunID != "run-3" || versions[2].RunID != "run-2" {
		t.Fatalf("unexpected retained versions: %#v", versions)
	}
	if _, err := os.Stat(filepath.Join(stagingRoot, "run-1")); !os.IsNotExist(err) {
		t.Fatalf("oldest version should be pruned, err=%v", err)
	}
	if !versions[0].CanRestore || versions[1].CanRestore || versions[2].CanRestore {
		t.Fatalf("only the latest matching version should be restorable: %#v", versions)
	}
	if err := Restore(results["run-4"].RecoveryPath); err != nil {
		t.Fatalf("restore latest: %v", err)
	}
	assertTestFile(t, path, "v3\n")
	versions, err = ListVersions(stagingRoot, workspace, DefaultVersionRetention)
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].Status != "restored" || !versions[1].CanRestore {
		t.Fatalf("previous version should become restorable after one rollback: %#v", versions)
	}
}

func manifestChange(t *testing.T, manifest Manifest, path string) Change {
	t.Helper()
	for _, change := range manifest.Changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("manifest change not found: %s", path)
	return Change{}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != expected {
		t.Fatalf("unexpected %s content: %q", path, data)
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
