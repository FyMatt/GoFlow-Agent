package session

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const sessionArchiveSuffix = ".full.json.gz"

var fullSessionArchiveRename = os.Rename

func writeFullSessionArchive(sessionPath string, snapshot Snapshot) error {
	archivePath := fullSessionArchivePath(sessionPath)
	if archivePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(archivePath), filepath.Base(archivePath)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	gz := gzip.NewWriter(temp)
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		_ = gz.Close()
		_ = temp.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	_ = os.Remove(archivePath)
	if err := fullSessionArchiveRename(tempPath, archivePath); err != nil {
		data, readErr := os.ReadFile(tempPath)
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		if writeErr := os.WriteFile(archivePath, data, 0o644); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		return nil
	}
	removeTemp = false
	return nil
}

func loadFullSessionArchive(sessionPath string) (Snapshot, bool, error) {
	archivePath := fullSessionArchivePath(sessionPath)
	if archivePath == "" {
		return Snapshot{}, false, nil
	}
	file, err := os.Open(archivePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, false, nil
		}
		return Snapshot{}, false, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Snapshot{}, false, err
	}
	defer gz.Close()
	var snapshot Snapshot
	if err := json.NewDecoder(gz).Decode(&snapshot); err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func fullSessionArchivePath(sessionPath string) string {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return ""
	}
	ext := filepath.Ext(sessionPath)
	if ext == "" {
		return sessionPath + sessionArchiveSuffix
	}
	return strings.TrimSuffix(sessionPath, ext) + sessionArchiveSuffix
}
