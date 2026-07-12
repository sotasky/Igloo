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
	if err := os.WriteFile(filepath.Join(tmp, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
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
		) VALUES ('sample-cursor-doctor', 1, '{}', 1, 0, 1, 2, 1)
	`); err != nil {
		t.Fatalf("insert sync health: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name)
		VALUES ('twitter_sample_profile', 'twitter', 'sample_profile', 'Doctor')
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO profile_jobs (
			channel_id, requested_revision, completed_revision, requested_at_ms,
			attempts, next_attempt_at_ms, last_error, updated_at_ms
		) VALUES ('twitter_sample_profile', 2, 1, ?, 1, ?, 'sample failure', ?)
	`, now, now+1000, now); err != nil {
		t.Fatalf("insert profile job: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'youtube_video', 'Doctor Video', ?)
	`, now); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_post_asset", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post", FilePath: "media/sample_post_0.jpg", ContentType: "image/jpeg", SizeBytes: 10, SHA256: "sha"}, igloodb.AssetStateReady, now, 0, "")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_missing_asset", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_profile"}, igloodb.AssetStateServerMissing, now, 0, "")
	insertMCPTestAsset(t, d, igloodb.Asset{AssetID: "sample_downloading_asset", AssetKind: "post_thumbnail", OwnerKind: "tweet", OwnerID: "sample_post", FilePath: "thumbnails/generated/sample_post.jpg", ContentType: "image/jpeg"}, igloodb.AssetStateDownloading, now, now-1, "worker-a")
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
		`{"event":"android_sync_metadata_retry","fields":{"label":"bootstrap","attempt":1},"level":"info"}`,
		`{"event":"android_sync_metadata_retry","fields":{"label":"changes","attempt":2},"level":"info"}`,
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
		"Storage layout:",
		"media_ready: true",
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
		"channel_profiles: total=1 tombstones=0",
		"profile_jobs: pending=1 leased=0 failed=1",
		"Asset inventory:",
		"inventory states: downloading=1, ready=1",
		"asset leases: active_downloading=0 expired_downloading=1",
		"avatar assets: server_missing=1",
		"banner assets: empty=0",
		"post_media:          ready=1",
		"video_stream:        empty=0",
		"post_thumbnail:      downloading=1",
		"dearrow_thumbnail:   empty=0",
		"avatar:              server_missing=1",
		"banner:              empty=0",
		"preview_track_json:  empty=0",
		"preview_sprite:      empty=0",
		"Downloader failures:",
		"Recent high-signal log errors:",
		"Android sync client failures:",
		"metadata_retry bootstrap=1, changes=1",
		"compact_heads=",
		"sample-cursor-doctor",
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

func TestDoctorStorageLayoutReportsUnavailableExternalRoot(t *testing.T) {
	base := t.TempDir()
	mediaRoot := filepath.Join(base, "media")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IGLOO_DATA_DIR", filepath.Join(base, "state"))
	t.Setenv("IGLOO_MEDIA_DIR", mediaRoot)
	var report strings.Builder
	writeDoctorStorageLayout(&report)
	if !strings.Contains(report.String(), "media_ready: false") || !strings.Contains(report.String(), ".igloo-media-root") {
		t.Fatalf("storage report did not expose missing marker:\n%s", report.String())
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
