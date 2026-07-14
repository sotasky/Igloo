package download

import (
	"encoding/json"
	"os"
)

// CompletedDownload is the exact set of files published by one producer run.
// Sidecars are separate from playable media so callers never rediscover them
// by scanning a destination directory.
type CompletedDownload struct {
	MediaPaths    []string
	ThumbnailPath string
	InfoJSONPath  string
	SubtitlePaths []string
	Metadata      map[string]any
}

func regularPath(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}

func uniquePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func metadataFromFile(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var metadata map[string]any
	if json.Unmarshal(data, &metadata) != nil {
		return nil
	}
	return metadata
}

func removeCompletedDownloadFiles(completed CompletedDownload) {
	paths := append([]string(nil), completed.MediaPaths...)
	paths = append(paths, completed.SubtitlePaths...)
	paths = append(paths, completed.ThumbnailPath, completed.InfoJSONPath)
	for _, path := range paths {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}
