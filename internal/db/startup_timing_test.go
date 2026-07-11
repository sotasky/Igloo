package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/storage"
)

func TestOpenWithOptionsReportsStartupPhases(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var phases []string
	layout, err := storage.New(tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}

	d, err := OpenWithOptions(
		layout,
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
	t.Cleanup(func() { _ = d.Close() })

	assertPhaseSubsequence(t, phases, []string{
		"db.sql_open",
		"db.ping",
		"db.ensure_schema",
		"db.open_total",
	})
}

func TestOpenWithOptionsDoesNotCreateDatabaseInUnmarkedStateRoot(t *testing.T) {
	stateRoot := t.TempDir()
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithOptions(layout, OpenOptions{}); err == nil {
		t.Fatal("OpenWithOptions succeeded without the state marker")
	}
	if _, err := os.Stat(layout.DatabasePath()); !os.IsNotExist(err) {
		t.Fatalf("database was created in the unmarked state root: %v", err)
	}
}

func TestEnsureSchemaWithOptionsReportsSubPhases(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "igloo.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

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
		"schema.android_sync_revision_triggers",
		"schema.indexes",
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
