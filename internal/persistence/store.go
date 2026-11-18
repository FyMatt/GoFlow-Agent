package persistence

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

type Store struct {
	root string
}

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type Tombstone struct {
	Family    string `json:"family"`
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
}

type TombstoneIndex struct {
	SchemaVersion int         `json:"schema_version"`
	Items         []Tombstone `json:"items,omitempty"`
}

type MigrationEntry struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	MigratedAt  string `json:"migrated_at"`
}

type MigrationJournal struct {
	SchemaVersion int              `json:"schema_version"`
	Items         []MigrationEntry `json:"items,omitempty"`
}

func New(root string) *Store {
	return &Store{root: filepath.Clean(strings.TrimSpace(root))}
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) ResourceDir(family string) (string, error) {
	family, err := cleanSegment(family)
	if err != nil {
		return "", fmt.Errorf("resource family: %w", err)
	}
	if err := s.ensureEnabled(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, "resources", family), nil
}

func (s *Store) Ensure() error {
	if err := s.ensureEnabled(); err != nil {
		return err
	}
	for _, dir := range []string{s.root, filepath.Join(s.root, "resources"), filepath.Join(s.root, "backups")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create data directory %s: %w", dir, err)
		}
	}
	path := filepath.Join(s.root, "manifest.json")
	if data, err := os.ReadFile(path); err == nil {
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("parse data manifest: %w", err)
		}
		return validateManifest(manifest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read data manifest: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONAtomic(path, Manifest{SchemaVersion: SchemaVersion, CreatedAt: now, UpdatedAt: now}, 0o644)
}

func (s *Store) IsTombstoned(family, name string) (bool, error) {
	family, err := cleanSegment(family)
	if err != nil {
		return false, err
	}
	name, err = cleanSegment(name)
	if err != nil {
		return false, err
	}
	index, err := s.readTombstones()
	if err != nil {
		return false, err
	}
	for _, item := range index.Items {
		if item.Family == family && item.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) SetTombstone(family, name string, deleted bool) error {
	family, err := cleanSegment(family)
	if err != nil {
		return err
	}
	name, err = cleanSegment(name)
	if err != nil {
		return err
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	index, err := s.readTombstones()
	if err != nil {
		return err
	}
	items := make([]Tombstone, 0, len(index.Items)+1)
	found := false
	for _, item := range index.Items {
		if item.Family == family && item.Name == name {
			found = true
			if deleted {
				items = append(items, Tombstone{Family: family, Name: name, DeletedAt: time.Now().UTC().Format(time.RFC3339Nano)})
			}
			continue
		}
		items = append(items, item)
	}
	if deleted && !found {
		items = append(items, Tombstone{Family: family, Name: name, DeletedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Family != items[j].Family {
			return items[i].Family < items[j].Family
		}
		return items[i].Name < items[j].Name
	})
	index.SchemaVersion = SchemaVersion
	index.Items = items
	return writeJSONAtomic(filepath.Join(s.root, "tombstones.json"), index, 0o644)
}

func (s *Store) MigrateFile(source, destination string) (bool, error) {
	if err := s.Ensure(); err != nil {
		return false, err
	}
	target, err := s.pathWithinRoot(destination)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(target); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat migration target: %w", err)
	}
	data, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read migration source: %w", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return false, fmt.Errorf("stat migration source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("migration source must be a regular file")
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := WriteFileAtomic(target, data, mode); err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	journal, err := s.readMigrationJournal()
	if err != nil {
		return false, err
	}
	journal.SchemaVersion = SchemaVersion
	journal.Items = append(journal.Items, MigrationEntry{
		Source: filepath.Clean(source), Destination: filepath.ToSlash(destination),
		SHA256: hex.EncodeToString(sum[:]), MigratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := writeJSONAtomic(filepath.Join(s.root, "migrations.json"), journal, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) CreateBackup() (string, error) {
	if err := s.Ensure(); err != nil {
		return "", err
	}
	name := "goflow-data-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".zip"
	path := filepath.Join(s.root, "backups", name)
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	zw := zip.NewWriter(file)
	walkErr := filepath.WalkDir(s.root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(s.root, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == "backups" || strings.HasPrefix(rel, "backups"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup refuses symbolic link %s", current)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
			_, err = zw.CreateHeader(header)
			return err
		}
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(current)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeZipErr := zw.Close()
	closeFileErr := file.Close()
	if walkErr != nil || closeZipErr != nil || closeFileErr != nil {
		_ = os.Remove(tmp)
		return "", errors.Join(walkErr, closeZipErr, closeFileErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("publish backup: %w", err)
	}
	return path, nil
}

func (s *Store) RestoreBackup(archivePath string) error {
	if err := s.ensureEnabled(); err != nil {
		return err
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	parent := filepath.Dir(s.root)
	tmp, err := os.MkdirTemp(parent, ".goflow-restore-")
	if err != nil {
		_ = reader.Close()
		return fmt.Errorf("create restore directory: %w", err)
	}
	cleanup := true
	defer func() {
		_ = reader.Close()
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	for _, item := range reader.File {
		if err := extractZipItem(tmp, item); err != nil {
			return err
		}
	}
	if err := reader.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		return fmt.Errorf("backup manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse backup manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	old := s.root + ".restore-old-" + fmt.Sprint(time.Now().UnixNano())
	if err := os.Rename(s.root, old); err != nil {
		return fmt.Errorf("stage current data root: %w", err)
	}
	if err := os.Rename(tmp, s.root); err != nil {
		rollbackErr := os.Rename(old, s.root)
		return errors.Join(fmt.Errorf("activate restored data: %w", err), rollbackErr)
	}
	cleanup = false
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("remove replaced data root: %w", err)
	}
	return nil
}

func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("replace target is not a regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect replace target: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".goflow-write-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	}
	old := path + ".replace-old"
	_ = os.Remove(old)
	if err := os.Rename(path, old); err != nil {
		return fmt.Errorf("stage previous file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		rollbackErr := os.Rename(old, path)
		return errors.Join(fmt.Errorf("replace file: %w", err), rollbackErr)
	}
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove previous file: %w", err)
	}
	return nil
}

func (s *Store) readTombstones() (TombstoneIndex, error) {
	var index TombstoneIndex
	err := readJSON(filepath.Join(s.root, "tombstones.json"), &index)
	if errors.Is(err, os.ErrNotExist) {
		return TombstoneIndex{SchemaVersion: SchemaVersion}, nil
	}
	return index, err
}

func (s *Store) readMigrationJournal() (MigrationJournal, error) {
	var journal MigrationJournal
	err := readJSON(filepath.Join(s.root, "migrations.json"), &journal)
	if errors.Is(err, os.ErrNotExist) {
		return MigrationJournal{SchemaVersion: SchemaVersion}, nil
	}
	return journal, err
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, mode)
}

// PathWithinRoot checks the store's existing relative-path boundary and rejects
// symlinks and Windows junctions below the user-configured root. The root and its parents must be
// trusted user configuration; callers must not allow concurrent untrusted edits.
func (s *Store) PathWithinRoot(relative string) (string, error) {
	if err := s.ensureEnabled(); err != nil {
		return "", err
	}
	path, err := s.pathWithinRoot(relative)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", err
	}
	current := s.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		// Windows directory junctions need not carry ModeSymlink, but Readlink
		// identifies their redirection just as it does ordinary symbolic links.
		_, linkErr := os.Readlink(current)
		if info.Mode()&os.ModeSymlink != 0 || linkErr == nil {
			return "", fmt.Errorf("data path cannot traverse a symbolic link or junction")
		}
	}
	return path, nil
}

func (s *Store) pathWithinRoot(relative string) (string, error) {
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", fmt.Errorf("destination must be relative to the data root")
	}
	path := filepath.Clean(filepath.Join(s.root, relative))
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("destination escapes the data root")
	}
	return path, nil
}

func (s *Store) ensureEnabled() error {
	if s == nil || strings.TrimSpace(s.root) == "" || s.root == "." {
		return fmt.Errorf("data root is not configured")
	}
	return nil
}

func cleanSegment(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return "", fmt.Errorf("invalid name %q", value)
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return "", fmt.Errorf("invalid name %q", value)
	}
	return value, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported data schema version %d", manifest.SchemaVersion)
	}
	return nil
}

func extractZipItem(root string, item *zip.File) error {
	name := filepath.FromSlash(item.Name)
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return fmt.Errorf("backup entry %q is absolute", item.Name)
	}
	target := filepath.Clean(filepath.Join(root, name))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup entry %q escapes restore directory", item.Name)
	}
	if item.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup entry %q is a symbolic link", item.Name)
	}
	if item.FileInfo().IsDir() {
		return os.MkdirAll(target, item.Mode().Perm())
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	source, err := item.Open()
	if err != nil {
		return err
	}
	destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, item.Mode().Perm())
	if err != nil {
		_ = source.Close()
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeDestinationErr := destination.Close()
	closeSourceErr := source.Close()
	return errors.Join(copyErr, closeDestinationErr, closeSourceErr)
}
