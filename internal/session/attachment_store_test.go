package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestAttachmentObjectStorePersistsRawContentByHash(t *testing.T) {
	root := t.TempDir()
	store := NewAttachmentObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	payload := []byte{0x00, 0xff, 0x10, 0x80, 'a'}
	stored, existed, err := store.Put(payload, schema.AttachmentRef{Name: "capture.bin", MIME: "application/octet-stream", Kind: "file"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if existed || stored.Ref == "" || stored.Hash == "" || stored.Size != int64(len(payload)) {
		t.Fatalf("unexpected stored metadata: %#v existed=%t", stored, existed)
	}
	meta, loaded, ok, err := store.Get(stored.Ref)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%t err=%v", ok, err)
	}
	if meta.Hash != stored.Hash || !bytes.Equal(loaded, payload) {
		t.Fatalf("round trip mismatch: meta=%#v payload=%v", meta, loaded)
	}
	if _, err := os.Stat(filepath.Join(root, attachmentObjectsRelativePath, stored.Hash+".blob")); err != nil {
		t.Fatalf("raw object missing: %v", err)
	}
	if second, existed, err := store.Put(payload, schema.AttachmentRef{Name: "renamed.bin", MIME: "application/octet-stream", Kind: "file"}); err != nil || !existed || second.Hash != stored.Hash {
		t.Fatalf("deduplicated Put: second=%#v existed=%t err=%v", second, existed, err)
	}
}

func TestAttachmentObjectStoreRejectsInvalidReference(t *testing.T) {
	store := NewAttachmentObjectStore(t.TempDir())
	if _, _, ok, err := store.Get("../../outside"); err != nil || ok {
		t.Fatalf("invalid reference must not resolve: ok=%t err=%v", ok, err)
	}
}
