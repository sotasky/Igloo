package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func testDBPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/igloo/igloo.db"
}

func testDataDir() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/igloo"
}

func markDBTestStateRoot(t *testing.T, stateRoot string) {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path, stateRoot := openReadOnlyFixtureDB(t)
	d, err := OpenReadOnly(path, stateRoot)
	if err != nil {
		t.Fatalf("open read-only fixture: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func openReadOnlyFixtureDB(t *testing.T) (string, string) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "igloo-readonly-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()

	stateRoot := t.TempDir()
	markDBTestStateRoot(t, stateRoot)
	d, err := OpenPath(tmpPath, stateRoot)
	if err != nil {
		_ = os.Remove(tmpPath)
		t.Fatalf("open writable: %v", err)
	}
	seedReadOnlyFixtureDB(t, d)
	if err := d.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(tmpPath)
	})
	return tmpPath, stateRoot
}

// openWritableTestDB creates a fresh temp DB with schema for write tests.
// Does not copy production data — tests write their own fixtures.
func openWritableTestDB(t *testing.T) *DB {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "igloo-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()

	stateRoot := t.TempDir()
	markDBTestStateRoot(t, stateRoot)
	d, err := OpenPath(tmpPath, stateRoot)
	if err != nil {
		_ = os.Remove(tmpPath)
		t.Fatalf("open writable: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		_ = os.Remove(tmpPath)
	})
	return d
}

// openFreshTestDB creates a brand-new database at the canonical state-root
// location used by tests that need to inspect the database path directly.
func openFreshTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	markDBTestStateRoot(t, tmpDir)
	d, err := OpenPath(filepath.Join(tmpDir, "test.db"), tmpDir)
	if err != nil {
		t.Fatalf("Open fresh DB: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedTestChannel(t *testing.T, d *DB, channelID string) {
	t.Helper()
	if err := d.ExecRaw(`
		INSERT OR IGNORE INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, ?, 'Fixture Channel', '', 'youtube', 1)
	`, channelID, channelID); err != nil {
		t.Fatalf("seed channel %s: %v", channelID, err)
	}
}

func seedTestFollowedChannel(t *testing.T, d *DB, channelID string) {
	t.Helper()
	seedTestChannel(t, d, channelID)
	if err := d.ExecRaw(`
		INSERT OR IGNORE INTO channel_follows (channel_id, followed_at)
		VALUES (?, 1)
	`, channelID); err != nil {
		t.Fatalf("seed channel follow %s: %v", channelID, err)
	}
}

func seedTestVideo(t *testing.T, d *DB, videoID, channelID string) {
	t.Helper()
	seedTestChannel(t, d, channelID)
	if err := d.ExecRaw(`
		INSERT OR IGNORE INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES (?, ?, 'youtube_video', 'Fixture Video', 120, 1)
	`, videoID, channelID); err != nil {
		t.Fatalf("seed video %s: %v", videoID, err)
	}
	asset := normalizeAsset(Asset{
		AssetID: "fixture-stream:" + videoID, AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: videoID,
		FilePath: "media/youtube/fixture.mp4", ContentType: "video/mp4",
		SizeBytes: 1, SHA256: "fixture", FileMtimeNs: 1, State: AssetStateReady,
	}, 1)
	if err := d.WithWrite(func(tx *sql.Tx) error { return upsertAssetTx(tx, asset) }); err != nil {
		t.Fatalf("seed video asset %s: %v", videoID, err)
	}
}

func seedReadOnlyFixtureDB(t *testing.T, d *DB) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	const (
		channelID = "youtube_fixture_channel"
		videoID   = "youtube_fixture_video"
		tweetID   = "twitter_fixture_tweet"
	)
	seedTestFollowedChannel(t, d, channelID)
	seedTestVideo(t, d, videoID, channelID)
	if err := d.ExecRaw(`
		INSERT OR IGNORE INTO video_comments (
			video_id, comment_id, author_name, author_id, text, like_count, published_at
		) VALUES (?, 'fixture_comment', 'Fixture Commenter', 'fixture_author', 'Fixture comment text', 1, 1)
	`, videoID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:        tweetID,
		AuthorHandle:   "fixture_author",
		BodyText:       "fixture feed item",
		PublishedAt:    &now,
		FetchedAt:      now,
		ContentHash:    "fixture_feed_hash",
		CanonicalURL:   "https://x.com/fixture_author/status/" + tweetID,
		MediaJSON:      `[]`,
		QuoteMediaJSON: `[]`,
	}}); err != nil {
		t.Fatalf("seed feed item: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT OR IGNORE INTO feed_likes (tweet_id, liked_at)
		VALUES (?, 1)
	`, tweetID); err != nil {
		t.Fatalf("seed feed like: %v", err)
	}
}

func TestOpen(t *testing.T) {
	d := openWritableTestDB(t)

	tables := []string{"channels", "videos", "feed_items", "settings"}
	for _, table := range tables {
		var name string
		err := d.conn.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestOpenReadOnly(t *testing.T) {
	path, dataDir := openReadOnlyFixtureDB(t)

	d, err := OpenReadOnly(path, dataDir)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	var count int
	err = d.conn.QueryRow("SELECT count(*) FROM channels").Scan(&count)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if count == 0 {
		t.Log("warning: channels table is empty")
	}
}
