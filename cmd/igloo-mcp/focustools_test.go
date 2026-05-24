package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	igloodb "github.com/screwys/igloo/internal/db"
)

func TestAndroidSyncStatusReportsConvergenceEvidence(t *testing.T) {
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
		) VALUES ('sample_generation', ?, 'ready', 'sample-source', '{}', 2, 2, 1, 1, 2048,
			'{"feed_items":2}', '{"post_media":1,"avatar":1}')
	`, now); err != nil {
		t.Fatalf("insert generation: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_assets (
			generation_id, seq, asset_id, asset_kind, owner_id, owner_kind,
			bucket, server_url, content_type, size_bytes, sha256, state,
			required_reason, effective_recency_ms
		) VALUES
			('sample_generation', 1, 'sample_asset_ready', 'post_media', 'sample_post', 'tweet',
				'media', '/asset/ready', 'image/jpeg', 512, 'sha-ready', 'ready', 'retention', ?),
			('sample_generation', 2, 'sample_asset_missing', 'avatar', 'twitter_sample_author', 'channel',
				'avatars', '/asset/missing', 'image/jpeg', 0, '', 'server_missing', 'profile', ?)
	`, now, now); err != nil {
		t.Fatalf("insert sync assets: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO android_sync_health_reports (
			generation_id, reported_at_ms, payload_json, verified_assets,
			pending_assets, failed_assets, missing_assets, total_assets, verified_bytes
		) VALUES ('sample_generation', ?, '{}', 1, 0, 0, 1, 2, 512)
	`, now); err != nil {
		t.Fatalf("insert sync health: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, sha256, state,
			lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		) VALUES
			('sample_ready_asset', 'post_media', 'tweet', 'sample_post', 0,
				'media/sample_post_0.jpg', 'image/jpeg', 512, 'sha-ready', 'ready', '', 0, ?, ?),
			('sample_downloading_asset', 'avatar', 'channel', 'twitter_sample_author', 0,
				'thumbnails/avatars/twitter_sample_author.jpg', 'image/jpeg', 0, '', 'downloading', 'worker-a', ?, ?, ?)
	`, now, now, now-1, now, now); err != nil {
		t.Fatalf("insert asset inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "logs", "android"), 0o755); err != nil {
		t.Fatalf("mkdir android logs: %v", err)
	}
	androidLog := `{"event":"android_sync_metadata_retry","fields":{"label":"latest_generation","error":"Sync HTTP 502 for latest_generation"},"level":"info"}`
	if err := os.WriteFile(filepath.Join(tmp, "logs", "android", "server.log"), []byte(androidLog+"\n"), 0o644); err != nil {
		t.Fatalf("write android log: %v", err)
	}

	report, err := androidSyncStatus(60)
	if err != nil {
		t.Fatalf("androidSyncStatus: %v", err)
	}
	for _, want := range []string{
		"=== Android Sync Status ===",
		"Latest generation:",
		"id: sample_generation",
		"server_missing groups: avatar/channel=1",
		"Recent health reports:",
		"Current asset inventory:",
		"leases: active_downloading=0 expired_downloading=1",
		"Recent Android client sync failures:",
		"metadata_retry latest_generation/http_502=1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("android sync status missing %q:\n%s", want, report)
		}
	}
}

func TestIdentityMediaStatusTracesTweetIdentities(t *testing.T) {
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
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, author_display_name, author_avatar_url,
			body_text, quote_tweet_id, quote_author_handle, quote_author_display_name,
			quote_author_avatar_url, reply_to_handle, published_at, fetched_at
		) VALUES (
			'sample_post', 'sample_source', 'sample_author', 'Sample Author',
			'https://example.invalid/avatar.jpg', 'sample body', 'sample_quote_post',
			'sample_quote', 'Sample Quote', 'https://example.invalid/quote-avatar.jpg',
			'sample_reply', ?, ?
		)
	`, now-1000, now); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (
			channel_id, platform, handle, display_name, avatar_url, banner_url,
			fetched_at, next_retry_at
		) VALUES (
			'twitter_sample_author', 'twitter', 'sample_author', 'Sample Author',
			'https://example.invalid/avatar.jpg', 'https://example.invalid/banner.jpg',
			?, 0
		)
	`, now); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, sha256, state, created_at_ms, updated_at_ms
		) VALUES (
			'sample_author_avatar', 'avatar', 'channel', 'twitter_sample_author', 0,
			'thumbnails/avatars/twitter_sample_author.jpg', 'image/jpeg', 64, 'sha-avatar',
			'ready', ?, ?
		)
	`, now, now); err != nil {
		t.Fatalf("insert avatar asset: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_queue (channel_id, status, priority, added_at, started_at, completed_at)
		VALUES ('twitter_sample_author', 'pending', 5, ?, 0, 0)
	`, now-2000); err != nil {
		t.Fatalf("insert channel queue: %v", err)
	}
	avatarPath := filepath.Join(tmp, "thumbnails", "avatars", "twitter_sample_author.jpg")
	if err := os.MkdirAll(filepath.Dir(avatarPath), 0o755); err != nil {
		t.Fatalf("mkdir avatar dir: %v", err)
	}
	if err := os.WriteFile(avatarPath, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write avatar fixture: %v", err)
	}

	report, err := identityMediaStatus("", "", "", "sample_post", 5)
	if err != nil {
		t.Fatalf("identityMediaStatus: %v", err)
	}
	for _, want := range []string{
		"=== Identity Media Status ===",
		"Feed item sample_post:",
		"Identity [author]:",
		"profile: channel_id=twitter_sample_author",
		"cached files: avatar=true",
		"asset rows:",
		"channel_queue: status=pending",
		"feed presence: rows=1",
		"recent feed rows:",
		"sample_post role=author",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("identity media status missing %q:\n%s", want, report)
		}
	}
}

func TestPipelineStatusIncludesCurrentQueuesAndRetryReadiness(t *testing.T) {
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
		INSERT INTO translation_jobs (
			tweet_id, field, target_lang, source_hash, status, priority, attempts,
			next_attempt_at, last_error_kind, last_error, created_at, updated_at
		) VALUES
			('sample_post', 'body', 'en', 'hash', 'queued', 0, 1, ?, '', '', ?, ?),
			('sample_failed_post', 'body', 'en', 'hash', 'failed', 0, 2, 0,
				'provider_unavailable', 'sample failure', ?, ?)
	`, now-1, now, now, now-5000, now-5000); err != nil {
		t.Fatalf("insert translation jobs: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, size_bytes, sha256, state,
			last_error_kind, last_error, next_attempt_at_ms, lease_owner,
			lease_until_ms, created_at_ms, updated_at_ms
		) VALUES
			('sample_asset_queued', 'post_media', 'tweet', 'sample_post', 0,
				'', '', 0, '', 'queued', '', '', ?, '', 0, ?, ?),
			('sample_asset_expired', 'avatar', 'channel', 'twitter_sample_author', 0,
				'', '', 0, '', 'downloading', '', '', 0, 'worker-a', ?, ?, ?),
			('sample_asset_failed', 'banner', 'channel', 'twitter_sample_author', 0,
				'', '', 0, '', 'failed', 'download_failed', 'sample asset failure', 0, '', 0, ?, ?)
	`, now-1, now, now, now-1, now-20*int64(time.Minute/time.Millisecond), now-20*int64(time.Minute/time.Millisecond), now-5000, now-5000); err != nil {
		t.Fatalf("insert assets: %v", err)
	}

	report, err := pipelineStatus()
	if err != nil {
		t.Fatalf("pipelineStatus: %v", err)
	}
	for _, want := range []string{
		"=== Pipeline Status ===",
		"Translation Jobs (translation_jobs):",
		"ready to claim: 1",
		"error kinds: provider_unavailable=1",
		"Asset Inventory (assets):",
		"leases: active=0 expired=1",
		"stuck active >10m: 1",
		"error kinds: download_failed=1",
		"sample asset failure",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("pipeline status missing %q:\n%s", want, report)
		}
	}
}
