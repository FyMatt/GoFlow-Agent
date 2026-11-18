package api

import (
	"fmt"
	"os"
	"path/filepath"
)

// Read low to high priority; API saves continue to use kitResourceRoot.
func (s *Server) kitResourceReadRoots() ([]string, error) {
	writeRoot, err := s.kitResourceRoot()
	if err != nil {
		return nil, err
	}
	runtimeRoot, err := filepath.Abs(filepath.Join(s.runtime.RuntimeHome(), "kits"))
	if err != nil {
		return nil, err
	}
	roots := []string{runtimeRoot}
	if !sameAPIPath(s.runtime.DataRoot(), s.runtime.RuntimeHome()) {
		resourceRoot, err := s.runtimeResourceRoot("resources", "kits")
		if err != nil {
			return nil, err
		}
		if !sameAPIPath(resourceRoot, writeRoot) {
			roots = append(roots, resourceRoot)
		}
	}
	if !sameAPIPath(runtimeRoot, writeRoot) {
		roots = append(roots, writeRoot)
	}
	return roots, nil
}

func kitResourcePathsAt(root, name string) []string {
	return []string{filepath.Join(root, name, "kit.yaml"), filepath.Join(root, name, "kit.yml"), filepath.Join(root, name+".yaml"), filepath.Join(root, name+".yml")}
}

func (s *Server) kitResourceReadPath(name string) (string, error) {
	if !resourceNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid Kit name %q", name)
	}
	roots, err := s.kitResourceReadRoots()
	if err != nil {
		return "", err
	}
	hidden, err := s.runtime.IsResourceTombstoned("kits", name)
	if err != nil {
		return "", err
	}
	if hidden {
		return "", os.ErrNotExist
	}
	for i := len(roots) - 1; i >= 0; i-- {
		for _, path := range kitResourcePathsAt(roots[i], name) {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return "", err
			}
			// A malformed or escaping override must not reveal a lower-priority copy.
			root, err := filepath.EvalSymlinks(roots[i])
			if err != nil {
				return "", err
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			if !referenceWithinBase(root, resolved) {
				return "", fmt.Errorf("Kit path escapes resource directory")
			}
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

// Pin a portable origin label, never an absolute installation/data path.
func (s *Server) kitResourceOrigin(path string) string {
	roots, err := s.kitResourceReadRoots()
	if err != nil {
		return "unknown"
	}
	writeRoot, err := s.kitResourceRoot()
	if err != nil {
		return "unknown"
	}
	for i := len(roots) - 1; i >= 0; i-- {
		if !referenceWithinBase(roots[i], path) {
			continue
		}
		if i == 0 {
			return "runtime"
		}
		if sameAPIPath(roots[i], writeRoot) {
			return "data"
		}
		return "data_resources"
	}
	return "unknown"
}
