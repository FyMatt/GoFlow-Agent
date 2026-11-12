// Package executionfingerprint creates deterministic, secret-free workspace
// state fingerprints for command evidence and validation reuse.
package executionfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Snapshot struct {
	Hash      string
	FileCount int
	ByteCount int64
}

func Workspace(root string) (Snapshot, error) {
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(root)))
	if err != nil || root == "" {
		return Snapshot{}, fmt.Errorf("workspace root is invalid")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Snapshot{}, fmt.Errorf("workspace root is not an existing directory")
	}
	entries := make([]string, 0)
	var totalBytes int64
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if shouldSkipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		entryHash, bytes, hashErr := hashFile(path, info.Size())
		if hashErr != nil {
			return hashErr
		}
		totalBytes += bytes
		entries = append(entries, fmt.Sprintf("%s\x00%d\x00%d\x00%s", filepath.ToSlash(relative), info.Size(), info.ModTime().UnixNano(), entryHash))
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("fingerprint workspace: %w", err)
	}
	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return Snapshot{Hash: hex.EncodeToString(digest[:]), FileCount: len(entries), ByteCount: totalBytes}, nil
}

func hashFile(path string, size int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	bytes, err := io.Copy(hash, file)
	if err != nil {
		return "", bytes, err
	}
	return hex.EncodeToString(hash.Sum(nil)), bytes, nil
}

func shouldSkipDirectory(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".goflow", "node_modules", ".cache":
		return true
	default:
		return false
	}
}
