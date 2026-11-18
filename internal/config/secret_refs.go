package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
	"gopkg.in/yaml.v3"
)

const secretReferencePrefix = "secret://"

// SecretReferencePath resolves one bounded secret:// reference under DataRoot.
func SecretReferencePath(dataRoot, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if !strings.HasPrefix(reference, secretReferencePrefix) {
		return "", fmt.Errorf("value is not a secret reference")
	}
	parts := strings.Split(strings.TrimPrefix(reference, secretReferencePrefix), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("secret reference must include a family and name")
	}
	for _, part := range parts {
		if !validSecretSegment(part) {
			return "", fmt.Errorf("invalid secret reference segment %q", part)
		}
	}
	root := filepath.Join(filepath.Clean(dataRoot), "secrets")
	path := filepath.Clean(filepath.Join(append([]string{root}, parts...)...))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("secret reference escapes data root")
	}
	return path, nil
}

// MigratePlaintextProviderSecrets replaces file-backed API keys with secret:// references.
func MigratePlaintextProviderSecrets(runtimeHome, dataRoot string) ([]string, error) {
	dir := filepath.Join(runtimeHome, "configs", "providers")
	if !sameCleanPath(runtimeHome, dataRoot) {
		dir = filepath.Join(dataRoot, "resources", "providers")
	}
	files, err := modularConfigFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("list provider modules for secret migration: %w", err)
	}
	migrated := make([]string, 0)
	for _, file := range files {
		providers, err := readProviderModule(file)
		if err != nil {
			return nil, err
		}
		changed := false
		for name, provider := range providers {
			value := strings.TrimSpace(provider.APIKey)
			if value == "" || strings.HasPrefix(value, "${") || strings.HasPrefix(value, secretReferencePrefix) {
				continue
			}
			reference := secretReferencePrefix + "providers/" + strings.ToLower(strings.TrimSpace(name)) + "/api_key"
			secretPath, err := SecretReferencePath(dataRoot, reference)
			if err != nil {
				return nil, err
			}
			if err := persistence.WriteFileAtomic(secretPath, []byte(value+"\n"), 0o600); err != nil {
				return nil, fmt.Errorf("migrate provider %s secret: %w", name, err)
			}
			provider.APIKey = reference
			providers[name] = provider
			changed = true
		}
		if !changed {
			continue
		}
		data, err := yaml.Marshal(providers)
		if err != nil {
			return nil, fmt.Errorf("render migrated provider module: %w", err)
		}
		if err := persistence.WriteFileAtomic(file, data, 0o600); err != nil {
			return nil, fmt.Errorf("write migrated provider module: %w", err)
		}
		migrated = append(migrated, file)
	}
	return migrated, nil
}

// ResolveSecretReference reads a secret reference or returns a plain value unchanged.
func ResolveSecretReference(dataRoot, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, secretReferencePrefix) {
		return value, nil
	}
	path, err := SecretReferencePath(dataRoot, value)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret reference: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func validSecretSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
