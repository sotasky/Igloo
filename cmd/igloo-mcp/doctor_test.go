package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	igloodb "github.com/screwys/igloo/internal/db"
)

func TestDoctorStatusReportsLocalHealthAndMasksSecrets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", tmp)
	resetTestServerDB(t)

	d, err := igloodb.Open(filepath.Join(tmp, "igloo.db"), tmp)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO android_sync_generations (
			generation_id, created_at_ms, status, source_version, retention_json,
			item_count, asset_count, ready_asset_count, server_missing_asset_count,
			total_bytes, content_counts_json, asset_counts_json
		) VALUES ('android-sync-doctor', ?, 'ready', 'doctor-source', '{}', 1, 2, 1, 1, 1024, '{}', '{}')
	`, now); err != nil {
		t.Fatalf("insert generation: %v", err)
	}
	for i, generationID := range []string{"android-sync-doctor-old-1", "android-sync-doctor-old-2"} {
		createdAtMs := now - int64(48-i)*int64(time.Hour/time.Millisecond)
		if err := d.ExecRaw(`
			INSERT INTO android_sync_generations (
				generation_id, created_at_ms, status, source_version, retention_json,
				item_count, asset_count, ready_asset_count, server_missing_asset_count,
				total_bytes, content_counts_json, asset_counts_json
			) VALUES (?, ?, 'ready', ?, '{}', 1, 1, 1, 0, 1, '{}', '{}')
		`, generationID, createdAtMs, generationID+"-source"); err != nil {
			t.Fatalf("insert old generation %s: %v", generationID, err)
		}
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_items (generation_id, seq, item_kind, item_id, payload_json)
		VALUES ('android-sync-doctor-old-1', 1, 'videos', 'doctor-video-old', '{}')
	`); err != nil {
		t.Fatalf("insert old sync item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_assets (
			generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
			bucket, server_url, content_type, size_bytes, sha256, state,
			required_reason, effective_recency_ms
		) VALUES ('android-sync-doctor-old-1', 1, 'doctor-asset-old', 'video_stream',
			'doctor-video-old', 'video', 'videos', '/asset', 'video/mp4', 1, 'sha',
			'ready', 'retention', 1)
	`); err != nil {
		t.Fatalf("insert old sync asset: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_health_reports (
			generation_id, reported_at_ms, payload_json, verified_assets,
			pending_assets, failed_assets, missing_assets, total_assets, verified_bytes
		) VALUES ('android-sync-doctor-old-1', 1, '{}', 1, 0, 0, 0, 1, 1)
	`); err != nil {
		t.Fatalf("insert old sync health: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name, avatar_url, banner_url)
		VALUES ('twitter_sample_profile', 'twitter', 'sample_profile', 'Doctor', 'https://cdn.example.invalid/avatar.jpg', 'https://cdn.example.invalid/banner.jpg')
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type)
		VALUES ('feed_media', 'sample_post', 0, 'media/sample_post_0.jpg', 'photo')
	`); err != nil {
		t.Fatalf("insert media file: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, thumbnail_path, file_path, dearrow_thumb_path, published_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'Doctor Video', 'videos/sample_video.jpg', 'videos/sample_video.mp4', 'thumbnails/dearrow/sample_video.jpg', ?)
	`, now); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, sha256, state, created_at_ms, updated_at_ms
		) VALUES ('sample_post_asset', 'post_media', 'tweet', 'sample_post', 0,
			'media/sample_post_0.jpg', 'image/jpeg', 10, 'sha', 'ready', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, sha256, state,
			lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		) VALUES ('sample_downloading_asset', 'post_thumbnail', 'tweet', 'sample_post', 0,
			'thumbnails/generated/sample_post.jpg', 'image/jpeg', 0, '', 'downloading',
			'worker-a', ?, ?, ?)
	`, now-1, now, now); err != nil {
		t.Fatalf("insert downloading asset: %v", err)
	}
	for _, path := range []string{
		filepath.Join(tmp, "thumbnails", "avatars", "sample_profile.jpg"),
		filepath.Join(tmp, "thumbnails", "banners", "sample_profile.jpg"),
		filepath.Join(tmp, "thumbnails", "previews", "sample_video", "track.json"),
		filepath.Join(tmp, "thumbnails", "previews", "sample_video", "sprite.jpg"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir asset fixture dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatalf("write asset fixture %s: %v", path, err)
		}
	}
	if err := d.ExecRaw(`
		INSERT INTO download_queue (video_id, channel_id, title, status, error, added_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'Doctor Video', 'failed', 'sample failure', ?)
	`, now); err != nil {
		t.Fatalf("insert download queue: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO downloader_operations (
			operation, platform, subject, tool, started_at_ms, ended_at_ms,
			status, error_kind, error
		) VALUES ('download', 'youtube', 'sample_video', 'yt-dlp', ?, ?, 'failed', 'network', 'sample failure')
	`, now, now); err != nil {
		t.Fatalf("insert downloader op: %v", err)
	}
	if err := d.ExecRaw(`CREATE TABLE custom_lifecycle_probe (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create custom lifecycle probe: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO custom_lifecycle_probe (value) VALUES ('sample')`); err != nil {
		t.Fatalf("insert custom lifecycle probe: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(tmp, "logs", "igloo.log"),
		[]byte("ERROR failed with token=super-secret-token cookie=session-secret\n"),
		0o644,
	); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "logs", "android"), 0o755); err != nil {
		t.Fatalf("mkdir android logs: %v", err)
	}
	androidLog := strings.Join([]string{
		`{"event":"android_sync_asset_failed","fields":{"asset_kind":"post_thumbnail","error":"verify_failed","generation_id":"android-sync-doctor","asset_id":"sample_asset_1"},"level":"info"}`,
		`{"event":"android_sync_asset_failed","fields":{"asset_kind":"post_thumbnail","error":"verify_failed","generation_id":"android-sync-doctor","asset_id":"sample_asset_2"},"level":"info"}`,
		`{"event":"android_sync_asset_stale","fields":{"asset_kind":"post_thumbnail","reason":"stale_generation_asset_verify_failed","generation_id":"android-sync-doctor","asset_id":"sample_asset_3"},"level":"info"}`,
		`{"event":"android_sync_metadata_retry","fields":{"label":"latest_generation","error":"Sync HTTP 502 for latest_generation"},"level":"info"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, "logs", "android", "server.log"), []byte(androidLog), 0o644); err != nil {
		t.Fatalf("write android log: %v", err)
	}

	report, err := doctorStatus()
	if err != nil {
		t.Fatalf("doctorStatus: %v", err)
	}
	for _, want := range []string{
		"=== Igloo Doctor ===",
		"Database files:",
		"SQLite storage:",
		"page_size:",
		"reclaimable freelist:",
		"Persistence lifecycle:",
		"archive:",
		"maintained_state:",
		"derived_cache:",
		"queue:",
		"unclassified:",
		"warnings:",
		"unclassified_tables",
		"Android sync:",
		"Queue counts:",
		"Profile/media readiness:",
		"Asset inventory parity:",
		"inventory states: downloading=1, ready=1",
		"asset leases: active_downloading=0 expired_downloading=1",
		"post_media:          assets=1 legacy=1 gap=0",
		"video_stream:        assets=0 legacy=1 gap=1",
		"post_thumbnail:      assets=1 legacy=1 gap=0",
		"dearrow_thumbnail:   assets=0 legacy=1 gap=1",
		"avatar:              assets=0 legacy=1 gap=1",
		"banner:              assets=0 legacy=1 gap=1",
		"preview_track_json:  assets=0 legacy=1 gap=1",
		"preview_sprite:      assets=0 legacy=1 gap=1",
		"Downloader failures:",
		"Recent high-signal log errors:",
		"Android sync client failures:",
		"asset_failed verify_failed/post_thumbnail=2",
		"asset_stale stale_generation_asset_verify_failed/post_thumbnail=1",
		"metadata_retry latest_generation/http_502=1",
		"android-sync-doctor",
		"prune eligible: generations=1 items=1 assets=1 health_reports=1",
		"network=1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "super-secret-token") || strings.Contains(report, "session-secret") {
		t.Fatalf("doctor report leaked sensitive log values:\n%s", report)
	}
}

func TestMaskSensitiveMasksHeaderAndColonSecrets(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer auth-header-secret",
		"Cookie: session=cookie-secret; theme=dark",
		"Set-Cookie: session=set-cookie-secret; Path=/; HttpOnly",
		"access_token: colon-token-secret",
		"api-key: colon-api-secret",
		"token=key-value-token-secret cookie=key-value-cookie-secret",
	}, "\n")

	masked := maskSensitive(input)
	for _, secret := range []string{
		"auth-header-secret",
		"cookie-secret",
		"set-cookie-secret",
		"colon-token-secret",
		"colon-api-secret",
		"key-value-token-secret",
		"key-value-cookie-secret",
	} {
		if strings.Contains(masked, secret) {
			t.Fatalf("maskSensitive leaked %q in:\n%s", secret, masked)
		}
	}
	for _, want := range []string{
		"Authorization: ***",
		"Cookie: ***",
		"Set-Cookie: ***",
		"access_token: ***",
		"api-key: ***",
		"token=***",
		"cookie=***",
	} {
		if !strings.Contains(masked, want) {
			t.Fatalf("maskSensitive missing %q in:\n%s", want, masked)
		}
	}
}

func resetTestServerDB(t *testing.T) {
	t.Helper()
	serverDBMu.Lock()
	defer serverDBMu.Unlock()
	if serverDB != nil {
		if err := serverDB.Close(); err != nil {
			t.Fatalf("close cached server DB: %v", err)
		}
		serverDB = nil
	}
}
