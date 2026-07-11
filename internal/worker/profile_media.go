package worker

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var cachedImageExts = []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}

func normalizeDownloadedImage(path, dir, key string) (string, error) {
	published := false
	defer func() {
		if !published {
			_ = os.Remove(path)
		}
	}()
	key, err := safeProfileMediaKey(key)
	if err != nil {
		return "", err
	}
	contentType, err := sniffImageContentType(path)
	if err != nil {
		return "", err
	}
	finalPath := filepath.Join(dir, key+imageExtForContentType(contentType))
	for _, knownExt := range cachedImageExts {
		candidate := filepath.Join(dir, key+knownExt)
		if candidate == finalPath {
			continue
		}
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove alternate media %s: %w", candidate, err)
		}
	}
	if err := os.Rename(path, finalPath); err != nil {
		return "", fmt.Errorf("rename media %s -> %s: %w", path, finalPath, err)
	}
	published = true
	return finalPath, nil
}

func safeProfileMediaKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, `/\`) ||
		filepath.Base(key) != key || filepath.Clean(key) != key {
		return "", fmt.Errorf("unsafe media key %q", key)
	}
	return key, nil
}

func sniffImageContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
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
