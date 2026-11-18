package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// All fixtures live in temporary directories; no project or user memory is read.
func BenchmarkFileIndexRead(b *testing.B) {
	for _, count := range []int{100, 2500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store := benchmarkMemoryStore(b, count)
			for _, name := range []string{"decode", "store", "cold"} {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var got FileIndex
						var err error
						switch name {
						case "store":
							got, err = store.FileIndex()
						case "cold":
							got, err = NewStore(store.Root()).FileIndex()
						default:
							err = readJSONFile(filepath.Join(store.Root(), fileIndexRelativePath), &got)
						}
						if err != nil || len(got.Files) != count {
							b.Fatalf("read index: files=%d err=%v", len(got.Files), err)
						}
					}
				})
			}
		})
	}
}

func benchmarkMemoryStore(b *testing.B, count int) *Store {
	b.Helper()
	store := NewStore(b.TempDir())
	index := FileIndex{GeneratedAt: "2026-10-24T00:00:00Z", TotalFiles: count}
	for i := 0; i < count; i++ {
		index.Files = append(index.Files, FileSummary{
			Path: fmt.Sprintf("domain-%d/document-%04d.md", i%8, i),
			Size: 4096, MTime: index.GeneratedAt, Hash: "0123456789abcdef",
			Language: "markdown", Symbols: []string{"Overview", "Procedure"},
			Summary: "Domain documentation, evidence, and reusable procedures.",
		})
	}
	if err := writeJSONFile(filepath.Join(store.Root(), fileIndexRelativePath), index); err != nil {
		b.Fatal(err)
	}
	return store
}

func BenchmarkPromptContextFresh(b *testing.B) {
	store := benchmarkMemoryStore(b, 2500)
	relative := "domain-7/document-0031.md"
	path := filepath.Join(store.Root(), filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Overview\nDomain documentation and reusable procedures.\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if _, err := store.PromptContextFresh(context.Background(), "document-0031.md", "task-benchmark"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := store.PromptContextFresh(context.Background(), "document-0031.md", "task-benchmark")
		if err != nil || len(got.Blocks) == 0 {
			b.Fatalf("prompt: context=%v err=%v", got, err)
		}
	}
}

func BenchmarkSummarizeLargeFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "reference.txt")
	content := strings.Repeat("domain reference evidence\n", 640*1024)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"read-all", "stream"} {
		b.Run(mode, func(b *testing.B) {
			b.SetBytes(info.Size())
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var got FileSummary
				var err error
				if mode == "read-all" {
					got, err = summarizeLargeFileReadAll(path, "reference.txt", info)
				} else {
					got, err = summarizeFile(path, "reference.txt", info)
				}
				if err != nil || got.Size != info.Size() || len(got.Hash) != 64 {
					b.Fatalf("summarize: file=%v err=%v", got, err)
				}
			}
		})
	}
}

// Original large-file path, retained as a reproducible benchmark baseline.
func summarizeLargeFileReadAll(path, rel string, info os.FileInfo) (FileSummary, error) {
	item := FileSummary{Path: rel, Size: info.Size(), MTime: info.ModTime().UTC().Format(time.RFC3339Nano), Language: languageForPath(rel)}
	data, err := os.ReadFile(path)
	if err != nil {
		return item, err
	}
	sum := sha256.Sum256(data)
	item.Hash = hex.EncodeToString(sum[:])
	item.Summary = fmt.Sprintf("Large %s file (%d bytes); content summary skipped until explicitly read.", fallbackText(item.Language, "workspace"), info.Size())
	return item, nil
}

func BenchmarkTasksRead(b *testing.B) {
	benchmarkTasksRead(b, true)
}

// Run with -benchtime=1x to measure the first read of newly written fixtures.
// This does not evict the OS filesystem cache.
func BenchmarkTasksReadFirstPass(b *testing.B) {
	benchmarkTasksRead(b, false)
}

// Alternate call order on the same files to reduce warmup/order bias.
func BenchmarkTasksReadPaired(b *testing.B) {
	for _, count := range []int{100, 1000} {
		store := benchmarkTaskStore(b, count)
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			if _, err := tasksSerial(store, 20); err != nil {
				b.Fatal(err)
			}
			if _, err := store.Tasks(20); err != nil {
				b.Fatal(err)
			}
			var serialTime, boundedTime time.Duration
			serial := func() {
				start := time.Now()
				got, err := tasksSerial(store, 20)
				serialTime += time.Since(start)
				if err != nil || len(got) != 20 {
					b.Fatalf("serial tasks: count=%d err=%v", len(got), err)
				}
			}
			bounded := func() {
				start := time.Now()
				got, err := store.Tasks(20)
				boundedTime += time.Since(start)
				if err != nil || len(got) != 20 {
					b.Fatalf("bounded tasks: count=%d err=%v", len(got), err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					serial()
					bounded()
				} else {
					bounded()
					serial()
				}
			}
			b.ReportMetric(float64(serialTime.Nanoseconds())/float64(b.N), "serial-ns/op")
			b.ReportMetric(float64(boundedTime.Nanoseconds())/float64(b.N), "bounded-ns/op")
		})
	}
}

func benchmarkTasksRead(b *testing.B, warm bool) {
	for _, count := range []int{16, 32, 33, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			var shared *Store
			if warm {
				shared = benchmarkTaskStore(b, count)
			}
			for _, mode := range []string{"serial", "bounded"} {
				b.Run(mode, func(b *testing.B) {
					store := shared
					if store == nil {
						store = benchmarkTaskStore(b, count)
					}
					read := store.Tasks
					if mode == "serial" {
						read = func(limit int) ([]TaskSummary, error) { return tasksSerial(store, limit) }
					}
					if warm {
						if _, err := read(20); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						got, err := read(20)
						if err != nil || len(got) != min(count, 20) || got[0].ID != fmt.Sprintf("task-%04d", count-1) {
							b.Fatalf("read tasks: count=%d err=%v", len(got), err)
						}
					}
				})
			}
		})
	}
}

func benchmarkTaskStore(b *testing.B, count int) *Store {
	b.Helper()
	store := NewStore(b.TempDir())
	for i := 0; i < count; i++ {
		task := TaskSummary{
			ID: fmt.Sprintf("task-%04d", i), UpdatedAt: fmt.Sprintf("2026-10-24T00:%02d:%02dZ", i/60, i%60),
			UserGoal:      "Review domain documentation and record evidence",
			KeyDecisions:  []string{"Keep source references", "Verify the result"},
			ModifiedFiles: []string{"docs/procedure.md"}, TestResults: []string{"verification passed"},
		}
		if err := writeJSONFile(filepath.Join(store.Root(), taskMemoryDirRelativePath, task.ID+".json"), task); err != nil {
			b.Fatal(err)
		}
	}
	return store
}

// Original task reader, retained for serial/parallel benchmarks and equivalence
// tests, including the input order of the existing unstable UpdatedAt sort.
func tasksSerial(s *Store, limit int) ([]TaskSummary, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.workspaceRoot, taskMemoryDirRelativePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	tasks := make([]TaskSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		var task TaskSummary
		if err := readJSONFile(filepath.Join(dir, entry.Name()), &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt > tasks[j].UpdatedAt })
	if limit > 0 && len(tasks) > limit {
		tasks = append([]TaskSummary(nil), tasks[:limit]...)
	}
	return tasks, nil
}
