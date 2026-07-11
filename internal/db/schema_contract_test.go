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
