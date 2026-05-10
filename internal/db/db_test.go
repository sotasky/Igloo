package db

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testDBPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/igloo/igloo.db"
}

func testDataDir() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/igloo"
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := testDBPath()
	if _, err := os.Stat(path); err != nil {
		t.Skip("database not found")
	}
	d, err := OpenReadOnly(path, testDataDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
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
	tmpFile.Close()

	d, err := Open(tmpPath, t.TempDir())
	if err != nil {
		os.Remove(tmpPath)
		t.Fatalf("open writable: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		os.Remove(tmpPath)
	})
	return d
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

func TestOpenDropsLegacyChannelCheckInterval(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "igloo.db")
	dataDir := filepath.Join(tmpDir, "data")

	seed, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT UNIQUE NOT NULL,
			source_id TEXT,
			name TEXT NOT NULL,
			url TEXT,
			platform TEXT,
			quality TEXT,
			check_interval INTEGER,
			last_checked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE settings (user_id TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT, PRIMARY KEY (user_id, key))`,
		`INSERT INTO channels (channel_id, name, platform, check_interval) VALUES ('youtube_legacy', 'Legacy', 'youtube', 6)`,
		`INSERT INTO settings (user_id, key, value) VALUES ('', 'youtube_check_interval', '6')`,
		`INSERT INTO settings (user_id, key, value) VALUES ('feed', 'shorts_check_interval', '3')`,
	} {
		if _, err := seed.Exec(stmt); err != nil {
			seed.Close()
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(dbPath, dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	rows, err := d.conn.Query(`PRAGMA table_info(channels)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     sql.NullString
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "check_interval" {
			t.Fatal("channels.check_interval should be dropped")
		}
	}
	var retiredSettings int
	if err := d.conn.QueryRow(`
		SELECT COUNT(*) FROM settings
		WHERE key IN ('youtube_check_interval', 'shorts_check_interval', 'instagram_check_interval')
	`).Scan(&retiredSettings); err != nil {
		t.Fatal(err)
	}
	if retiredSettings != 0 {
		t.Fatalf("retired interval settings = %d, want 0", retiredSettings)
	}
}

func TestOpenReadOnly(t *testing.T) {
	path := testDBPath()
	if _, err := os.Stat(path); err != nil {
		t.Skip("database not found, skipping integration test")
	}

	d, err := OpenReadOnly(path, testDataDir())
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer d.Close()

	var count int
	err = d.conn.QueryRow("SELECT count(*) FROM channels").Scan(&count)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if count == 0 {
		t.Log("warning: channels table is empty")
	}
}

func TestOpenCleansRetiredReadingFeatureState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "igloo.db")
	dataDir := filepath.Join(tmpDir, "data")
	articlesDir := filepath.Join(dataDir, "articles")
	if err := os.MkdirAll(articlesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(articlesDir, "saved.md"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	seed, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE settings (user_id TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT, PRIMARY KEY (user_id, key))`,
		`INSERT INTO settings (user_id, key, value) VALUES ('', 'reading_download_dir', '/tmp/articles')`,
		`INSERT INTO settings (user_id, key, value) VALUES ('', 'starting_page', 'reading')`,
		`INSERT INTO settings (user_id, key, value) VALUES ('', 'shortcuts', '{"reading.download":"b","reading.share":"s","feed.like":"l"}')`,
		`CREATE TABLE reading_preferences (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE saved_articles (story_id TEXT UNIQUE NOT NULL)`,
		`CREATE TABLE reading_articles_cache (url_hash TEXT PRIMARY KEY, category_key TEXT, published_at INTEGER)`,
		`CREATE INDEX idx_reading_cache_cat_pub ON reading_articles_cache(category_key, published_at DESC)`,
	} {
		if _, err := seed.Exec(stmt); err != nil {
			seed.Close()
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(dbPath, dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	for _, table := range []string{"reading_preferences", "saved_articles", "reading_articles_cache"} {
		var count int
		if err := d.conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("lookup table %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("retired table %s still exists", table)
		}
	}

	if got, err := d.GetSetting("starting_page", ""); err != nil || got != "feed" {
		t.Fatalf("starting_page = %q, %v; want feed", got, err)
	}
	if got, err := d.GetSetting("reading_download_dir", "fallback"); err != nil || got != "fallback" {
		t.Fatalf("reading_download_dir = %q, %v; want fallback", got, err)
	}

	rawShortcuts, err := d.GetSetting("shortcuts", "")
	if err != nil {
		t.Fatal(err)
	}
	var shortcuts map[string]string
	if err := json.Unmarshal([]byte(rawShortcuts), &shortcuts); err != nil {
		t.Fatalf("shortcuts JSON: %v", err)
	}
	if _, ok := shortcuts["reading.download"]; ok {
		t.Fatalf("reading.download shortcut survived: %s", rawShortcuts)
	}
	if _, ok := shortcuts["reading.share"]; ok {
		t.Fatalf("reading.share shortcut survived: %s", rawShortcuts)
	}
	if got := shortcuts["feed.like"]; got != "l" {
		t.Fatalf("feed.like = %q; want l", got)
	}

	if _, err := os.Stat(articlesDir); !os.IsNotExist(err) {
		t.Fatalf("articles dir stat = %v; want removed", err)
	}
}
