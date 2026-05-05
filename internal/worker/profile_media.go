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

func normalizeDownloadedImage(path, dir, key string) (string, error) {
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

func sniffImageContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

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
