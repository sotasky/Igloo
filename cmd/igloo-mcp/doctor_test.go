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
	defer d.Close()

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
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name, avatar_url, banner_url)
		VALUES ('twitter_sample_profile', 'twitter', 'sample_profile', 'Doctor', 'https://cdn.example.invalid/avatar.jpg', 'https://cdn.example.invalid/banner.jpg')
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
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

	report, err := doctorStatus()
	if err != nil {
		t.Fatalf("doctorStatus: %v", err)
	}
	for _, want := range []string{
		"=== Igloo Doctor ===",
		"Database files:",
		"Android sync:",
		"Queue counts:",
		"Profile/media readiness:",
		"Downloader failures:",
		"Recent high-signal log errors:",
		"android-sync-doctor",
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
