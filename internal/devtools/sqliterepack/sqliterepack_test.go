package sqliterepack

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDryRunReportsSQLiteStorage(t *testing.T) {
	dbPath := createRepackFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"db: " + dbPath,
		"page_size:",
		"pages:",
		"reclaimable_freelist:",
		"compact_estimate:",
		"dry_run: true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestVacuumIntoCreatesCompactCopy(t *testing.T) {
	dbPath := createRepackFixture(t)
	outPath := filepath.Join(t.TempDir(), "compact.db")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath, "-vacuum-into", outPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	if info, err := os.Stat(outPath); err != nil {
		t.Fatalf("stat compact db: %v", err)
	} else if info.Size() == 0 {
		t.Fatalf("compact db is empty")
	}
	out := stdout.String()
	if !strings.Contains(out, "vacuum_into: "+outPath) {
		t.Fatalf("output missing vacuum path:\n%s", out)
	}
	if !strings.Contains(out, "output_size:") {
		t.Fatalf("output missing compact size:\n%s", out)
	}
}

func TestVacuumIntoRefusesExistingOutput(t *testing.T) {
	dbPath := createRepackFixture(t)
	outPath := filepath.Join(t.TempDir(), "compact.db")
	if err := os.WriteFile(outPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath, "-vacuum-into", outPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit=%d want 1 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "output already exists") {
		t.Fatalf("stderr missing existing-output error:\n%s", stderr.String())
	}
}

func createRepackFixture(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igloo.db")
	conn, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	if _, err := conn.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, payload TEXT)`); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin fixture insert: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO items (payload) VALUES (?)`)
	if err != nil {
		t.Fatalf("prepare fixture insert: %v", err)
	}
	for i := 0; i < 512; i++ {
		if _, err := stmt.Exec(strings.Repeat("x", 512)); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			t.Fatalf("insert fixture row: %v", err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close fixture stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture insert: %v", err)
	}
	if _, err := conn.Exec(`DELETE FROM items WHERE id <= 256`); err != nil {
		t.Fatalf("delete fixture rows: %v", err)
	}
	return dbPath
}
