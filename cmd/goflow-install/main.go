package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	manifestName = ".goflow-install.json"
	backupDir    = ".goflow-upgrade-backups"
)

type installManifest struct {
	Product       string `json:"product"`
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	InstalledAt   string `json:"installed_at"`
	DataRoot      string `json:"data_root"`
}

type operationResult struct {
	Operation   string `json:"operation"`
	Prefix      string `json:"prefix"`
	DataRoot    string `json:"data_root,omitempty"`
	Version     string `json:"version,omitempty"`
	Backup      string `json:"backup,omitempty"`
	DataRemoved bool   `json:"data_removed,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "GoFlow installer failed: %v\n", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: goflow-install <install|upgrade|rollback|uninstall> [options]")
	}
	operation := strings.ToLower(strings.TrimSpace(args[0]))
	if operation != "install" && operation != "upgrade" && operation != "rollback" && operation != "uninstall" {
		return fmt.Errorf("unsupported operation %q", operation)
	}
	set := flag.NewFlagSet("goflow-install", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	archivePath := set.String("archive", "", "release archive (.zip or .tar.gz)")
	prefixPath := set.String("prefix", "", "application installation directory")
	dataPath := set.String("data-root", "", "persistent data directory")
	backupPath := set.String("backup", "", "upgrade backup to restore; defaults to the latest backup")
	purgeData := set.Bool("purge-data", false, "remove persistent data during uninstall")
	force := set.Bool("force", false, "replace an existing unmanaged directory")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	prefix, err := absoluteDirectory(*prefixPath, "prefix")
	if err != nil {
		return err
	}
	switch operation {
	case "uninstall":
		result, err := uninstall(prefix, *purgeData)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "rollback":
		result, err := rollback(prefix, *backupPath)
		if err != nil {
			return err
		}
		return printJSON(result)
	default:
		if strings.TrimSpace(*archivePath) == "" {
			return errors.New("--archive is required for install and upgrade")
		}
		archive, err := filepath.Abs(filepath.Clean(*archivePath))
		if err != nil {
			return fmt.Errorf("resolve archive: %w", err)
		}
		dataRoot := *dataPath
		if strings.TrimSpace(dataRoot) == "" {
			dataRoot = filepath.Join(filepath.Dir(prefix), "data")
		}
		dataRoot, err = absoluteDirectory(dataRoot, "data root")
		if err != nil {
			return err
		}
		result, err := install(archive, prefix, dataRoot, *force || operation == "upgrade")
		if err != nil {
			return err
		}
		return printJSON(result)
	}
}

func rollback(prefix, requestedBackup string) (operationResult, error) {
	if _, err := readManifest(prefix); err != nil {
		return operationResult{}, fmt.Errorf("current installation is not managed: %w", err)
	}
	backup, err := resolveRollbackBackup(prefix, requestedBackup)
	if err != nil {
		return operationResult{}, err
	}
	restoredManifest, err := readManifest(backup)
	if err != nil {
		return operationResult{}, fmt.Errorf("read rollback backup manifest: %w", err)
	}
	if !hasGoFlowBinary(backup) {
		return operationResult{}, errors.New("rollback backup does not contain the GoFlow executable")
	}
	backupRoot := filepath.Join(filepath.Dir(prefix), backupDir)
	displaced, err := os.MkdirTemp(backupRoot, ".rollback-current-")
	if err != nil {
		return operationResult{}, fmt.Errorf("reserve rollback location: %w", err)
	}
	if err := os.Remove(displaced); err != nil {
		return operationResult{}, fmt.Errorf("prepare rollback location: %w", err)
	}
	if err := renameWithRetry(prefix, displaced); err != nil {
		return operationResult{}, fmt.Errorf("stage current installation for rollback: %w", err)
	}
	if err := renameWithRetry(backup, prefix); err != nil {
		if restoreErr := renameWithRetry(displaced, prefix); restoreErr != nil {
			return operationResult{}, fmt.Errorf("activate rollback backup: %v; restore current installation: %w", err, restoreErr)
		}
		return operationResult{}, fmt.Errorf("activate rollback backup: %w", err)
	}
	if err := os.RemoveAll(displaced); err != nil {
		return operationResult{}, fmt.Errorf("rollback succeeded but old application cleanup failed: %w", err)
	}
	return operationResult{
		Operation: "rollback",
		Prefix:    prefix,
		DataRoot:  restoredManifest.DataRoot,
		Version:   restoredManifest.Version,
		Backup:    backup,
	}, nil
}

func resolveRollbackBackup(prefix, requested string) (string, error) {
	backupRoot := filepath.Join(filepath.Dir(prefix), backupDir)
	root, err := filepath.Abs(backupRoot)
	if err != nil {
		return "", fmt.Errorf("resolve rollback backup root: %w", err)
	}
	if strings.TrimSpace(requested) != "" {
		candidate, err := filepath.Abs(filepath.Clean(requested))
		if err != nil {
			return "", fmt.Errorf("resolve rollback backup: %w", err)
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil || relative == "." || filepath.Dir(relative) != "." {
			return "", errors.New("rollback backup must be a direct child of the managed backup directory")
		}
		if !strings.HasSuffix(filepath.Base(candidate), "-"+filepath.Base(prefix)) {
			return "", errors.New("rollback backup does not belong to this installation")
		}
		if err := validateRollbackBackup(prefix, candidate); err != nil {
			return "", err
		}
		return candidate, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("no upgrade backup is available for rollback")
		}
		return "", fmt.Errorf("read rollback backups: %w", err)
	}
	prefixSuffix := "-" + filepath.Base(prefix)
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), prefixSuffix) {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if err := validateRollbackBackup(prefix, candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("no valid upgrade backup is available for rollback")
}

func validateRollbackBackup(prefix, backup string) error {
	info, err := os.Lstat(backup)
	if err != nil {
		return fmt.Errorf("inspect rollback backup: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("rollback backup must be a regular directory")
	}
	if filepath.Base(backup) == filepath.Base(prefix) {
		return errors.New("rollback backup cannot be the current installation")
	}
	return nil
}

func printJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func absoluteDirectory(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) || absolute == filepath.VolumeName(absolute) {
		return "", fmt.Errorf("refusing to operate on filesystem root as %s", label)
	}
	return filepath.Clean(absolute), nil
}

func install(archive, prefix, dataRoot string, force bool) (operationResult, error) {
	if _, err := os.Stat(archive); err != nil {
		return operationResult{}, fmt.Errorf("release archive not found: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return operationResult{}, fmt.Errorf("create installation parent: %w", err)
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return operationResult{}, fmt.Errorf("create data root: %w", err)
	}
	existing := false
	if _, err := os.Stat(prefix); err == nil {
		existing = true
		if _, manifestErr := readManifest(prefix); manifestErr != nil && !force {
			return operationResult{}, fmt.Errorf("existing installation is unmanaged; use --force: %w", manifestErr)
		}
	} else if !os.IsNotExist(err) {
		return operationResult{}, fmt.Errorf("inspect installation: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(prefix), ".goflow-stage-")
	if err != nil {
		return operationResult{}, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extractArchive(archive, stage); err != nil {
		return operationResult{}, err
	}
	payload, err := findPayloadRoot(stage)
	if err != nil {
		return operationResult{}, err
	}
	if !hasGoFlowBinary(payload) {
		return operationResult{}, errors.New("release archive does not contain the GoFlow executable")
	}
	var backup string
	if existing {
		backupRoot := filepath.Join(filepath.Dir(prefix), backupDir)
		if err := os.MkdirAll(backupRoot, 0o700); err != nil {
			return operationResult{}, fmt.Errorf("create upgrade backup directory: %w", err)
		}
		backup = filepath.Join(backupRoot, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(prefix)))
		if err := renameWithRetry(prefix, backup); err != nil {
			return operationResult{}, fmt.Errorf("move existing installation to backup: %w", err)
		}
	}
	if err := renameWithRetry(payload, prefix); err != nil {
		if backup != "" {
			_ = renameWithRetry(backup, prefix)
		}
		return operationResult{}, fmt.Errorf("activate staged installation: %w", err)
	}
	archiveHash, err := fileSHA256(archive)
	if err != nil {
		_ = os.RemoveAll(prefix)
		if backup != "" {
			_ = renameWithRetry(backup, prefix)
		}
		return operationResult{}, err
	}
	manifest := installManifest{
		Product:       "GoFlow Local Agent Workflow Platform",
		Version:       inferVersion(archive),
		ArchiveSHA256: archiveHash,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		DataRoot:      dataRoot,
	}
	if err := writeManifest(prefix, manifest); err != nil {
		_ = os.RemoveAll(prefix)
		if backup != "" {
			_ = renameWithRetry(backup, prefix)
		}
		return operationResult{}, err
	}
	operation := "install"
	if existing {
		operation = "upgrade"
	}
	return operationResult{Operation: operation, Prefix: prefix, DataRoot: dataRoot, Version: manifest.Version}, nil
}

func renameWithRetry(oldPath, newPath string) error {
	const (
		maximumWait = 8 * time.Second
		retryDelay  = 100 * time.Millisecond
	)
	deadline := time.Now().Add(maximumWait)
	for {
		err := os.Rename(oldPath, newPath)
		if err == nil || !isRetryableRenameError(err, runtime.GOOS) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(retryDelay)
	}
}

func isRetryableRenameError(err error, goos string) bool {
	if err == nil || goos != "windows" {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.Errno(5) || errno == syscall.Errno(32)
}

func uninstall(prefix string, purge bool) (operationResult, error) {
	manifest, manifestErr := readManifest(prefix)
	if _, err := os.Stat(prefix); err != nil && os.IsNotExist(err) {
		return operationResult{Operation: "uninstall", Prefix: prefix}, nil
	}
	if err := os.RemoveAll(prefix); err != nil {
		return operationResult{}, fmt.Errorf("remove application: %w", err)
	}
	dataRemoved := false
	dataRoot := ""
	if manifestErr == nil {
		dataRoot = manifest.DataRoot
	}
	if purge && dataRoot != "" {
		resolved, err := absoluteDirectory(dataRoot, "manifest data root")
		if err != nil {
			return operationResult{}, err
		}
		if err := os.RemoveAll(resolved); err != nil {
			return operationResult{}, fmt.Errorf("remove data root: %w", err)
		}
		dataRemoved = true
	}
	return operationResult{Operation: "uninstall", Prefix: prefix, DataRoot: dataRoot, DataRemoved: dataRemoved}, nil
}

func readManifest(prefix string) (installManifest, error) {
	data, err := os.ReadFile(filepath.Join(prefix, manifestName))
	if err != nil {
		return installManifest{}, err
	}
	var manifest installManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return installManifest{}, fmt.Errorf("decode installation manifest: %w", err)
	}
	return manifest, nil
}

func writeManifest(prefix string, manifest installManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(prefix, manifestName), data, 0o600); err != nil {
		return fmt.Errorf("write installation manifest: %w", err)
	}
	return nil
}

func inferVersion(archive string) string {
	base := filepath.Base(archive)
	const prefix = "goflow-agent_"
	if !strings.HasPrefix(base, prefix) {
		return "unknown"
	}
	value := strings.TrimPrefix(base, prefix)
	if index := strings.IndexByte(value, '_'); index >= 0 {
		value = value[:index]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func fileSHA256(path string) (string, error) {
	hash := sha256.New()
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open archive for hashing: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash archive: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeArchivePath(root, member string) (string, error) {
	member = strings.TrimSpace(strings.ReplaceAll(member, "\\", "/"))
	windowsDrivePath := len(member) >= 2 && member[1] == ':' && ((member[0] >= 'A' && member[0] <= 'Z') || (member[0] >= 'a' && member[0] <= 'z'))
	if member == "" || strings.HasPrefix(member, "/") || windowsDrivePath || filepath.VolumeName(member) != "" {
		return "", fmt.Errorf("unsafe archive member %q", member)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(member)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive member escapes staging root: %q", member)
	}
	return target, nil
}

func extractArchive(archive, destination string) error {
	lower := strings.ToLower(archive)
	if strings.HasSuffix(lower, ".zip") {
		return extractZip(archive, destination)
	}
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return extractTarGz(archive, destination)
	}
	return errors.New("archive must be .zip, .tar.gz, or .tgz")
}

func extractZip(archive, destination string) error {
	bundle, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer bundle.Close()
	for _, item := range bundle.File {
		target, err := safeArchivePath(destination, item.Name)
		if err != nil {
			return err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err == nil {
			_, err = io.Copy(output, input)
			closeErr := output.Close()
			if err == nil {
				err = closeErr
			}
		}
		_ = input.Close()
		if err != nil {
			return fmt.Errorf("extract %s: %w", item.Name, err)
		}
	}
	return nil
}

func extractTarGz(archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return fmt.Errorf("symlink archive member is not allowed: %s", header.Name)
		}
		target, err := safeArchivePath(destination, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive member type %d: %s", header.Typeflag, header.Name)
		}
	}
}

func findPayloadRoot(staging string) (string, error) {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return "", err
	}
	var directories []string
	for _, entry := range entries {
		if entry.Name() == "__MACOSX" {
			continue
		}
		if !entry.IsDir() {
			return "", errors.New("release archive must contain one top-level directory")
		}
		directories = append(directories, entry.Name())
	}
	if len(directories) != 1 {
		return "", errors.New("release archive must contain one top-level directory")
	}
	return filepath.Join(staging, directories[0]), nil
}

func hasGoFlowBinary(payload string) bool {
	for _, name := range []string{"goflow", "goflow.exe"} {
		if info, err := os.Stat(filepath.Join(payload, "bin", name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}
