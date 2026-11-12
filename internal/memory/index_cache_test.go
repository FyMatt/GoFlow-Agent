package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestFileIndexCacheTracksPersistedContent(t *testing.T) {
	store := NewStore(t.TempDir())
	path := filepath.Join(store.Root(), fileIndexRelativePath)
	want := FileIndex{GeneratedAt: "original", TotalFiles: 1, Files: []FileSummary{{Path: "docs.md", Summary: "alpha", Symbols: []string{"Overview"}}}}
	write := func(index FileIndex) {
		t.Helper()
		if err := writeJSONFile(path, index); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		got, err := store.FileIndex()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("index=%#v want=%#v err=%v", got, want, err)
		}
	}
	write(want)
	check()
	got, err := store.FileIndex()
	if err != nil {
		t.Fatal(err)
	}
	got.Files[0].Path = "caller-only.md"
	got.Files[0].Symbols[0] = "caller-only symbol"
	check()

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want.Files[0].Summary = "bravo"
	write(want)
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("same-metadata edit not established: info=%v err=%v", after, err)
	}
	check()

	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FileIndex(); err == nil {
		t.Fatal("corrupt persisted index must not return cached success")
	}
	write(want)
	check()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, err := store.FileIndex(); err != nil || !reflect.DeepEqual(got, FileIndex{}) {
		t.Fatalf("removed index returned stale content: %#v err=%v", got, err)
	}
	write(want)
	check()
	// An independent store must observe persisted state, without cache recovery.
	got, err = NewStore(store.Root()).FileIndex()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened index=%#v err=%v", got, err)
	}
}

func TestFileIndexCacheBoundDoesNotTruncateIndex(t *testing.T) {
	store := NewStore(t.TempDir())
	path := filepath.Join(store.Root(), fileIndexRelativePath)
	if err := writeJSONFile(path, FileIndex{GeneratedAt: "small"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FileIndex(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []FileIndex{
		{Files: []FileSummary{{Path: "large.md", Summary: strings.Repeat("x", maxCachedFileIndexBytes)}}},
		{Files: make([]FileSummary, maxIndexedFiles+1)},
	} {
		if err := writeJSONFile(path, want); err != nil {
			t.Fatal(err)
		}
		got, err := store.FileIndex()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("uncached index changed: files=%d err=%v", len(got.Files), err)
		}
		if store.indexCache != nil {
			t.Fatal("oversized index was retained in the cache")
		}
	}
}

func TestFileIndexCacheConcurrentReadersAndWorkspaceIsolation(t *testing.T) {
	// Both stores use the same relative file paths but different content.
	stores := []*Store{NewStore(t.TempDir()), NewStore(t.TempDir())}
	for _, store := range stores {
		want := FileIndex{GeneratedAt: store.Root(), Files: []FileSummary{{Path: "shared.md", Summary: store.Root(), Symbols: []string{"original"}}}}
		if err := writeJSONFile(filepath.Join(store.Root(), fileIndexRelativePath), want); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		for _, store := range stores {
			wg.Go(func() {
				for range 20 {
					got, err := store.FileIndex()
					if err != nil || len(got.Files) != 1 || got.Files[0].Summary != store.Root() || got.Files[0].Symbols[0] != "original" {
						t.Errorf("shared or corrupted snapshot: %#v err=%v", got, err)
						return
					}
					got.Files[0].Summary = "caller mutation"
					got.Files[0].Symbols[0] = "caller mutation"
				}
			})
		}
	}
	wg.Wait()
}

func TestPromptContextCacheKeepsQueriesAndWorkspacesIsolated(t *testing.T) {
	for _, marker := range []string{"workspace-alpha", "workspace-bravo"} {
		store := NewStore(t.TempDir())
		for _, domain := range []string{"operations", "research"} {
			content := "# " + domain + "\n" + marker + " " + domain + " procedures.\n"
			if err := os.WriteFile(filepath.Join(store.Root(), domain+".md"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.RebuildFiles(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, domain := range []string{"operations", "research", "operations"} {
			got, err := store.PromptContextFresh(context.Background(), domain, marker+"-"+domain)
			if err != nil {
				t.Fatal(err)
			}
			files := 0
			for _, block := range got.Blocks {
				if block.Kind != "file" {
					continue
				}
				files++
				if block.Ref != domain+".md" || !strings.Contains(block.Summary, marker) {
					t.Fatalf("prompt mixed queries or workspaces: %#v", block)
				}
			}
			if files != 1 {
				t.Fatalf("expected one domain file, got %#v", got)
			}
		}
	}
}

func TestSummarizeFilePreservesFullHashAcrossSizeBoundary(t *testing.T) {
	for _, size := range []int{maxIndexedFileBytes, maxIndexedFileBytes + 1, 3*maxIndexedFileBytes + 17} {
		content := []byte(strings.Repeat("x", size))
		content[len(content)-1] = 'y'
		path := filepath.Join(t.TempDir(), "reference.txt")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := summarizeFile(path, "reference.txt", info)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(content)
		if got.Hash != hex.EncodeToString(hash[:]) || got.Size != int64(size) {
			t.Fatalf("incomplete hash or size for %d bytes: %#v", size, got)
		}
		if strings.HasPrefix(got.Summary, "Large ") != (size > maxIndexedFileBytes) {
			t.Fatalf("summary changed at size boundary %d: %q", size, got.Summary)
		}
		if size > maxIndexedFileBytes {
			want, err := summarizeLargeFileReadAll(path, "reference.txt", info)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("streaming changed the summary: got=%#v want=%#v err=%v", got, want, err)
			}
		}
	}
}
