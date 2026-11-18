package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretReferenceIsBounded(t *testing.T) {
	root := t.TempDir()
	reference := "secret://providers/local/api_key"
	path, err := SecretReferencePath(root, reference)
	if err != nil {
		t.Fatalf("secret path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("local-secret\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	value, err := ResolveSecretReference(root, reference)
	if err != nil || value != "local-secret" {
		t.Fatalf("resolve secret: value=%q err=%v", value, err)
	}
	if _, err := SecretReferencePath(root, "secret://providers/../outside"); err == nil {
		t.Fatal("expected traversal reference rejection")
	}
}

func TestMigratePlaintextProviderSecrets(t *testing.T) {
	runtimeHome := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "data")
	dir := filepath.Join(dataRoot, "resources", "providers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	modulePath := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(modulePath, []byte("local:\n  provider: openai-compatible\n  api_key: old-plain-secret\n  model: test\n"), 0o644); err != nil {
		t.Fatalf("write provider: %v", err)
	}
	migrated, err := MigratePlaintextProviderSecrets(runtimeHome, dataRoot)
	if err != nil || len(migrated) != 1 {
		t.Fatalf("migrate: files=%#v err=%v", migrated, err)
	}
	module, err := os.ReadFile(modulePath)
	if err != nil || strings.Contains(string(module), "old-plain-secret") || !strings.Contains(string(module), "secret://providers/local/api_key") {
		t.Fatalf("unexpected migrated module %q err=%v", module, err)
	}
	resolved, err := ResolveSecretReference(dataRoot, "secret://providers/local/api_key")
	if err != nil || resolved != "old-plain-secret" {
		t.Fatalf("resolve migrated secret: value=%q err=%v", resolved, err)
	}
}
