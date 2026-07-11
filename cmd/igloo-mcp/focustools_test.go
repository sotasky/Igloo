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

	d, err := igloodb.OpenPath(filepath.Join(tmp, "igloo.db"), tmp)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO android_sync_health_reports (
			cursor, reported_at_ms, payload_json, verified_assets,
			pending_assets, missing_assets, total_assets, verified_bytes
		) VALUES ('sample_cursor', ?, '{}', 1, 0, 1, 2, 512)
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
				'thumbnails/avatars/twitter_sample_author.jpg', 'image/jpeg', 0, '', 'downloading', 'worker-a', ?, ?, ?),
			('sample_missing_asset', 'avatar', 'channel', 'twitter_sample_author', 1,
				'', '', 0, '', 'server_missing', '', 0, ?, ?)
	`, now, now, now-1, now, now, now, now); err != nil {
		t.Fatalf("insert asset inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "logs", "android"), 0o755); err != nil {
		t.Fatalf("mkdir android logs: %v", err)
	}
	androidLog := `{"event":"android_sync_metadata_retry","fields":{"label":"changes","attempt":1},"level":"info"}`
	if err := os.WriteFile(filepath.Join(tmp, "logs", "android", "server.log"), []byte(androidLog+"\n"), 0o644); err != nil {
		t.Fatalf("write android log: %v", err)
	}

	report, err := androidSyncStatus(60)
	if err != nil {
		t.Fatalf("androidSyncStatus: %v", err)
	}
	for _, want := range []string{
		"=== Android Sync Status ===",
		"Convergence stream:",
		"compact_heads=",
		"Recent cursor health reports:",
		"Current asset inventory:",
		"leases: active_downloading=0 expired_downloading=1",
		"Canonical missing assets:",
		"avatar/channel=1",
		"Recent Android client sync failures:",
		"metadata_retry changes=1",
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

	d, err := igloodb.OpenPath(filepath.Join(tmp, "igloo.db"), tmp)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id,
			body_text, quote_tweet_id, quote_channel_id,
			reply_channel_id, published_at, fetched_at
		) VALUES (
			'sample_post', 'twitter_sample_source', 'twitter_sample_author',
			'sample body', 'sample_quote_post', 'twitter_sample_quote',
			'twitter_sample_reply', ?, ?
		)
	`, now-1000, now); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (
			channel_id, platform, handle, display_name, fetched_at
		) VALUES (
			'twitter_sample_author', 'twitter', 'sample_author', 'Sample Author',
			?
		)
	`, now); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO profile_jobs (
			channel_id, requested_revision, completed_revision, requested_at_ms,
			attempts, next_attempt_at_ms, last_error, updated_at_ms
		) VALUES ('twitter_sample_author', 2, 1, ?, 1, ?, 'sample retry', ?)
	`, now-2000, now+1000, now); err != nil {
		t.Fatalf("insert profile job: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			source_url, file_path, content_type, size_bytes, sha256, state, created_at_ms, updated_at_ms
		) VALUES (
			'sample_author_avatar', 'avatar', 'channel', 'twitter_sample_author', 0,
			'https://example.invalid/avatar.jpg', 'thumbnails/avatars/twitter_sample_author.jpg', 'image/jpeg', 64, 'sha-avatar',
			'ready', ?, ?
		)
	`, now, now); err != nil {
		t.Fatalf("insert avatar asset: %v", err)
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
		"profile_job: requested=2 completed=1 attempts=1",
		"profile assets: avatar=ready banner=-",
		"avatar: file=thumbnails/avatars/twitter_sample_author.jpg source=true",
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

	d, err := igloodb.OpenPath(filepath.Join(tmp, "igloo.db"), tmp)
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
