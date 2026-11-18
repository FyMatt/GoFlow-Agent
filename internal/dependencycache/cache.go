package dependencycache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultMaxDownloadBytes int64 = 256 << 20

// Artifact identifies one immutable cache object by its content hash.
type Artifact struct {
	SHA256 string
	Size   int64
	Path   string
}

// Store keeps immutable dependency files in a content-addressed directory.
type Store struct {
	root string
}

// Fetch downloads one explicitly requested dependency into the immutable
// content-addressed store. The caller must provide the expected SHA-256; this
// keeps a remote response from becoming an executable dependency by accident.
// HTTPS is required, except for loopback URLs used by local development and
// tests. Redirects are re-validated and limited to three hops.
func (s *Store) Fetch(ctx context.Context, rawURL, expectedSHA256 string, maxBytes int64) (Artifact, error) {
	if s == nil {
		return Artifact{}, fmt.Errorf("dependency cache is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	expected := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if len(expected) != sha256.Size*2 {
		return Artifact{}, fmt.Errorf("dependency SHA-256 is required")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return Artifact{}, fmt.Errorf("dependency SHA-256 is invalid: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}
	parsed, err := validateURL(rawURL)
	if err != nil {
		return Artifact{}, err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("dependency download exceeded redirect limit")
		}
		if _, err := validateURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Artifact{}, fmt.Errorf("build dependency request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return Artifact{}, fmt.Errorf("download dependency: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Artifact{}, fmt.Errorf("dependency download returned %s", response.Status)
	}
	if response.ContentLength > maxBytes {
		return Artifact{}, fmt.Errorf("dependency download exceeds %d bytes", maxBytes)
	}
	temp, err := os.CreateTemp(s.root, ".dependency-download-*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("create dependency download temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, io.LimitReader(response.Body, maxBytes+1)); err != nil {
		_ = temp.Close()
		return Artifact{}, fmt.Errorf("read dependency download: %w", err)
	}
	if info, err := temp.Stat(); err != nil {
		_ = temp.Close()
		return Artifact{}, fmt.Errorf("inspect dependency download: %w", err)
	} else if info.Size() > maxBytes {
		_ = temp.Close()
		return Artifact{}, fmt.Errorf("dependency download exceeds %d bytes", maxBytes)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return Artifact{}, fmt.Errorf("sync dependency download: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close dependency download: %w", err)
	}
	artifact, err := s.PutFile(tempPath)
	if err != nil {
		return Artifact{}, err
	}
	if artifact.SHA256 != expected {
		return Artifact{}, fmt.Errorf("dependency download SHA-256 mismatch: got %s", artifact.SHA256)
	}
	return artifact, nil
}

func validateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("dependency URL is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if scheme != "https" && !(scheme == "http" && loopback) {
		return nil, fmt.Errorf("dependency URL must use HTTPS (loopback HTTP is allowed for local proxy)")
	}
	return parsed, nil
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("dependency cache root is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve dependency cache root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, "objects"), 0o700); err != nil {
		return nil, fmt.Errorf("create dependency cache root: %w", err)
	}
	return &Store{root: absolute}, nil
}

func (s *Store) PutFile(source string) (Artifact, error) {
	if s == nil {
		return Artifact{}, fmt.Errorf("dependency cache is required")
	}
	source, err := filepath.Abs(filepath.Clean(strings.TrimSpace(source)))
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve dependency source: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return Artifact{}, fmt.Errorf("open dependency source: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return Artifact{}, fmt.Errorf("inspect dependency source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("dependency source is not a regular file")
	}
	temp, err := os.CreateTemp(s.root, ".dependency-*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("create dependency cache temporary file: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temp, hash), input)
	if err != nil {
		return Artifact{}, fmt.Errorf("cache dependency: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return Artifact{}, fmt.Errorf("sync dependency cache object: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close dependency cache object: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	target := filepath.Join(s.root, "objects", digest[:2], digest)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create dependency cache shard: %w", err)
	}
	if existing, err := os.Stat(target); err == nil {
		if !existing.Mode().IsRegular() || existing.Size() != size {
			return Artifact{}, fmt.Errorf("dependency cache object conflicts with %s", digest)
		}
		return Artifact{SHA256: digest, Size: size, Path: target}, nil
	} else if !os.IsNotExist(err) {
		return Artifact{}, fmt.Errorf("inspect dependency cache object: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		if existing, statErr := os.Stat(target); statErr != nil || existing.Size() != size {
			return Artifact{}, fmt.Errorf("publish dependency cache object: %w", err)
		}
	} else {
		removeTemp = false
	}
	_ = os.Chmod(target, 0o400)
	return Artifact{SHA256: digest, Size: size, Path: target}, nil
}

func (s *Store) Materialize(artifact Artifact, destinationDir, name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		return "", fmt.Errorf("dependency destination name is required")
	}
	return s.materializeRelative(artifact, destinationDir, name, 0o400)
}

// MaterializeExecutable creates an immutable executable view of one cache
// object. The cache object itself remains non-executable and read-only.
func (s *Store) MaterializeExecutable(artifact Artifact, destinationDir, name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		return "", fmt.Errorf("dependency destination name is required")
	}
	return s.materializeRelative(artifact, destinationDir, name, 0o500)
}

// MaterializeRelative preserves a checked relative path for script bundles so
// local imports keep working inside the task dependency directory.
func (s *Store) MaterializeRelative(artifact Artifact, destinationDir, relative string) (string, error) {
	return s.materializeRelative(artifact, destinationDir, relative, 0o400)
}

func (s *Store) materializeRelative(artifact Artifact, destinationDir, relative string, mode os.FileMode) (string, error) {
	if s == nil || len(artifact.SHA256) != 64 {
		return "", fmt.Errorf("valid dependency artifact is required")
	}
	relative = filepath.Clean(strings.TrimSpace(relative))
	if relative == "" || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dependency destination path must stay relative")
	}
	destinationDir, err := filepath.Abs(filepath.Clean(strings.TrimSpace(destinationDir)))
	if err != nil {
		return "", fmt.Errorf("resolve dependency destination: %w", err)
	}
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return "", fmt.Errorf("create dependency destination: %w", err)
	}
	source := filepath.Join(s.root, "objects", artifact.SHA256[:2], artifact.SHA256)
	verified, err := verifyFile(source, artifact.SHA256)
	if err != nil {
		return "", err
	}
	if verified.Size != artifact.Size {
		return "", fmt.Errorf("dependency cache object size changed")
	}
	target := filepath.Join(destinationDir, relative)
	if rel, relErr := filepath.Rel(destinationDir, target); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dependency destination path escapes its root")
	}
	if info, err := os.Stat(target); err == nil {
		if !info.Mode().IsRegular() || info.Size() != artifact.Size {
			return "", fmt.Errorf("materialized dependency conflicts with existing path")
		}
		verifiedTarget, verifyErr := verifyFile(target, artifact.SHA256)
		if verifyErr != nil || verifiedTarget.Size != artifact.Size {
			return "", fmt.Errorf("materialized dependency integrity check failed")
		}
		_ = os.Chmod(target, mode)
		return target, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect materialized dependency: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create materialized dependency directory: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return "", fmt.Errorf("create materialized dependency: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(target)
		return "", fmt.Errorf("materialize dependency: %w", err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("close materialized dependency: %w", err)
	}
	_ = os.Chmod(target, mode)
	return target, nil
}

func verifyFile(path, expected string) (Artifact, error) {
	input, err := os.Open(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("open dependency cache object: %w", err)
	}
	defer input.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, input)
	if err != nil {
		return Artifact{}, fmt.Errorf("verify dependency cache object: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != strings.ToLower(strings.TrimSpace(expected)) {
		return Artifact{}, fmt.Errorf("dependency cache object hash mismatch")
	}
	return Artifact{SHA256: digest, Size: size, Path: path}, nil
}
