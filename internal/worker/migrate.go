package worker

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

// migrateMediaPaths backfills the media_files table from feed media only:
//  1. Relocate feed_media/ files to media/twitter/{handle}/ and update DB paths
//  2. {DataDir}/feed_media/ and {DataDir}/media/twitter/*/ directory walk
//
// All inserts use INSERT OR IGNORE so re-runs are safe.
func (m *Manager) migrateMediaPaths() {
	log.Printf("[migrate] starting media path migration")
	total := 0

	// --- 1. Relocate feed_media/ files to media/twitter/{handle}/ ---
	movedCount := m.migrateFeedMediaToTwitterDirs()

	hasLegacyRows, err := m.db.HasLegacyFeedMediaPaths()
	if err != nil {
		log.Printf("[migrate] legacy feed media check: %v", err)
	} else if !hasLegacyRows && !m.hasLegacyFeedMediaFiles() {
		log.Printf("[migrate] media path migration done: %d feed media records inserted, %d feed media files relocated",
			total, movedCount)
		return
	}

	// --- 2. Re-index feed media files from disk (both legacy and new locations) ---
	feedMediaCount := m.migrateFeedMediaDir()
	total += feedMediaCount

	log.Printf("[migrate] media path migration done: %d feed media records inserted, %d feed media files relocated",
		total, movedCount)
}

// migrateFeedMediaDir indexes feed media files from both the legacy feed_media/
// flat directory and the new per-account media/twitter/{handle}/ directories.
// Uses INSERT OR IGNORE so re-runs are safe.
//
// Filename formats:
//
//	{tweet_id}_{index}.{ext}  — photo at given index
//	{tweet_id}.{ext}          — video/single file (index=0)
func (m *Manager) migrateFeedMediaDir() int {
	var files []model.MediaFile

	// Walk legacy flat directory.
	legacyDir := filepath.Join(m.cfg.DataDir, "feed_media")
	collectFeedMediaFiles(legacyDir, "feed_media", &files)

	// Walk per-account directories.
	twitterDir := filepath.Join(m.cfg.DataDir, "media", "twitter")
	if entries, err := os.ReadDir(twitterDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			handle := entry.Name()
			handleDir := filepath.Join(twitterDir, handle)
			relBase := filepath.Join("media", "twitter", handle)
			collectFeedMediaFiles(handleDir, relBase, &files)
		}
	}

	if len(files) == 0 {
		return 0
	}

	if err := m.db.InsertMediaFileBatch(files); err != nil {
		log.Printf("[migrate] InsertMediaFileBatch (feed_media dir): %v", err)
		return 0
	}
	return len(files)
}

func (m *Manager) hasLegacyFeedMediaFiles() bool {
	legacyDir := filepath.Join(m.cfg.DataDir, "feed_media")
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		stem := strings.TrimSuffix(entry.Name(), ext)
		tweetID, _, _ := parseFeedMediaFilename(stem, ext)
		if tweetID != "" {
			return true
		}
	}
	return false
}

// collectFeedMediaFiles walks dir and appends model.MediaFile entries to files,
// using relBase as the path prefix relative to DataDir.
func collectFeedMediaFiles(dir, relBase string, files *[]model.MediaFile) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		if stem == "" {
			return nil
		}

		tweetID, mediaIndex, mediaType := parseFeedMediaFilename(stem, ext)
		if tweetID == "" {
			return nil
		}

		*files = append(*files, model.MediaFile{
			OwnerType:  "feed_media",
			OwnerID:    tweetID,
			MediaIndex: mediaIndex,
			FilePath:   filepath.Join(relBase, name),
			MediaType:  mediaType,
		})
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[migrate] WalkDir %s: %v", dir, err)
	}
}

// migrateFeedMediaToTwitterDirs moves files from the legacy flat feed_media/
// directory to per-account media/twitter/{handle}/ directories, then updates
// the DB file_path records. Skips entries where no feed_item handle is found.
// This is idempotent — re-runs skip already-moved files.
func (m *Manager) migrateFeedMediaToTwitterDirs() int {
	all, err := m.db.GetMediaFilesByOwnerType("feed_media")
	if err != nil {
		log.Printf("[migrate] GetMediaFilesByOwnerType feed_media: %v", err)
		return 0
	}

	// Only process records still pointing at the legacy feed_media/ path.
	var toMigrate []model.MediaFile
	for _, mf := range all {
		if strings.HasPrefix(mf.FilePath, "feed_media/") {
			toMigrate = append(toMigrate, mf)
		}
	}
	if len(toMigrate) == 0 {
		return 0
	}

	// Collect unique tweet IDs.
	uniqueIDs := make([]string, 0, len(toMigrate))
	seen := make(map[string]bool)
	for _, mf := range toMigrate {
		if !seen[mf.OwnerID] {
			seen[mf.OwnerID] = true
			uniqueIDs = append(uniqueIDs, mf.OwnerID)
		}
	}

	// Batch-query feed_items in chunks of 500 (SQLite IN-clause limit).
	const chunkSize = 500
	handleMap := make(map[string]string, len(uniqueIDs))
	for i := 0; i < len(uniqueIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		items, err := m.db.GetFeedItemsForTweetIDs(uniqueIDs[i:end])
		if err != nil {
			log.Printf("[migrate] GetFeedItemsForTweetIDs chunk %d: %v", i/chunkSize, err)
			continue
		}
		for _, item := range items {
			handle := strings.TrimPrefix(item.SourceHandle, "twitter_")
			if handle == "" {
				handle = item.AuthorHandle
			}
			if handle != "" {
				handleMap[item.TweetID] = handle
			}
		}
	}

	var updates []db.MediaFilePathUpdate
	moved := 0

	for _, mf := range toMigrate {
		handle, ok := handleMap[mf.OwnerID]
		if !ok || handle == "" {
			log.Printf("[migrate] no handle for tweet %s, skipping", mf.OwnerID)
			continue
		}

		filename := filepath.Base(mf.FilePath)
		newRelPath := filepath.Join("media", "twitter", handle, filename)
		oldAbsPath := filepath.Join(m.cfg.DataDir, mf.FilePath)
		newAbsPath := filepath.Join(m.cfg.DataDir, newRelPath)

		destDir := filepath.Dir(newAbsPath)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			log.Printf("[migrate] mkdir %s: %v", destDir, err)
			continue
		}

		if _, err := os.Stat(oldAbsPath); errors.Is(err, os.ErrNotExist) {
			// Source already gone — update DB path only.
			updates = append(updates, db.MediaFilePathUpdate{
				OwnerType:  mf.OwnerType,
				OwnerID:    mf.OwnerID,
				MediaIndex: mf.MediaIndex,
				NewPath:    newRelPath,
			})
			continue
		}

		if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
			log.Printf("[migrate] rename %s -> %s: %v", oldAbsPath, newAbsPath, err)
			continue
		}
		moved++

		updates = append(updates, db.MediaFilePathUpdate{
			OwnerType:  mf.OwnerType,
			OwnerID:    mf.OwnerID,
			MediaIndex: mf.MediaIndex,
			NewPath:    newRelPath,
		})
	}

	if len(updates) > 0 {
		if err := m.db.BatchUpdateMediaFilePaths(updates); err != nil {
			log.Printf("[migrate] BatchUpdateMediaFilePaths: %v", err)
		}
	}

	log.Printf("[migrate] feed_media relocation: moved %d files, updated %d DB records", moved, len(updates))
	return moved
}

// parseFeedMediaFilename parses a feed_media filename stem into tweetID, index, mediaType.
// stem examples: "1234567890", "1234567890_0", "1234567890_2"
// ext examples: ".jpg", ".mp4", ".webm"
func parseFeedMediaFilename(stem, ext string) (tweetID string, index int, mediaType string) {
	// Determine media type from extension.
	switch strings.ToLower(ext) {
	case ".mp4", ".webm", ".mkv", ".avi", ".mov":
		mediaType = "video"
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		mediaType = "photo"
	default:
		mediaType = "photo"
	}

	// Check for _{index} suffix.
	lastUS := strings.LastIndex(stem, "_")
	if lastUS < 0 {
		// No underscore: tweet_id only.
		return stem, 0, mediaType
	}

	possibleIndex := stem[lastUS+1:]
	n := 0
	valid := len(possibleIndex) > 0
	for _, c := range possibleIndex {
		if c < '0' || c > '9' {
			valid = false
			break
		}
		n = n*10 + int(c-'0')
	}

	if valid {
		// {tweet_id}_{index}
		return stem[:lastUS], n, mediaType
	}

	// Underscore but not a numeric index — treat whole stem as tweet_id.
	return stem, 0, mediaType
}
