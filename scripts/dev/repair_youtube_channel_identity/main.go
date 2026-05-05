// repair_youtube_channel_identity fixes two related YouTube profile bugs:
//
//  1. handle-style youtube_<handle> channel IDs that should be canonical
//     youtube_UC... rows, including their videos/follows/settings/queues
//  2. stale YouTube profile caches where a wide banner crop was stored as the
//     avatar (or avatar == banner)
//
// Usage:
//
//	go run ./scripts/dev/repair_youtube_channel_identity           # apply
//	go run ./scripts/dev/repair_youtube_channel_identity -dry-run  # preview
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

var (
	dataDir = os.ExpandEnv("$HOME/.local/share/igloo")
	dbPath  = filepath.Join(dataDir, "igloo.db")
	dryRun  = flag.Bool("dry-run", false, "print planned repairs without writing")
)

type channelRow struct {
	ChannelID     string
	SourceID      string
	Name          string
	URL           string
	Platform      string
	Quality       string
	CheckInterval sql.NullInt64
}

type mergeCandidate struct {
	Old channelRow
	New string
}

func main() {
	flag.Parse()

	store, err := db.Open(dbPath, dataDir)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	merges, err := findMergeCandidates(store)
	if err != nil {
		log.Fatalf("find merge candidates: %v", err)
	}
	suspicious, err := findSuspiciousRefreshIDs(store)
	if err != nil {
		log.Fatalf("find suspicious profile ids: %v", err)
	}

	refreshIDs := make(map[string]struct{})
	staleIDs := make(map[string]struct{})
	for _, candidate := range merges {
		refreshIDs[candidate.New] = struct{}{}
		staleIDs[candidate.Old.ChannelID] = struct{}{}
		staleIDs[candidate.New] = struct{}{}
	}
	for _, channelID := range suspicious {
		refreshIDs[channelID] = struct{}{}
		staleIDs[channelID] = struct{}{}
	}

	fmt.Printf("merge candidates: %d\n", len(merges))
	for _, candidate := range merges {
		fmt.Printf("  %s -> %s (%s)\n", candidate.Old.ChannelID, candidate.New, firstNonEmpty(candidate.Old.URL, candidate.Old.Name))
	}
	fmt.Printf("profile refresh candidates: %d\n", len(refreshIDs))
	for _, channelID := range sortedKeys(refreshIDs) {
		fmt.Printf("  %s\n", channelID)
	}

	if *dryRun {
		fmt.Println("dry-run: no changes applied")
		return
	}

	if err := store.WithWrite(func(tx *sql.Tx) error {
		for _, candidate := range merges {
			if err := applyMerge(tx, store, candidate); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Fatalf("apply merges: %v", err)
	}

	avatarDir := filepath.Join(dataDir, "thumbnails", "avatars")
	bannerDir := filepath.Join(dataDir, "thumbnails", "banners")
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		log.Fatalf("mkdir avatar dir: %v", err)
	}
	if err := os.MkdirAll(bannerDir, 0o755); err != nil {
		log.Fatalf("mkdir banner dir: %v", err)
	}

	for _, channelID := range sortedKeys(staleIDs) {
		if err := removeConventionalMedia(avatarDir, channelID); err != nil {
			log.Printf("remove avatar cache %s: %v", channelID, err)
		}
		if err := removeConventionalMedia(bannerDir, channelID); err != nil {
			log.Printf("remove banner cache %s: %v", channelID, err)
		}
	}

	for _, channelID := range sortedKeys(refreshIDs) {
		if err := refreshProfile(store, channelID, avatarDir, bannerDir); err != nil {
			log.Printf("refresh %s: %v", channelID, err)
		}
	}

	fmt.Printf("done: merged=%d refreshed=%d\n", len(merges), len(refreshIDs))
}

func findMergeCandidates(store *db.DB) ([]mergeCandidate, error) {
	var rows []channelRow
	err := store.WithRead(func(conn *sql.DB) error {
		result, err := conn.Query(`
			SELECT channel_id, COALESCE(source_id,''), COALESCE(name,''), COALESCE(url,''),
			       COALESCE(platform,''), COALESCE(quality,''), check_interval
			FROM channels
			WHERE channel_id LIKE 'youtube_%'
		`)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var row channelRow
			if err := result.Scan(
				&row.ChannelID,
				&row.SourceID,
				&row.Name,
				&row.URL,
				&row.Platform,
				&row.Quality,
				&row.CheckInterval,
			); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return result.Err()
	})
	if err != nil {
		return nil, err
	}

	var candidates []mergeCandidate
	for _, row := range rows {
		target := download.CanonicalizeYouTubeChannelID(strings.TrimPrefix(row.ChannelID, "youtube_"), row.URL)
		if target == "" || target == row.ChannelID || !strings.HasPrefix(target, "youtube_UC") {
			continue
		}
		candidates = append(candidates, mergeCandidate{
			Old: row,
			New: target,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Old.ChannelID < candidates[j].Old.ChannelID
	})
	return candidates, nil
}

func findSuspiciousRefreshIDs(store *db.DB) ([]string, error) {
	ids := make(map[string]struct{})

	err := store.WithRead(func(conn *sql.DB) error {
		rows, err := conn.Query(`
			SELECT channel_id, COALESCE(avatar_url,''), COALESCE(banner_url,'')
			FROM channel_profiles
			WHERE platform = 'youtube'
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var channelID, avatarURL, bannerURL string
			if err := rows.Scan(&channelID, &avatarURL, &bannerURL); err != nil {
				return err
			}
			if avatarURL != "" && avatarURL == bannerURL {
				ids[channelID] = struct{}{}
				continue
			}
			if strings.Contains(strings.ToLower(avatarURL), "fcrop64") {
				ids[channelID] = struct{}{}
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	avatarDir := filepath.Join(dataDir, "thumbnails", "avatars")
	entries, err := os.ReadDir(avatarDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "youtube_") {
			continue
		}
		channelID := strings.TrimSuffix(name, filepath.Ext(name))
		width, height, ok := imageConfig(filepath.Join(avatarDir, name))
		if ok && width > height*2 {
			ids[channelID] = struct{}{}
		}
	}
	return sortedKeys(ids), nil
}

func applyMerge(tx *sql.Tx, store *db.DB, candidate mergeCandidate) error {
	existing, err := loadChannel(tx, candidate.New)
	if err != nil {
		return fmt.Errorf("load canonical %s: %w", candidate.New, err)
	}
	merged := channelRow{
		ChannelID: candidate.New,
		SourceID:  firstNonEmpty(existing.SourceID, candidate.Old.SourceID),
		Name:      firstNonEmpty(existing.Name, candidate.Old.Name),
		URL:       firstNonEmpty(existing.URL, candidate.Old.URL, canonicalYouTubeURL(candidate.New)),
		Platform:  "youtube",
		Quality:   firstNonEmpty(existing.Quality, candidate.Old.Quality),
	}
	if existing.CheckInterval.Valid {
		merged.CheckInterval = existing.CheckInterval
	} else {
		merged.CheckInterval = candidate.Old.CheckInterval
	}

	channelSeq := store.NextSyncSeq()
	if _, err := tx.Exec(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, quality, check_interval, sync_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			source_id = CASE
				WHEN COALESCE(channels.source_id,'') = '' AND excluded.source_id IS NOT NULL THEN excluded.source_id
				ELSE channels.source_id
			END,
			name = CASE
				WHEN COALESCE(channels.name,'') = '' AND excluded.name IS NOT NULL THEN excluded.name
				ELSE channels.name
			END,
			url = CASE
				WHEN COALESCE(channels.url,'') = '' AND excluded.url IS NOT NULL THEN excluded.url
				ELSE channels.url
			END,
			platform = 'youtube',
			quality = CASE
				WHEN COALESCE(channels.quality,'') = '' AND excluded.quality IS NOT NULL THEN excluded.quality
				ELSE channels.quality
			END,
			check_interval = COALESCE(channels.check_interval, excluded.check_interval),
			sync_seq = excluded.sync_seq
	`, merged.ChannelID, nilIfEmpty(merged.SourceID), nilIfEmpty(merged.Name), nilIfEmpty(merged.URL), merged.Platform, nilIfEmpty(merged.Quality), nullableInt64(merged.CheckInterval), channelSeq); err != nil {
		return fmt.Errorf("upsert canonical channel %s: %w", candidate.New, err)
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO channel_follows (user_id, channel_id, followed_at)
		SELECT user_id, ?, followed_at
		FROM channel_follows
		WHERE channel_id = ?
	`, candidate.New, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move channel_follows %s: %w", candidate.Old.ChannelID, err)
	}
	if _, err := tx.Exec(`DELETE FROM channel_follows WHERE channel_id = ?`, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("delete old channel_follows %s: %w", candidate.Old.ChannelID, err)
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO channel_stars (user_id, channel_id, starred_at)
		SELECT user_id, ?, starred_at
		FROM channel_stars
		WHERE channel_id = ?
	`, candidate.New, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move channel_stars %s: %w", candidate.Old.ChannelID, err)
	}
	if _, err := tx.Exec(`DELETE FROM channel_stars WHERE channel_id = ?`, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("delete old channel_stars %s: %w", candidate.Old.ChannelID, err)
	}

	if _, err := tx.Exec(`
		INSERT INTO channel_settings (
			channel_id, media_only, include_reposts, media_download_limit,
			max_videos, download_subtitles, updated_at
		)
		SELECT ?, media_only, include_reposts, media_download_limit,
		       max_videos, download_subtitles, updated_at
		FROM channel_settings
		WHERE channel_id = ?
		ON CONFLICT(channel_id) DO UPDATE SET
			media_only = COALESCE(channel_settings.media_only, excluded.media_only),
			include_reposts = COALESCE(channel_settings.include_reposts, excluded.include_reposts),
			media_download_limit = COALESCE(channel_settings.media_download_limit, excluded.media_download_limit),
			max_videos = COALESCE(channel_settings.max_videos, excluded.max_videos),
			download_subtitles = COALESCE(channel_settings.download_subtitles, excluded.download_subtitles),
			updated_at = CASE
				WHEN excluded.updated_at > channel_settings.updated_at THEN excluded.updated_at
				ELSE channel_settings.updated_at
			END
	`, candidate.New, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move channel_settings %s: %w", candidate.Old.ChannelID, err)
	}
	if _, err := tx.Exec(`DELETE FROM channel_settings WHERE channel_id = ?`, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("delete old channel_settings %s: %w", candidate.Old.ChannelID, err)
	}

	videoSeq := store.NextSyncSeq()
	if _, err := tx.Exec(`
		UPDATE videos
		SET channel_id = ?, sync_seq = ?
		WHERE channel_id = ?
	`, candidate.New, videoSeq, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move videos %s: %w", candidate.Old.ChannelID, err)
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO channel_queue (channel_id, status, priority, added_at, started_at, completed_at)
		SELECT ?, status, priority, added_at, started_at, completed_at
		FROM channel_queue
		WHERE channel_id = ?
	`, candidate.New, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move channel_queue %s: %w", candidate.Old.ChannelID, err)
	}
	if _, err := tx.Exec(`DELETE FROM channel_queue WHERE channel_id = ?`, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("delete old channel_queue %s: %w", candidate.Old.ChannelID, err)
	}

	if _, err := tx.Exec(`UPDATE download_queue SET channel_id = ? WHERE channel_id = ?`, candidate.New, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("move download_queue %s: %w", candidate.Old.ChannelID, err)
	}

	if _, err := tx.Exec(`DELETE FROM channel_profiles WHERE channel_id IN (?, ?)`, candidate.Old.ChannelID, candidate.New); err != nil {
		return fmt.Errorf("delete old profiles %s: %w", candidate.Old.ChannelID, err)
	}
	if _, err := tx.Exec(`DELETE FROM channels WHERE channel_id = ?`, candidate.Old.ChannelID); err != nil {
		return fmt.Errorf("delete old channel %s: %w", candidate.Old.ChannelID, err)
	}
	return nil
}

func loadChannel(tx *sql.Tx, channelID string) (channelRow, error) {
	var row channelRow
	err := tx.QueryRow(`
		SELECT channel_id, COALESCE(source_id,''), COALESCE(name,''), COALESCE(url,''),
		       COALESCE(platform,''), COALESCE(quality,''), check_interval
		FROM channels
		WHERE channel_id = ?
	`, channelID).Scan(
		&row.ChannelID,
		&row.SourceID,
		&row.Name,
		&row.URL,
		&row.Platform,
		&row.Quality,
		&row.CheckInterval,
	)
	if err == sql.ErrNoRows {
		return channelRow{}, nil
	}
	return row, err
}

func refreshProfile(store *db.DB, channelID, avatarDir, bannerDir string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	profile, err := fetchprofile.Fetch(ctx, channelID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := store.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:    profile.ChannelID,
		Platform:     profile.Platform,
		Handle:       profile.Handle,
		DisplayName:  profile.DisplayName,
		Bio:          profile.Bio,
		Website:      profile.Website,
		Followers:    profile.Followers,
		Following:    profile.Following,
		Verified:     profile.Verified,
		VerifiedType: profile.VerifiedType,
		Protected:    profile.Protected,
		AvatarURL:    profile.AvatarURL,
		BannerURL:    profile.BannerURL,
		FetchedAt:    &now,
		FailCount:    0,
	}); err != nil {
		return err
	}

	httpDL := download.NewHTTPDownloader()
	if profile.AvatarURL != "" {
		if err := downloadProfileMedia(ctx, httpDL, profile.ChannelID, profile.AvatarURL, avatarDir); err != nil {
			return fmt.Errorf("avatar: %w", err)
		}
	}
	if profile.BannerURL != "" && !strings.HasPrefix(profile.BannerURL, "synth:") {
		if err := downloadProfileMedia(ctx, httpDL, profile.ChannelID, profile.BannerURL, bannerDir); err != nil {
			return fmt.Errorf("banner: %w", err)
		}
	}
	return nil
}

func downloadProfileMedia(ctx context.Context, httpDL *download.HTTPDownloader, channelID, rawURL, dir string) error {
	tmpPath, err := httpDL.DownloadFile(ctx, rawURL, dir, channelID+".download")
	if err != nil {
		return err
	}
	_, err = normalizeDownloadedImage(tmpPath, dir, channelID)
	return err
}

func removeConventionalMedia(dir, channelID string) error {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		path := filepath.Join(dir, channelID+ext)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func normalizeDownloadedImage(path, dir, key string) (string, error) {
	contentType, err := sniffImageContentType(path)
	if err != nil {
		return "", err
	}
	ext := imageExtForContentType(contentType)
	finalPath := filepath.Join(dir, key+ext)
	for _, knownExt := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		candidate := filepath.Join(dir, key+knownExt)
		if candidate == finalPath {
			continue
		}
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	if err := os.Rename(path, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

func sniffImageContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return "", err
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

func imageConfig(path string) (int, int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

func canonicalYouTubeURL(channelID string) string {
	if !strings.HasPrefix(channelID, "youtube_") {
		return ""
	}
	return "https://www.youtube.com/channel/" + strings.TrimPrefix(channelID, "youtube_")
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func nilIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullableInt64(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}
