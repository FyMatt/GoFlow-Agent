package dependencycache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreDeduplicatesAndMaterializesReadOnly(t *testing.T) {
	root := t.TempDir()
	store, err := New(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "helper.py")
	if err := os.WriteFile(source, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := store.PutFile(source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutFile(source)
	if err != nil || first.SHA256 != second.SHA256 || first.Path != second.Path {
		t.Fatalf("cache did not deduplicate: first=%#v second=%#v err=%v", first, second, err)
	}
	materialized, err := store.Materialize(first, filepath.Join(root, "task", "dependencies"), filepath.Base(source))
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(materialized); err != nil || string(content) != "print('ok')\n" {
		t.Fatalf("unexpected materialized content %q err=%v", content, err)
	}
	if info, err := os.Stat(materialized); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("materialized dependency must be read-only: info=%#v err=%v", info, err)
	}
}

func TestMaterializeRelativePreservesBundleLayoutAndExecutableMode(t *testing.T) {
	root := t.TempDir()
	store, err := New(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "server.py")
	if err := os.WriteFile(source, []byte("print('ok')\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PutFile(source)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "task", "dependencies", "bundle")
	module, err := store.MaterializeRelative(artifact, bundle, filepath.Join("package", "server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if module != filepath.Join(bundle, "package", "server.py") {
		t.Fatalf("relative bundle path was not preserved: %s", module)
	}
	if info, err := os.Stat(module); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("bundle module must be read-only: info=%v err=%v", info, err)
	}
	executable, err := store.MaterializeExecutable(artifact, filepath.Join(root, "task", "tools"), "python")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(executable); err != nil || info.Mode().Perm()&0o222 != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("sandbox command must stay executable where supported and non-writable: info=%v err=%v", info, err)
	}
	if _, err := store.MaterializeRelative(artifact, bundle, filepath.Join("..", "escape.py")); err == nil {
		t.Fatal("expected bundle path escape to be rejected")
	}
}

func TestMaterializeRejectsTamperedCacheObject(t *testing.T) {
	root := t.TempDir()
	store, err := New(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "helper.js")
	if err := os.WriteFile(source, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PutFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifact.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize(artifact, filepath.Join(root, "task"), "helper.js"); err == nil {
		t.Fatal("expected tampered dependency cache object to be rejected")
	}
}

func TestFetchRequiresHashAndStagesVerifiedObject(t *testing.T) {
	t.Parallel()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("verified dependency")
	digest := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	artifact, err := store.Fetch(context.Background(), server.URL, hex.EncodeToString(digest[:]), 1<<20)
	if err != nil {
		t.Fatalf("fetch dependency: %v", err)
	}
	if artifact.SHA256 != hex.EncodeToString(digest[:]) || artifact.Size != int64(len(body)) {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
	if info, err := os.Stat(artifact.Path); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("cache object must be read-only: info=%v err=%v", info, err)
	}
}

func TestFetchRejectsHashMismatchAndOversizedResponse(t *testing.T) {
	t.Parallel()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 32)))
	}))
	defer server.Close()
	if _, err := store.Fetch(context.Background(), server.URL, strings.Repeat("0", 64), 1<<20); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	if _, err := store.Fetch(context.Background(), server.URL, strings.Repeat("0", 64), 8); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestFetchRejectsNonHTTPSRemoteURL(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fetch(context.Background(), "http://example.com/dependency", strings.Repeat("0", 64), 1<<20); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS restriction, got %v", err)
	}
}
