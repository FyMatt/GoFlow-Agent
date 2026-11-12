package memory

import (
	"os"
	"path/filepath"
	"sync"
)

// Read every file, keeping its directory position even if reads finish out of
// order. Sorting and limiting must see the same input as the serial reader.
func readTasksParallel(dir string, entries []os.DirEntry) []TaskSummary {
	const workers = 8
	tasks := make([]TaskSummary, len(entries))
	loaded := make([]bool, len(entries))
	var readers sync.WaitGroup
	for worker := range workers {
		readers.Go(func() {
			for i := worker; i < len(entries); i += workers {
				if err := readJSONFile(filepath.Join(dir, entries[i].Name()), &tasks[i]); err == nil {
					loaded[i] = true
				}
			}
		})
	}
	readers.Wait()
	n := 0
	for i, ok := range loaded {
		if ok {
			tasks[n] = tasks[i]
			n++
		}
	}
	clear(tasks[n:])
	return tasks[:n]
}
