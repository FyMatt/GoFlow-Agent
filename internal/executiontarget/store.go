package executiontarget

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

const (
	LocalID       = "local"
	KindLocal     = "local"
	KindSSH       = "ssh"
	SchemaVersion = 1
)

var (
	targetIDPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	sshUserPattern       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	hostnamePattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	hostFingerprint      = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
	environmentReference = regexp.MustCompile(`^env:(?://)?[A-Za-z_][A-Za-z0-9_]*$`)
)

type Target struct {
	SchemaVersion   int      `json:"schema_version"`
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	DisplayName     string   `json:"display_name"`
	Host            string   `json:"host,omitempty"`
	Port            int      `json:"port,omitempty"`
	User            string   `json:"user,omitempty"`
	CredentialRef   string   `json:"credential_ref,omitempty"`
	HostKeySHA256   string   `json:"host_key_sha256,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	Enabled         bool     `json:"enabled"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

type Store struct {
	root string
	mu   sync.Mutex
}

func NewStore(dataRoot string) *Store {
	return &Store{root: filepath.Join(filepath.Clean(strings.TrimSpace(dataRoot)), "resources", "targets")}
}

func Local() Target {
	return Target{
		SchemaVersion: SchemaVersion,
		ID:            LocalID,
		Kind:          KindLocal,
		DisplayName:   "本机",
		Enabled:       true,
	}
}

func (s *Store) List() ([]Target, error) {
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list execution targets: %w", err)
	}
	items := []Target{Local()}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		item, err := s.readPath(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items[1:], func(i, j int) bool {
		left, right := items[i+1], items[j+1]
		return strings.ToLower(left.DisplayName) < strings.ToLower(right.DisplayName)
	})
	return items, nil
}

func (s *Store) Get(id string) (Target, bool, error) {
	id = normalizeID(id)
	if id == LocalID {
		return Local(), true, nil
	}
	if !targetIDPattern.MatchString(id) {
		return Target{}, false, nil
	}
	item, err := s.readPath(filepath.Join(s.root, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Target{}, false, nil
	}
	if err != nil {
		return Target{}, false, err
	}
	return item, true, nil
}

func (s *Store) Put(item Target) (Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item = normalize(item)
	if err := Validate(item); err != nil {
		return Target{}, err
	}
	if item.ID == LocalID {
		return Target{}, fmt.Errorf("the built-in local target is read-only")
	}
	if err := s.ensureRoot(); err != nil {
		return Target{}, err
	}
	path := filepath.Join(s.root, item.ID+".json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if existing, err := s.readPath(path); err == nil {
		item.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, os.ErrNotExist) {
		return Target{}, err
	}
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return Target{}, fmt.Errorf("encode execution target: %w", err)
	}
	data = append(data, '\n')
	if err := persistence.WriteFileAtomic(path, data, 0o600); err != nil {
		return Target{}, fmt.Errorf("write execution target: %w", err)
	}
	return item, nil
}

func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = normalizeID(id)
	if id == LocalID {
		return false, fmt.Errorf("the built-in local target cannot be deleted")
	}
	if !targetIDPattern.MatchString(id) {
		return false, nil
	}
	path := filepath.Join(s.root, id+".json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect execution target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("execution target is not a regular file")
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("delete execution target: %w", err)
	}
	return true, nil
}

func Validate(item Target) error {
	if item.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported execution target schema version %d", item.SchemaVersion)
	}
	if !targetIDPattern.MatchString(item.ID) {
		return fmt.Errorf("target id must be a lowercase slug of at most 64 characters")
	}
	if item.ID == LocalID || item.Kind != KindSSH {
		return fmt.Errorf("custom targets must use kind ssh")
	}
	if item.DisplayName == "" || len([]rune(item.DisplayName)) > 100 {
		return fmt.Errorf("display_name is required and must not exceed 100 characters")
	}
	if err := ValidateSSHAddress(item.Host, item.Port); err != nil {
		return err
	}
	if !sshUserPattern.MatchString(item.User) {
		return fmt.Errorf("user contains unsupported characters")
	}
	if !validCredentialReference(item.CredentialRef) {
		return fmt.Errorf("credential_ref must use secret:// or env: without containing a secret value")
	}
	if !hostFingerprint.MatchString(item.HostKeySHA256) {
		return fmt.Errorf("host_key_sha256 must be an OpenSSH SHA256 fingerprint")
	}
	if len(item.AllowedCommands) == 0 || len(item.AllowedCommands) > 100 {
		return fmt.Errorf("allowed_commands must contain between 1 and 100 exact commands")
	}
	for _, command := range item.AllowedCommands {
		if err := validateAllowedCommand(command); err != nil {
			return err
		}
	}
	return nil
}

func ValidateSSHAddress(host string, port int) error {
	if !validHost(host) {
		return fmt.Errorf("host must be an IP address or DNS name without a scheme or path")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func normalize(item Target) Target {
	item.SchemaVersion = SchemaVersion
	item.ID = normalizeID(item.ID)
	item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.Host = strings.Trim(strings.ToLower(strings.TrimSpace(item.Host)), "[]")
	if item.Port == 0 {
		item.Port = 22
	}
	item.User = strings.TrimSpace(item.User)
	item.CredentialRef = strings.TrimSpace(item.CredentialRef)
	item.HostKeySHA256 = strings.TrimSpace(item.HostKeySHA256)
	commands := make([]string, 0, len(item.AllowedCommands))
	seen := make(map[string]struct{}, len(item.AllowedCommands))
	for _, command := range item.AllowedCommands {
		command = strings.Join(strings.Fields(command), " ")
		if command == "" {
			continue
		}
		key := strings.ToLower(command)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		commands = append(commands, command)
	}
	sort.Strings(commands)
	item.AllowedCommands = commands
	return item
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, `/\\?#@ \t\r\n`) {
		return false
	}
	if net.ParseIP(strings.Trim(value, "[]")) != nil {
		return true
	}
	return hostnamePattern.MatchString(value) && !strings.Contains(value, "..")
}

func validCredentialReference(value string) bool {
	value = strings.TrimSpace(value)
	if environmentReference.MatchString(value) {
		return true
	}
	if !strings.HasPrefix(value, "secret://") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "secret://"), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, char := range part {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.", char) {
				continue
			}
			return false
		}
	}
	return true
}

func validateAllowedCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 500 {
		return fmt.Errorf("allowed command is empty or too long")
	}
	if strings.ContainsAny(command, "\r\n;&|><`$") {
		return fmt.Errorf("allowed command %q contains shell control characters", command)
	}
	return nil
}

func (s *Store) ensureRoot() error {
	if s == nil || strings.TrimSpace(s.root) == "" || s.root == "." {
		return fmt.Errorf("execution target store is not configured")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create execution target store: %w", err)
	}
	return nil
}

func (s *Store) readPath(path string) (Target, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Target{}, err
	}
	if !info.Mode().IsRegular() {
		return Target{}, fmt.Errorf("execution target is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Target{}, fmt.Errorf("read execution target: %w", err)
	}
	var item Target
	if err := json.Unmarshal(data, &item); err != nil {
		return Target{}, fmt.Errorf("parse execution target %s: %w", filepath.Base(path), err)
	}
	item = normalize(item)
	if err := Validate(item); err != nil {
		return Target{}, fmt.Errorf("validate execution target %s: %w", item.ID, err)
	}
	return item, nil
}
