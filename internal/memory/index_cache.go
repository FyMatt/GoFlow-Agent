package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

const maxCachedFileIndexBytes = 4 * 1024 * 1024

// Keep only one decoded index per store. Never cache query results or prompts.
type fileIndexCache struct {
	hash  [sha256.Size]byte
	index FileIndex
}

func (s *Store) readFileIndex() (FileIndex, error) {
	// Read every time: timestamps and sizes alone miss same-metadata edits,
	// replacements by other stores, and restores of persisted state.
	data, err := os.ReadFile(filepath.Join(s.workspaceRoot, fileIndexRelativePath))
	s.indexCacheMu.Lock()
	defer s.indexCacheMu.Unlock()
	if err != nil {
		s.indexCache = nil
		return FileIndex{}, err
	}
	hash := sha256.Sum256(data)
	if s.indexCache != nil && s.indexCache.hash == hash {
		return cloneFileIndex(s.indexCache.index), nil
	}
	s.indexCache = nil
	var index FileIndex
	// Preserve the existing decoder's handling of persisted JSON.
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&index); err != nil {
		return FileIndex{}, err
	}
	if len(data) > maxCachedFileIndexBytes || len(index.Files) > maxIndexedFiles {
		return index, nil
	}
	s.indexCache = &fileIndexCache{hash: hash, index: index}
	return cloneFileIndex(index), nil
}

func cloneFileIndex(index FileIndex) FileIndex {
	index.Files = slices.Clone(index.Files)
	for i := range index.Files {
		index.Files[i].Symbols = slices.Clone(index.Files[i].Symbols)
	}
	return index
}
