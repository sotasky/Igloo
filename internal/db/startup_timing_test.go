package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenWithOptionsReportsStartupPhases(t *testing.T) {
	tmpDir := t.TempDir()
	var phases []string

	d, err := OpenWithOptions(
		filepath.Join(tmpDir, "igloo.db"),
		filepath.Join(tmpDir, "data"),
		OpenOptions{
			Phase: func(name string, elapsed time.Duration) {
				if elapsed < 0 {
					t.Fatalf("phase %s elapsed = %s, want non-negative", name, elapsed)
				}
				phases = append(phases, name)
			},
		},
	)
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	assertPhaseSubsequence(t, phases, []string{
		"db.sql_open",
		"db.ping",
		"db.ensure_schema",
		"db.cleanup_retired_reading",
		"db.init_sync_seq",
		"db.repair_video_media_shapes",
		"db.open_total",
	})
}

func TestEnsureSchemaWithOptionsReportsSubPhases(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "igloo.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	var phases []string
	if err := EnsureSchemaWithOptions(conn, EnsureSchemaOptions{
		Phase: func(name string, elapsed time.Duration) {
			if elapsed < 0 {
				t.Fatalf("phase %s elapsed = %s, want non-negative", name, elapsed)
			}
			phases = append(phases, name)
		},
	}); err != nil {
		t.Fatalf("EnsureSchemaWithOptions: %v", err)
	}

	assertPhaseSubsequence(t, phases, []string{
		"schema.create_tables",
		"schema.add_columns",
		"schema.drop_channel_check_interval",
		"schema.indexes",
		"schema.android_sync_cleanup",
		"schema.legacy_table_repairs",
		"schema.sync_seq_backfill",
		"schema.feed_media_legacy_fixes",
		"schema.total",
	})
}

func assertPhaseSubsequence(t *testing.T, got, want []string) {
	t.Helper()
	next := 0
	for _, phase := range got {
		if next < len(want) && phase == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("phases = %v, want subsequence %v", got, want)
	}
}
