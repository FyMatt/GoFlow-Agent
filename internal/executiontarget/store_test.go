package executiontarget

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStorePersistsValidatedSSHTarget(t *testing.T) {
	store := NewStore(t.TempDir())
	saved, err := store.Put(Target{
		ID: "edge-a", Kind: KindSSH, DisplayName: "边缘主机 A", Host: "192.0.2.10", User: "operator",
		CredentialRef:   "secret://targets/edge-a/private_key",
		HostKeySHA256:   "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		AllowedCommands: []string{" uptime ", "df -h", "uptime"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.Port != 22 || len(saved.AllowedCommands) != 2 || saved.CreatedAt == "" || saved.UpdatedAt == "" {
		t.Fatalf("unexpected saved target %#v", saved)
	}
	loaded, ok, err := store.Get("EDGE-A")
	if err != nil || !ok || loaded.CredentialRef != saved.CredentialRef {
		t.Fatalf("Get: target=%#v ok=%t err=%v", loaded, ok, err)
	}
	items, err := store.List()
	if err != nil || len(items) != 2 || items[0].ID != LocalID || items[1].ID != "edge-a" {
		t.Fatalf("List: items=%#v err=%v", items, err)
	}
	info, err := os.Stat(filepath.Join(store.root, "edge-a.json"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("target file permissions: info=%#v err=%v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("target file permissions are too broad: %v", info.Mode().Perm())
	}
	deleted, err := store.Delete("edge-a")
	if err != nil || !deleted {
		t.Fatalf("Delete: deleted=%t err=%v", deleted, err)
	}
}

func TestValidateRejectsUnsafeTargetFields(t *testing.T) {
	base := Target{
		SchemaVersion: SchemaVersion, ID: "edge-a", Kind: KindSSH, DisplayName: "Edge A",
		Host: "edge.example.test", Port: 22, User: "operator",
		CredentialRef:   "secret://targets/edge-a/private_key",
		HostKeySHA256:   "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		AllowedCommands: []string{"uptime"}, Enabled: true,
	}
	cases := []Target{
		func() Target { item := base; item.Host = "https://edge.example.test"; return item }(),
		func() Target { item := base; item.CredentialRef = "private-key-value"; return item }(),
		func() Target { item := base; item.HostKeySHA256 = "accept-new"; return item }(),
		func() Target { item := base; item.AllowedCommands = []string{"uptime; reboot"}; return item }(),
	}
	for i, item := range cases {
		if err := Validate(item); err == nil {
			t.Fatalf("case %d unexpectedly passed", i)
		}
	}
	if _, err := storeForTest(t).Put(Local()); err == nil {
		t.Fatal("expected local target mutation rejection")
	}
}

func storeForTest(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}
