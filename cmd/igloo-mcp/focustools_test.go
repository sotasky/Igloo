package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	igloodb "github.com/screwys/igloo/internal/db"
)

func insertMCPTestAsset(t *testing.T, d *igloodb.DB, asset igloodb.Asset, state string, nowMs, leaseUntilMs int64, leaseOwner string) {
	t.Helper()
	if asset.SourceURL == "" {
		asset.SourceURL = "https://example.test/" + asset.AssetID
	}
	if err := d.DeclareAsset(asset, nowMs); err != nil {
		t.Fatalf("declare test asset: %v", err)
	}
	published := 0
	if state == igloodb.AssetStateReady {
		published = 1
		if asset.FilePath == "" {
			asset.FilePath = "media/" + asset.AssetID
		}
		if asset.ContentType == "" {
			asset.ContentType = "image/jpeg"
		}
		if asset.SizeBytes == 0 {
			asset.SizeBytes = 1
		}
	}
	if err := d.ExecRaw(`
		UPDATE media_objects
		SET published_revision = CASE WHEN ? != 0 THEN desired_revision ELSE 0 END,
		    published_source_url = CASE WHEN ? != 0 THEN source_url ELSE '' END,
		    file_path = ?, content_type = ?, size_bytes = ?, sha256 = ?, file_mtime_ns = ?,
		    job_state = ?, lease_owner = ?, lease_until_ms = ?, updated_at_ms = ?
		WHERE object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ?)
	`, published, published, asset.FilePath, asset.ContentType, asset.SizeBytes, asset.SHA256, nowMs,
		state, leaseOwner, leaseUntilMs, nowMs, asset.AssetID); err != nil {
		t.Fatalf("set test asset state: %v", err)
	}
}

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
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_ready_asset", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post", FilePath: "media/sample_post_0.jpg", ContentType: "image/jpeg", SizeBytes: 512, SHA256: "sha-ready"}, igloodb.AssetStateReady, now, 0, "")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_downloading_asset", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_author", FilePath: "thumbnails/avatars/twitter_sample_author.jpg", ContentType: "image/jpeg"}, igloodb.AssetStateDownloading, now, now-1, "worker-a")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_missing_asset", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_author", MediaIndex: 1}, igloodb.AssetStateServerMissing, now, 0, "")
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
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_author_avatar", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_author", SourceURL: "https://example.invalid/avatar.jpg", FilePath: "thumbnails/avatars/twitter_sample_author.jpg", ContentType: "image/jpeg", SizeBytes: 64, SHA256: "sha-avatar"}, igloodb.AssetStateReady, now, 0, "")
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
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_asset_queued", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post"}, igloodb.AssetStateQueued, now, 0, "")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_asset_expired", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_author"}, igloodb.AssetStateDownloading, now-20*int64(time.Minute/time.Millisecond), now-1, "worker-a")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_asset_failed", AssetKind: "banner", OwnerKind: "channel", OwnerID: "twitter_sample_author"}, igloodb.AssetStateFailed, now-5000, 0, "")
	if err := d.ExecRaw(`UPDATE media_objects SET last_error_kind='download_failed', last_error='sample asset failure' WHERE object_id=(SELECT desired_object_id FROM assets WHERE asset_id='sample_asset_failed')`); err != nil {
		t.Fatalf("set failed asset error: %v", err)
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
		"Media Objects (media_objects):",
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
