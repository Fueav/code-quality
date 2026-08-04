package nativereview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	resolved := filepath.VolumeName(path) + string(filepath.Separator)
	pending := pathComponents(path)
	symlinkTraversals := 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, component)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			for _, unresolved := range pending {
				if unresolved == ".." {
					return "", errors.New("parent traversal follows a missing path component")
				}
			}
			resolved = candidate
			for _, unresolved := range pending {
				if unresolved != "." {
					resolved = filepath.Join(resolved, unresolved)
				}
			}
			pending = nil
			continue
		}
		if err != nil {
			return "", err
		}
		actualComponent, err := canonicalComponentName(resolved, component, info)
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(resolved, actualComponent)
		if info.Mode()&os.ModeSymlink == 0 {
			if len(pending) > 0 && !info.IsDir() {
				return "", errors.New("non-directory path component")
			}
			resolved = candidate
			continue
		}
		symlinkTraversals++
		if symlinkTraversals > 255 {
			return "", errors.New("too many symlink traversals")
		}
		target, err := os.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = filepath.VolumeName(target) + string(filepath.Separator)
		}
		pending = append(pathComponents(target), pending...)
	}
	return filepath.Clean(resolved), nil
}

func canonicalComponentName(parent, requested string, requestedInfo os.FileInfo) (string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", err
	}
	for pass := 0; pass < 3; pass++ {
		for _, entry := range entries {
			name := entry.Name()
			if pass == 0 && name != requested {
				continue
			}
			if pass == 1 && (name == requested || !strings.EqualFold(name, requested)) {
				continue
			}
			if pass == 2 && (name == requested || strings.EqualFold(name, requested)) {
				continue
			}
			entryInfo, err := os.Lstat(filepath.Join(parent, name))
			if err == nil && os.SameFile(requestedInfo, entryInfo) {
				return name, nil
			}
		}
	}
	return "", errors.New("path component changed during canonicalization")
}

func pathComponents(path string) []string {
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	return strings.FieldsFunc(path, func(character rune) bool {
		return character == filepath.Separator
	})
}
