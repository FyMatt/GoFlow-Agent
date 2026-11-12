//go:build windows

package nativesandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// WindowsTaskBoundarySpec contains only paths created for one task. The real
// workspace must not be passed as WritableRoot.
type WindowsTaskBoundarySpec struct {
	TaskID        string
	ReadOnlyRoots []string
	WritableRoot  string
}

// WindowsTaskBoundary owns the AppContainer identity and every temporary ACL
// grant for one task.
type WindowsTaskBoundary struct {
	Profile *WindowsAppContainerProfile

	aclLeases []*WindowsACLLease
	writable  string
	readOnly  []string
	mu        sync.Mutex
	active    int
	closed    bool
}

// PrepareWindowsTaskBoundary provisions a task identity and grants it only the
// declared read-only inputs and writable staging root.
func PrepareWindowsTaskBoundary(spec WindowsTaskBoundarySpec) (*WindowsTaskBoundary, error) {
	normalized, err := normalizeWindowsTaskBoundarySpec(spec)
	if err != nil {
		return nil, err
	}
	profile, err := EnsureWindowsAppContainerProfile(normalized.TaskID)
	if err != nil {
		return nil, err
	}
	boundary, err := prepareWindowsTaskACLBoundary(profile.SID, normalized)
	if err != nil {
		if deleteErr := profile.Delete(); deleteErr != nil {
			return nil, errors.Join(err, deleteErr)
		}
		return nil, err
	}
	boundary.Profile = profile
	boundary.writable = normalized.WritableRoot
	boundary.readOnly = append([]string(nil), normalized.ReadOnlyRoots...)
	return boundary, nil
}

func prepareWindowsTaskACLBoundary(sid *windows.SID, spec WindowsTaskBoundarySpec) (*WindowsTaskBoundary, error) {
	var leases []*WindowsACLLease
	rollback := func(cause error) error {
		errs := []error{cause}
		for index := len(leases) - 1; index >= 0; index-- {
			if err := leases[index].Close(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	for _, root := range spec.ReadOnlyRoots {
		lease, err := GrantWindowsDirectoryAccess(root, sid, WindowsDirectoryReadOnly)
		if err != nil {
			return nil, rollback(fmt.Errorf("grant read-only task input %s: %w", root, err))
		}
		leases = append(leases, lease)
	}
	lease, err := GrantWindowsDirectoryAccess(spec.WritableRoot, sid, WindowsDirectoryReadWrite)
	if err != nil {
		return nil, rollback(fmt.Errorf("grant writable task staging root %s: %w", spec.WritableRoot, err))
	}
	leases = append(leases, lease)
	return &WindowsTaskBoundary{aclLeases: leases}, nil
}

func normalizeWindowsTaskBoundarySpec(spec WindowsTaskBoundarySpec) (WindowsTaskBoundarySpec, error) {
	if _, err := windowsAppContainerProfileName(spec.TaskID); err != nil {
		return WindowsTaskBoundarySpec{}, err
	}
	writable, err := existingAbsoluteDirectory(spec.WritableRoot, "writable task staging root")
	if err != nil {
		return WindowsTaskBoundarySpec{}, err
	}
	seen := map[string]struct{}{strings.ToLower(writable): {}}
	readOnly := make([]string, 0, len(spec.ReadOnlyRoots))
	for _, root := range spec.ReadOnlyRoots {
		root, err = existingAbsoluteDirectory(root, "read-only task input")
		if err != nil {
			return WindowsTaskBoundarySpec{}, err
		}
		key := strings.ToLower(root)
		if _, ok := seen[key]; ok {
			return WindowsTaskBoundarySpec{}, fmt.Errorf("task sandbox paths must not overlap exactly: %s", root)
		}
		if windowsPathsOverlap(root, writable) {
			return WindowsTaskBoundarySpec{}, fmt.Errorf("read-only task input must not contain or be contained by the writable staging root: %s", root)
		}
		seen[key] = struct{}{}
		readOnly = append(readOnly, root)
	}
	return WindowsTaskBoundarySpec{
		TaskID:        strings.TrimSpace(spec.TaskID),
		ReadOnlyRoots: readOnly,
		WritableRoot:  writable,
	}, nil
}

func existingAbsoluteDirectory(path, label string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory: %s", label, path)
	}
	return path, nil
}

func windowsPathsOverlap(first, second string) bool {
	return windowsPathContains(first, second) || windowsPathContains(second, first)
}

func windowsPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// Close restores ACLs before deleting the task AppContainer profile.
func (boundary *WindowsTaskBoundary) Close() error {
	if boundary == nil {
		return nil
	}
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	if boundary.closed {
		return nil
	}
	if boundary.active > 0 {
		return fmt.Errorf("cannot close Windows task boundary while %d sandbox process(es) are active", boundary.active)
	}
	var errs []error
	for index := len(boundary.aclLeases) - 1; index >= 0; index-- {
		if err := boundary.aclLeases[index].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if boundary.Profile != nil {
		if err := boundary.Profile.Delete(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		boundary.closed = true
	}
	return errors.Join(errs...)
}
