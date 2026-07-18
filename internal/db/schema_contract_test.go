package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenRejectsExistingSchemaDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "igloo.db")
	store, err := OpenPath(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`ALTER TABLE channel_profiles ADD COLUMN retired_retry_state INTEGER NOT NULL DEFAULT 0`); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenPath(path, root)
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "database schema does not match the current contract") {
		t.Fatalf("OpenPath schema drift error = %v", err)
	}
}

func TestOpenMigratesKnownSchemaChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "igloo.db")
	store, err := OpenPath(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ExecRaw(`INSERT INTO videos (video_id, channel_id, owner_kind) VALUES ('sample_video', 'sample_channel', 'youtube_video')`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`ALTER TABLE videos DROP COLUMN is_temp`); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DROP TABLE schema_migrations`); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenPath(path, root)
	if err != nil {
		t.Fatalf("OpenPath legacy schema: %v", err)
	}

	var isTemp int
	if err := store.QueryRow(`SELECT is_temp FROM videos WHERE video_id = 'sample_video'`).Scan(&isTemp); err != nil {
		t.Fatalf("read migrated column: %v", err)
	}
	if isTemp != 0 {
		t.Fatalf("migrated is_temp = %d, want 0", isTemp)
	}
	var migrations int
	if err := store.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '20260718_add_videos_is_temp'`).Scan(&migrations); err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if migrations != 1 {
		t.Fatalf("migration ledger entries = %d, want 1", migrations)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenPath(path, root)
	if err != nil {
		t.Fatalf("OpenPath migrated schema: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '20260718_add_videos_is_temp'`).Scan(&migrations); err != nil {
		t.Fatalf("read migration ledger after reopen: %v", err)
	}
	if migrations != 1 {
		t.Fatalf("migration ledger entries after reopen = %d, want 1", migrations)
	}
}

func TestEnsureSchemaCanRunTwice(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := EnsureSchema(conn); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(conn); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
}
