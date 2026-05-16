package session

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	artifactStoreObjectsRelativePath = ".goflow/artifacts/objects"
	artifactStoreIndexRelativePath   = ".goflow/artifacts/index.json"
)

// ArtifactObjectStore persists large runtime artifacts by content hash.
type ArtifactObjectStore struct {
	root string
	mu   sync.Mutex
}

// ArtifactObject describes one content-addressed artifact.
type ArtifactObject struct {
	Ref         string            `json:"ref"`
	Hash        string            `json:"hash"`
	CreatedAt   string            `json:"created_at,omitempty"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	Mime        string            `json:"mime,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Content     string            `json:"content,omitempty"`
	Size        int               `json:"size"`
	StoredBytes int64             `json:"stored_bytes,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Title       string            `json:"title,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ArtifactObjectIndex is the summary-first index stored beside object files.
type ArtifactObjectIndex struct {
	UpdatedAt string                   `json:"updated_at,omitempty"`
	Objects   []ArtifactObjectMetadata `json:"objects,omitempty"`
}

// ArtifactObjectMetadata is an index entry without full content.
type ArtifactObjectMetadata struct {
	Ref         string            `json:"ref"`
	Hash        string            `json:"hash"`
	CreatedAt   string            `json:"created_at,omitempty"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	Mime        string            `json:"mime,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Size        int               `json:"size"`
	StoredBytes int64             `json:"stored_bytes,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Title       string            `json:"title,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NewArtifactObjectStore returns a workspace-scoped artifact object store.
func NewArtifactObjectStore(workspaceRoot string) *ArtifactObjectStore {
	return &ArtifactObjectStore{root: strings.TrimSpace(workspaceRoot)}
}

// Root returns the workspace root backing this object store.
func (s *ArtifactObjectStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Ensure creates artifact object directories.
func (s *ArtifactObjectStore) Ensure() error {
	if err := s.ensureEnabled(); err != nil {
		return err
	}
	return os.MkdirAll(s.objectsDir(), 0o755)
}

// Put stores content by sha256, updates the index, and returns metadata.
func (s *ArtifactObjectStore) Put(object ArtifactObject) (ArtifactObject, bool, error) {
	if err := s.ensureEnabled(); err != nil {
		return ArtifactObject{}, false, err
	}
	if object.Content == "" {
		return ArtifactObject{}, false, fmt.Errorf("artifact object content is required")
	}
	sum := sha256.Sum256([]byte(object.Content))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	object.Hash = hash
	object.Ref = "sha256:" + hash
	if object.CreatedAt == "" {
		object.CreatedAt = now
	}
	object.UpdatedAt = now
	if object.Mime == "" {
		object.Mime = "text/plain"
	}
	object.Size = len([]byte(object.Content))
	object.Metadata = copyArtifactObjectMetadataMap(object.Metadata)
	if strings.TrimSpace(object.Summary) == "" {
		object.Summary = object.Content
	}
	object.Summary = trimSessionArtifactBytes(object.Summary, maxSessionArtifactSummaryBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.objectsDir(), 0o755); err != nil {
		return ArtifactObject{}, false, err
	}
	path := s.objectPath(hash)
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return ArtifactObject{}, false, statErr
	}
	if !existed {
		storedBytes, err := writeArtifactObjectGzip(path, object)
		if err != nil {
			return ArtifactObject{}, false, err
		}
		object.StoredBytes = storedBytes
	} else if info, err := os.Stat(path); err == nil {
		object.StoredBytes = info.Size()
	}
	if err := s.upsertIndexLocked(object); err != nil {
		return ArtifactObject{}, false, err
	}
	return object, existed, nil
}

// Get reads a full artifact object by sha256 hash or sha256:<hash> ref.
func (s *ArtifactObjectStore) Get(hashOrRef string) (ArtifactObject, bool, error) {
	if err := s.ensureEnabled(); err != nil {
		return ArtifactObject{}, false, err
	}
	hash := normalizeArtifactHash(hashOrRef)
	if hash == "" {
		return ArtifactObject{}, false, nil
	}
	path := s.objectPath(hash)
	object, err := readArtifactObjectGzip(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactObject{}, false, nil
		}
		return ArtifactObject{}, false, err
	}
	if object.Hash == "" {
		object.Hash = hash
	}
	if object.Ref == "" {
		object.Ref = "sha256:" + object.Hash
	}
	if object.Size == 0 {
		object.Size = len([]byte(object.Content))
	}
	if info, err := os.Stat(path); err == nil {
		object.StoredBytes = info.Size()
	}
	return object, true, nil
}

// Index reads the artifact object metadata index.
func (s *ArtifactObjectStore) Index() (ArtifactObjectIndex, error) {
	if err := s.ensureEnabled(); err != nil {
		return ArtifactObjectIndex{}, err
	}
	var index ArtifactObjectIndex
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactObjectIndex{}, nil
		}
		return ArtifactObjectIndex{}, err
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return ArtifactObjectIndex{}, err
	}
	return index, nil
}

func (s *ArtifactObjectStore) ensureEnabled() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("artifact object store is not configured")
	}
	return nil
}

func (s *ArtifactObjectStore) objectsDir() string {
	return filepath.Join(s.root, artifactStoreObjectsRelativePath)
}

func (s *ArtifactObjectStore) indexPath() string {
	return filepath.Join(s.root, artifactStoreIndexRelativePath)
}

func (s *ArtifactObjectStore) objectPath(hash string) string {
	normalized := normalizeArtifactHash(hash)
	if normalized == "" {
		normalized = "invalid"
	}
	return filepath.Join(s.objectsDir(), normalized+".json.gz")
}

func (s *ArtifactObjectStore) upsertIndexLocked(object ArtifactObject) error {
	index := ArtifactObjectIndex{}
	if data, err := os.ReadFile(s.indexPath()); err == nil {
		_ = json.Unmarshal(data, &index)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	meta := artifactObjectMetadata(object)
	replaced := false
	for i := range index.Objects {
		if index.Objects[i].Hash == meta.Hash {
			if index.Objects[i].CreatedAt != "" && meta.CreatedAt == "" {
				meta.CreatedAt = index.Objects[i].CreatedAt
			}
			index.Objects[i] = meta
			replaced = true
			break
		}
	}
	if !replaced {
		index.Objects = append(index.Objects, meta)
	}
	sort.Slice(index.Objects, func(i, j int) bool {
		return index.Objects[i].UpdatedAt > index.Objects[j].UpdatedAt
	})
	index.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.MkdirAll(filepath.Dir(s.indexPath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.indexPath(), data, 0o644)
}

func artifactObjectMetadata(object ArtifactObject) ArtifactObjectMetadata {
	return ArtifactObjectMetadata{
		Ref:         object.Ref,
		Hash:        object.Hash,
		CreatedAt:   object.CreatedAt,
		UpdatedAt:   object.UpdatedAt,
		Mime:        object.Mime,
		Summary:     trimSessionArtifactBytes(object.Summary, maxSessionArtifactSummaryBytes),
		Size:        object.Size,
		StoredBytes: object.StoredBytes,
		Kind:        object.Kind,
		Title:       object.Title,
		Metadata:    copyArtifactObjectMetadataMap(object.Metadata),
	}
}

func writeArtifactObjectGzip(path string, object ArtifactObject) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	encoder := json.NewEncoder(gz)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(object); err != nil {
		_ = gz.Close()
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func readArtifactObjectGzip(path string) (ArtifactObject, error) {
	file, err := os.Open(path)
	if err != nil {
		return ArtifactObject{}, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return ArtifactObject{}, err
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return ArtifactObject{}, err
	}
	var object ArtifactObject
	if err := json.Unmarshal(data, &object); err != nil {
		return ArtifactObject{}, err
	}
	return object, nil
}

func normalizeArtifactHash(hashOrRef string) string {
	value := strings.TrimSpace(hashOrRef)
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.TrimSuffix(value, ".json.gz")
	if len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return ""
		}
	}
	return strings.ToLower(value)
}

func copyArtifactObjectMetadataMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}
