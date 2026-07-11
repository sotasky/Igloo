package storage

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	stateRootMarker = ".igloo-state-root"
	mediaRootMarker = ".igloo-media-root"
)

// Layout maps logical storage keys onto durable state and bulk-media roots.
type Layout struct {
	stateRoot         string
	mediaRoot         string
	externalMediaRoot bool
}

func New(stateRoot, mediaRoot string) (Layout, error) {
	if stateRoot == "" {
		return Layout{}, fmt.Errorf("state root is empty")
	}
	stateRoot, err := canonicalPath(stateRoot)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve state root: %w", err)
	}

	defaultMediaPath := filepath.Join(stateRoot, "media")
	defaultMediaRoot, err := canonicalPath(defaultMediaPath)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve default media root: %w", err)
	}
	if defaultMediaRoot != defaultMediaPath {
		return Layout{}, fmt.Errorf("default media root %q resolves to %q outside its reserved namespace", defaultMediaPath, defaultMediaRoot)
	}
	external := mediaRoot != ""
	if !external {
		mediaRoot = defaultMediaRoot
	} else {
		mediaRoot, err = canonicalPath(mediaRoot)
		if err != nil {
			return Layout{}, fmt.Errorf("resolve media root: %w", err)
		}
		external = mediaRoot != defaultMediaRoot
	}
	if external && (containsPath(stateRoot, mediaRoot) || containsPath(mediaRoot, stateRoot)) {
		return Layout{}, fmt.Errorf("external media root %q overlaps state root %q", mediaRoot, stateRoot)
	}
	return Layout{stateRoot: stateRoot, mediaRoot: mediaRoot, externalMediaRoot: external}, nil
}

func (l Layout) StateRoot() string { return l.stateRoot }

func (l Layout) MediaRoot() string { return l.mediaRoot }

func (l Layout) DatabasePath() string { return filepath.Join(l.stateRoot, "igloo.db") }

// Ensure validates the provisioned state root and creates only its reserved
// media directory. It never provisions or marks a storage root.
func (l Layout) Ensure() error {
	if l.stateRoot == "" || l.mediaRoot == "" {
		return fmt.Errorf("storage layout is not configured")
	}
	if err := requireProvisionedRoot(l.stateRoot, "state", stateRootMarker); err != nil {
		return err
	}
	if l.externalMediaRoot {
		if err := requireProvisionedRoot(l.mediaRoot, "external media", mediaRootMarker); err != nil {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(l.mediaRoot, 0o755); err != nil {
		return fmt.Errorf("create storage root %q: %w", l.mediaRoot, err)
	}
	return requireStableRoot(l.mediaRoot, "media")
}

// Path maps media/<rel> to MediaRoot and every other key to StateRoot.
func (l Layout) Path(key string) (string, error) {
	key, err := normalizeKey(key)
	if err != nil {
		return "", err
	}
	root, rel := l.stateRoot, key
	if strings.HasPrefix(key, "media/") {
		root, rel = l.mediaRoot, strings.TrimPrefix(key, "media/")
		if l.externalMediaRoot {
			if err := requireRootMarker(l.mediaRoot, "external media", mediaRootMarker); err != nil {
				return "", err
			}
		} else if err := requireRootMarker(l.stateRoot, "state", stateRootMarker); err != nil {
			return "", err
		}
	} else if err := requireRootMarker(l.stateRoot, "state", stateRootMarker); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// WritePath performs the filesystem validation intentionally omitted from
// read-path mapping before a caller mutates a storage object.
func (l Layout) WritePath(key string) (string, error) {
	absPath, err := l.Path(key)
	if err != nil {
		return "", err
	}
	root := l.stateRoot
	if _, ok := relativeTo(l.mediaRoot, absPath); ok {
		root = l.mediaRoot
	}
	if err := rejectChildSymlinks(root, absPath); err != nil {
		return "", err
	}
	return absPath, nil
}

// Key maps an absolute path below either configured root back to a logical key.
func (l Layout) Key(absPath string) (string, error) {
	if !filepath.IsAbs(absPath) {
		return "", fmt.Errorf("storage path %q is not absolute", absPath)
	}
	absPath = filepath.Clean(absPath)
	if rel, ok := relativeTo(l.mediaRoot, absPath); ok {
		if rel == "." {
			return "", fmt.Errorf("media root has no object key")
		}
		if l.externalMediaRoot {
			if err := requireRootMarker(l.mediaRoot, "external media", mediaRootMarker); err != nil {
				return "", err
			}
		} else if err := requireRootMarker(l.stateRoot, "state", stateRootMarker); err != nil {
			return "", err
		}
		return "media/" + filepath.ToSlash(rel), nil
	}
	if rel, ok := relativeTo(l.stateRoot, absPath); ok && rel != "." {
		if err := requireRootMarker(l.stateRoot, "state", stateRootMarker); err != nil {
			return "", err
		}
		key := filepath.ToSlash(rel)
		if key == "media" || strings.HasPrefix(key, "media/") {
			return "", fmt.Errorf("storage path %q occupies the reserved media namespace", absPath)
		}
		return key, nil
	}
	return "", fmt.Errorf("storage path %q is outside configured roots", absPath)
}

func requireProvisionedRoot(root, kind, markerName string) error {
	if err := requireStableRoot(root, kind); err != nil {
		return err
	}
	return requireRootMarker(root, kind, markerName)
}

func requireRootMarker(root, kind, markerName string) error {
	marker := filepath.Join(root, markerName)
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s root %q is missing marker %q", kind, root, marker)
	}
	return nil
}

func canonicalPath(name string) (string, error) {
	absPath, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	absPath = filepath.Clean(absPath)

	current := absPath
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// ValidateContainedPath rejects destinations outside root and child symlink
// aliases inside it.
func ValidateContainedPath(root, name string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root %q: %w", root, err)
	}
	name, err = filepath.Abs(name)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", name, err)
	}
	if !containsPath(root, name) {
		return fmt.Errorf("path %q is outside root %q", name, root)
	}
	if err := rejectChildSymlinks(root, name); err != nil {
		return err
	}
	return nil
}

func requireStableRoot(root, kind string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s root %q is unavailable", kind, root)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(resolved) != root {
		return fmt.Errorf("%s root %q changed location", kind, root)
	}
	return nil
}

func rejectChildSymlinks(root, name string) error {
	rel, ok := relativeTo(root, name)
	if !ok {
		return fmt.Errorf("path %q is outside root %q", name, root)
	}
	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect path %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q uses child symlink %q", name, current)
		}
	}
	return nil
}

func normalizeKey(key string) (string, error) {
	if key == "" || strings.ContainsRune(key, 0) || strings.Contains(key, `\`) || path.IsAbs(key) || filepath.IsAbs(key) {
		return "", fmt.Errorf("invalid logical key %q", key)
	}
	for _, part := range strings.Split(key, "/") {
		if part == ".." {
			return "", fmt.Errorf("logical key %q contains traversal", key)
		}
	}
	key = path.Clean(key)
	if key == "." || key == "media" {
		return "", fmt.Errorf("logical key %q does not name an object", key)
	}
	return key, nil
}

func containsPath(root, name string) bool {
	_, ok := relativeTo(root, name)
	return ok
}

func relativeTo(root, name string) (string, bool) {
	rel, err := filepath.Rel(root, name)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
