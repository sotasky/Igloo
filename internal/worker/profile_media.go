package worker

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var cachedImageExts = []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}

func conventionalMediaPath(dir, key string) string {
	key, err := safeProfileMediaKey(key)
	if err != nil {
		return ""
	}
	for _, ext := range cachedImageExts {
		candidate := filepath.Join(dir, key+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func hasConventionalMediaFile(dir, key string) bool {
	return conventionalMediaPath(dir, key) != ""
}

func removeConventionalMediaFiles(dir, key string) error {
	key, err := safeProfileMediaKey(key)
	if err != nil {
		return err
	}
	for _, ext := range cachedImageExts {
		candidate := filepath.Join(dir, key+ext)
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func normalizeDownloadedImage(path, dir, key string) (string, error) {
	key, err := safeProfileMediaKey(key)
	if err != nil {
		return "", err
	}
	contentType, err := sniffImageContentType(path)
	if err != nil {
		return "", err
	}
	ext := imageExtForContentType(contentType)
	finalPath := filepath.Join(dir, key+ext)
	for _, knownExt := range cachedImageExts {
		candidate := filepath.Join(dir, key+knownExt)
		if candidate == finalPath {
			continue
		}
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove stale media %s: %w", candidate, err)
		}
	}
	if err := os.Rename(path, finalPath); err != nil {
		return "", fmt.Errorf("rename media %s -> %s: %w", path, finalPath, err)
	}
	return finalPath, nil
}

func safeProfileMediaKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || key == "." || key == ".." {
		return "", fmt.Errorf("unsafe profile media key %q", key)
	}
	if strings.ContainsAny(key, `/\`) || filepath.Base(key) != key || filepath.Clean(key) != key {
		return "", fmt.Errorf("unsafe profile media key %q", key)
	}
	return key, nil
}

func sniffImageContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	contentType := http.DetectContentType(buf[:n])
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("unexpected content type %q for %s", contentType, path)
	}
	return contentType, nil
}

func imageExtForContentType(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
