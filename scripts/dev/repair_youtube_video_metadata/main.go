// repair_youtube_video_metadata backfills broken downloaded YouTube rows whose
// canonical metadata was clobbered by a partial download ingest. It repairs:
//   - duration      via info.json when present, else ffprobe on the local file
//   - thumbnail_path via sibling {videoID}.webp/.jpg/.png when present
//   - published_at  via info.json when present
//   - metadata_json via stripped info.json when present
//
// Usage:
//
//	go run ./scripts/dev/repair_youtube_video_metadata           # apply
//	go run ./scripts/dev/repair_youtube_video_metadata -dry-run  # preview
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

var (
	dataDir = os.ExpandEnv("$HOME/.local/share/igloo")
	dbPath  = filepath.Join(dataDir, "igloo.db")
	dryRun  = flag.Bool("dry-run", false, "print planned repairs without updating the database")
)

type videoRow struct {
	videoID       string
	duration      int
	thumbnailPath string
	filePath      string
	publishedAt   int64
	metadataJSON  string
}

func main() {
	flag.Parse()

	db, err := dbpkg.Open(dbPath, dataDir)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	rows, err := loadRows(db)
	if err != nil {
		log.Fatalf("load rows: %v", err)
	}
	fmt.Printf("candidate rows: %d\n", len(rows))

	type repair struct {
		videoID       string
		duration      int
		thumbnailPath string
		publishedAt   int64
		metadataJSON  string
	}

	var repairs []repair
	for _, row := range rows {
		fix := repair{
			videoID:       row.videoID,
			duration:      row.duration,
			thumbnailPath: row.thumbnailPath,
			publishedAt:   row.publishedAt,
			metadataJSON:  row.metadataJSON,
		}

		absVideoPath := row.filePath
		if !filepath.IsAbs(absVideoPath) {
			absVideoPath = filepath.Join(dataDir, row.filePath)
		}
		info := loadInfoJSON(absVideoPath, row.videoID)

		if fix.duration <= 0 {
			if metadataDuration := extractDurationFromMetadata(info); metadataDuration > 0 {
				fix.duration = metadataDuration
			} else if probedDuration := probeDuration(absVideoPath); probedDuration > 0 {
				fix.duration = probedDuration
			}
		}
		if fix.thumbnailPath == "" {
			if sibling := findSiblingThumbnail(absVideoPath, row.videoID); sibling != "" {
				fix.thumbnailPath = toRelPath(dataDir, sibling)
			}
		}
		if fix.publishedAt <= 0 {
			if publishedAt := extractPublishedAt(info); publishedAt > 0 {
				fix.publishedAt = publishedAt
			}
		}
		if fix.metadataJSON == "" && info != nil {
			stripped := model.StripVideoMetadata(info)
			if stripped != nil {
				if payload, err := json.Marshal(stripped); err == nil {
					fix.metadataJSON = string(payload)
				}
			}
		}

		if fix.duration != row.duration ||
			fix.thumbnailPath != row.thumbnailPath ||
			fix.publishedAt != row.publishedAt ||
			fix.metadataJSON != row.metadataJSON {
			repairs = append(repairs, fix)
		}
	}

	fmt.Printf("repairable rows: %d\n", len(repairs))
	for i, fix := range repairs {
		if i >= 20 {
			fmt.Println("...")
			break
		}
		fmt.Printf(
			"  %s duration=%d thumb=%q published_at=%d metadata=%t\n",
			fix.videoID, fix.duration, fix.thumbnailPath, fix.publishedAt, fix.metadataJSON != "",
		)
	}

	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}

	if err := db.WithWrite(func(tx *sql.Tx) error {
		for _, fix := range repairs {
			seq := db.NextSyncSeq()
			if _, err := tx.Exec(`
				UPDATE videos
				SET duration = ?,
				    thumbnail_path = ?,
				    published_at = ?,
				    metadata_json = ?,
				    sync_seq = ?
				WHERE video_id = ?
			`, fix.duration, fix.thumbnailPath, fix.publishedAt, fix.metadataJSON, seq, fix.videoID); err != nil {
				return fmt.Errorf("repair %s: %w", fix.videoID, err)
			}
		}
		return nil
	}); err != nil {
		log.Fatalf("apply repairs: %v", err)
	}

	fmt.Printf("done: repaired %d rows\n", len(repairs))
}

func loadRows(db *dbpkg.DB) ([]videoRow, error) {
	var out []videoRow
	err := db.WithRead(func(conn *sql.DB) error {
		rows, err := conn.Query(`
			SELECT video_id, COALESCE(duration, 0), COALESCE(thumbnail_path, ''),
			       COALESCE(file_path, ''), COALESCE(published_at, 0), COALESCE(metadata_json, '')
			FROM videos
			WHERE channel_id LIKE 'youtube_%'
			  AND COALESCE(file_path, '') != ''
			  AND (
			        COALESCE(duration, 0) = 0 OR
			        COALESCE(thumbnail_path, '') = '' OR
			        COALESCE(published_at, 0) = 0 OR
			        COALESCE(metadata_json, '') = ''
			  )
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row videoRow
			if err := rows.Scan(
				&row.videoID,
				&row.duration,
				&row.thumbnailPath,
				&row.filePath,
				&row.publishedAt,
				&row.metadataJSON,
			); err != nil {
				return err
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func probeDuration(absVideoPath string) int {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		absVideoPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int(math.Round(seconds))
}

func loadInfoJSON(videoPath, videoID string) map[string]any {
	dir := filepath.Dir(videoPath)
	p := filepath.Join(dir, videoID+".info.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func extractDurationFromMetadata(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}
	switch v := metadata["duration"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return 0
}

func findSiblingThumbnail(videoPath, videoID string) string {
	dir := filepath.Dir(videoPath)
	for _, ext := range []string{".webp", ".jpg", ".jpeg", ".png"} {
		p := filepath.Join(dir, videoID+ext)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func extractPublishedAt(metadata map[string]any) int64 {
	if metadata == nil {
		return 0
	}
	for _, key := range []string{"release_timestamp", "timestamp"} {
		switch v := metadata[key].(type) {
		case float64:
			if v > 0 {
				return int64(v) * 1000
			}
		case int64:
			if v > 0 {
				return v * 1000
			}
		}
	}
	for _, key := range []string{"release_date", "upload_date", "published_at", "created_at"} {
		if value, _ := metadata[key].(string); value != "" {
			if parsed := parseDateString(value); parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func parseDateString(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	layouts := []string{
		"20060102",
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().UnixMilli()
		}
	}
	return 0
}

func toRelPath(baseDir, absPath string) string {
	rel, err := filepath.Rel(baseDir, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}
