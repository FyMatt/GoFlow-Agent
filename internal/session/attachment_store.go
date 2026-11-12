package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	attachmentObjectsRelativePath = ".goflow/attachments/objects"
	attachmentIndexRelativePath   = ".goflow/attachments/index.json"
)

// AttachmentObjectStore persists uploaded bytes outside session JSON. Objects
// are immutable and addressed by SHA-256 so retries and restarts reuse them.
type AttachmentObjectStore struct {
	root string
	mu   sync.Mutex
}

type AttachmentObjectIndex struct {
	UpdatedAt   string                 `json:"updated_at,omitempty"`
	Attachments []schema.AttachmentRef `json:"attachments,omitempty"`
}

func NewAttachmentObjectStore(root string) *AttachmentObjectStore {
	return &AttachmentObjectStore{root: strings.TrimSpace(root)}
}

func (s *AttachmentObjectStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *AttachmentObjectStore) Ensure() error {
	if err := s.ensureEnabled(); err != nil {
		return err
	}
	return os.MkdirAll(s.objectsDir(), 0o755)
}

func (s *AttachmentObjectStore) Put(data []byte, attachment schema.AttachmentRef) (schema.AttachmentRef, bool, error) {
	if err := s.ensureEnabled(); err != nil {
		return schema.AttachmentRef{}, false, err
	}
	if len(data) == 0 {
		return schema.AttachmentRef{}, false, fmt.Errorf("attachment content is required")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attachment.Hash = hash
	attachment.Ref = "sha256:" + hash
	attachment.Size = int64(len(data))
	attachment.Name = sanitizeAttachmentName(attachment.Name)
	if attachment.Name == "" {
		attachment.Name = hash
	}
	if attachment.CreatedAt == "" {
		attachment.CreatedAt = now
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.objectsDir(), 0o755); err != nil {
		return schema.AttachmentRef{}, false, err
	}
	path := s.objectPath(hash)
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return schema.AttachmentRef{}, false, statErr
	}
	if !existed {
		if err := persistence.WriteFileAtomic(path, data, 0o600); err != nil {
			return schema.AttachmentRef{}, false, err
		}
	}
	if err := s.upsertIndexLocked(attachment, now); err != nil {
		return schema.AttachmentRef{}, false, err
	}
	return attachment, existed, nil
}

func (s *AttachmentObjectStore) Get(hashOrRef string) (schema.AttachmentRef, []byte, bool, error) {
	if err := s.ensureEnabled(); err != nil {
		return schema.AttachmentRef{}, nil, false, err
	}
	hash := normalizeAttachmentHash(hashOrRef)
	if hash == "" {
		return schema.AttachmentRef{}, nil, false, nil
	}
	data, err := os.ReadFile(s.objectPath(hash))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return schema.AttachmentRef{}, nil, false, nil
		}
		return schema.AttachmentRef{}, nil, false, err
	}
	meta, ok, err := s.Metadata(hash)
	if err != nil {
		return schema.AttachmentRef{}, nil, false, err
	}
	if !ok {
		meta = schema.AttachmentRef{Ref: "sha256:" + hash, Hash: hash, Name: hash, Size: int64(len(data))}
	}
	return meta, data, true, nil
}

func (s *AttachmentObjectStore) Metadata(hashOrRef string) (schema.AttachmentRef, bool, error) {
	hash := normalizeAttachmentHash(hashOrRef)
	if hash == "" {
		return schema.AttachmentRef{}, false, nil
	}
	index, err := s.Index()
	if err != nil {
		return schema.AttachmentRef{}, false, err
	}
	for _, attachment := range index.Attachments {
		if strings.EqualFold(attachment.Hash, hash) {
			return attachment, true, nil
		}
	}
	return schema.AttachmentRef{}, false, nil
}

func (s *AttachmentObjectStore) Index() (AttachmentObjectIndex, error) {
	if err := s.ensureEnabled(); err != nil {
		return AttachmentObjectIndex{}, err
	}
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AttachmentObjectIndex{}, nil
		}
		return AttachmentObjectIndex{}, err
	}
	var index AttachmentObjectIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return AttachmentObjectIndex{}, err
	}
	return index, nil
}

func (s *AttachmentObjectStore) upsertIndexLocked(attachment schema.AttachmentRef, now string) error {
	index := AttachmentObjectIndex{}
	if data, err := os.ReadFile(s.indexPath()); err == nil {
		_ = json.Unmarshal(data, &index)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	replaced := false
	for i := range index.Attachments {
		if strings.EqualFold(index.Attachments[i].Hash, attachment.Hash) {
			if index.Attachments[i].CreatedAt != "" {
				attachment.CreatedAt = index.Attachments[i].CreatedAt
			}
			index.Attachments[i] = attachment
			replaced = true
			break
		}
	}
	if !replaced {
		index.Attachments = append(index.Attachments, attachment)
	}
	sort.Slice(index.Attachments, func(i, j int) bool {
		return index.Attachments[i].CreatedAt > index.Attachments[j].CreatedAt
	})
	index.UpdatedAt = now
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return persistence.WriteFileAtomic(s.indexPath(), data, 0o600)
}

func (s *AttachmentObjectStore) ensureEnabled() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("attachment object store is not configured")
	}
	return nil
}

func (s *AttachmentObjectStore) objectsDir() string {
	return filepath.Join(s.root, attachmentObjectsRelativePath)
}

func (s *AttachmentObjectStore) indexPath() string {
	return filepath.Join(s.root, attachmentIndexRelativePath)
}

func (s *AttachmentObjectStore) objectPath(hash string) string {
	return filepath.Join(s.objectsDir(), normalizeAttachmentHash(hash)+".blob")
}

func normalizeAttachmentHash(hashOrRef string) string {
	value := strings.TrimSpace(hashOrRef)
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.TrimSuffix(value, ".blob")
	if len(value) != 64 {
		return ""
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return ""
		}
	}
	return strings.ToLower(value)
}

func sanitizeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, name)
	if len(name) > 180 {
		name = name[:180]
	}
	return strings.TrimSpace(name)
}
