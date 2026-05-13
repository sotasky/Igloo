package queryaudit

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	igloodb "github.com/screwys/igloo/internal/db"

	_ "modernc.org/sqlite"
)

func TestRunReportsHotPathPlans(t *testing.T) {
	dbPath := createQueryAuditFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath, "-limit", "5"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"db: " + dbPath,
		"query_audit:",
		"feed_snapshot_page:",
		"videos_shorts_page:",
		"android_sync_media_videos:",
		"asset_repair_claim_candidates:",
		"profile_refresh_candidate:",
		"channel_search:",
		"video_search:",
		"mutation_delta_candidates:",
		"elapsed_ms:",
		"rows:",
		"plan:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReadReportCollectsPlans(t *testing.T) {
	dbPath := createQueryAuditFixture(t)

	report, err := ReadReport(dbPath, Options{Limit: 5, Username: "alice", Search: "Sample"})
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if report.DBPath != dbPath {
		t.Fatalf("DBPath = %q, want %q", report.DBPath, dbPath)
	}
	if len(report.Probes) < 7 {
		t.Fatalf("probe count = %d, want at least 7", len(report.Probes))
	}
	for _, probe := range report.Probes {
		if probe.Name == "" {
			t.Fatalf("probe without name: %+v", probe)
		}
		if probe.Error != "" {
			t.Fatalf("probe %s errored: %s", probe.Name, probe.Error)
		}
		if len(probe.Plan) == 0 {
			t.Fatalf("probe %s missing query plan", probe.Name)
		}
	}
}

func TestAndroidSyncMediaVideoProbeUsesIndexedPlan(t *testing.T) {
	dbPath := createQueryAuditProductionFixture(t)

	report, err := ReadReport(dbPath, Options{
		Limit:    5,
		Username: "alice",
		Probe:    "android_sync_media_videos",
		NowMs:    2_000,
	})
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if len(report.Probes) != 1 {
		t.Fatalf("probe count = %d, want 1", len(report.Probes))
	}
	probe := report.Probes[0]
	if probe.Error != "" {
		t.Fatalf("probe errored: %s", probe.Error)
	}
	if planHas(probe.Plan, "SCAN v") {
		t.Fatalf("android sync media-video plan scans videos table:\n%s", strings.Join(probe.Plan, "\n"))
	}
	if !planHas(probe.Plan, "idx_videos_channel_published") {
		t.Fatalf("android sync media-video plan missing videos channel index:\n%s", strings.Join(probe.Plan, "\n"))
	}
}

func TestParseOptionsRejectsUnknownProbe(t *testing.T) {
	if _, err := parseOptions([]string{"-probe", "missing"}); err == nil {
		t.Fatal("parseOptions accepted unknown probe")
	}
}

func planHas(plan []string, needle string) bool {
	for _, detail := range plan {
		if strings.Contains(detail, needle) {
			return true
		}
	}
	return false
}

func createQueryAuditProductionFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "igloo.db")
	d, err := igloodb.Open(dbPath, tmp)
	if err != nil {
		t.Fatalf("open production fixture db: %v", err)
	}
	defer d.Close()

	stmts := []string{
		`INSERT INTO channels (channel_id, source_id, name, platform)
		 VALUES ('tiktok_sample_channel', 'sample_channel', 'Sample Channel', 'tiktok')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at)
		 VALUES ('', 'tiktok_sample_channel', 1000)`,
		`INSERT INTO videos (video_id, channel_id, title, file_path, published_at)
		 VALUES ('sample_video_1', 'tiktok_sample_channel', 'Sample Video', 'videos/sample.mp4', 1500)`,
	}
	for _, stmt := range stmts {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("exec production fixture statement %q: %v", stmt, err)
		}
	}
	return dbPath
}

func createQueryAuditFixture(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igloo.db")
	conn, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer conn.Close()
	stmts := []string{
		`CREATE TABLE feed_items (
			tweet_id TEXT PRIMARY KEY,
			source_handle TEXT,
			author_handle TEXT NOT NULL,
			body_text TEXT,
			published_at INTEGER NOT NULL DEFAULT 0,
			fetched_at INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT,
			canonical_tweet_id TEXT,
			media_json TEXT
		)`,
		`CREATE TABLE feed_rank_snapshot (
			username TEXT NOT NULL,
			tweet_id TEXT NOT NULL,
			rank_position INTEGER NOT NULL,
			final_score REAL NOT NULL DEFAULT 0,
			computed_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (username, tweet_id)
		)`,
		`CREATE INDEX idx_feed_rank_snapshot_pos ON feed_rank_snapshot(username, rank_position)`,
		`CREATE TABLE feed_seen (username TEXT NOT NULL, tweet_id TEXT NOT NULL, seen_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (username, tweet_id))`,
		`CREATE TABLE videos (
			video_id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			title TEXT,
			file_path TEXT,
			source_kind TEXT DEFAULT '',
			published_at INTEGER NOT NULL DEFAULT 0,
			is_temp INTEGER DEFAULT 0
		)`,
		`CREATE INDEX idx_videos_channel ON videos(channel_id)`,
		`CREATE TABLE channel_follows (user_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL, followed_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (user_id, channel_id))`,
		`CREATE TABLE bookmarks (user_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL, bookmarked_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (user_id, video_id))`,
		`CREATE TABLE feed_likes (username TEXT NOT NULL, tweet_id TEXT NOT NULL, liked_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (username, tweet_id))`,
		`CREATE TABLE assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL UNIQUE,
			asset_kind TEXT NOT NULL,
			owner_kind TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			state TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms INTEGER NOT NULL DEFAULT 0,
			lease_until_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_assets_repair ON assets(state, next_attempt_at_ms, lease_until_ms, attempts, updated_at_ms, id)`,
		`CREATE TABLE channels (
			channel_id TEXT PRIMARY KEY,
			source_id TEXT,
			name TEXT NOT NULL,
			platform TEXT,
			sync_seq INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE channel_profiles (
			channel_id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			handle TEXT,
			display_name TEXT,
			avatar_url TEXT,
			banner_url TEXT,
			fetched_at INTEGER NOT NULL DEFAULT 0,
			next_retry_at INTEGER NOT NULL DEFAULT 0,
			tombstone INTEGER DEFAULT 0
		)`,
		`CREATE INDEX idx_channel_profiles_refresh ON channel_profiles(tombstone, fetched_at) WHERE tombstone = 0`,
		`CREATE TABLE video_repost_sources (video_id TEXT NOT NULL, reposter_channel_id TEXT NOT NULL, reposted_at_ms INTEGER NOT NULL DEFAULT 0, first_seen_at_ms INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (video_id, reposter_channel_id))`,
		`CREATE TABLE channel_stars (user_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL, starred_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (user_id, channel_id))`,
		`CREATE VIRTUAL TABLE search_channels_fts USING fts5(
			channel_id_pk UNINDEXED,
			name,
			source_id,
			display_name,
			handle,
			tokenize = 'unicode61'
		)`,
		`CREATE VIRTUAL TABLE search_videos_fts USING fts5(
			video_id_pk UNINDEXED,
			title,
			dearrow_title,
			dearrow_title_casual,
			channel_name,
			tokenize = 'unicode61'
		)`,
		`CREATE TABLE sync_changes (
			version INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			item_id TEXT NOT NULL,
			value TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_sync_changes_version ON sync_changes(version)`,
		`INSERT INTO feed_items (tweet_id, source_handle, author_handle, body_text, published_at, fetched_at)
		 VALUES ('sample_tweet_1', 'sample_author', 'sample_author', 'body', 1000, 1000)`,
		`INSERT INTO feed_rank_snapshot (username, tweet_id, rank_position, final_score, computed_at)
		 VALUES ('alice', 'sample_tweet_1', 1, 1.0, 1000)`,
		`INSERT INTO channels (channel_id, source_id, name, platform)
		 VALUES ('tiktok_sample_channel', 'sample_channel', 'Sample Channel', 'tiktok')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at)
		 VALUES ('', 'tiktok_sample_channel', 1000)`,
		`INSERT INTO videos (video_id, channel_id, title, file_path, published_at)
		 VALUES ('sample_video_1', 'tiktok_sample_channel', 'Sample Video', 'videos/sample.mp4', 1000)`,
		`INSERT INTO assets (asset_id, asset_kind, owner_kind, owner_id, state, updated_at_ms)
		 VALUES ('sample_asset_1', 'post_media', 'video', 'sample_video_1', 'queued', 1000)`,
		`INSERT INTO channel_profiles (channel_id, platform, handle, display_name, fetched_at)
		 VALUES ('tiktok_sample_channel', 'tiktok', 'sample_channel', 'Sample Channel', 0)`,
		`INSERT INTO search_channels_fts(rowid, channel_id_pk, name, source_id, display_name, handle)
		 VALUES (1, 'tiktok_sample_channel', 'Sample Channel', 'sample_channel', 'Sample Channel', 'sample_channel')`,
		`INSERT INTO search_videos_fts(rowid, video_id_pk, title, dearrow_title, dearrow_title_casual, channel_name)
		 VALUES (1, 'sample_video_1', 'Sample Video', '', '', 'Sample Channel')`,
		`INSERT INTO sync_changes (type, item_id, value, created_at)
		 VALUES ('follow', 'tiktok_sample_channel', '{}', 1000)`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("exec fixture statement %q: %v", stmt, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat fixture db: %v", err)
	}
	return dbPath
}
