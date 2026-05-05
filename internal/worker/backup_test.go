package worker

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
)

func TestCreateBackupWritesIglooDBAndSkipsStaleSnapshotName(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	confDir := t.TempDir()
	database, err := db.Open(config.DefaultDatabasePath(dataDir), dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.ExecRaw(`INSERT OR REPLACE INTO settings (user_id, key, value) VALUES ('', 'sample', 'ok')`); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "db-snapshot.tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed stale snapshot: %v", err)
	}

	m := NewManager(database, &config.Config{
		DataDir:    dataDir,
		ConfDir:    confDir,
		CookiesDir: filepath.Join(confDir, "cookies"),
	})
	if err := m.createBackup(backupDir); err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(backupDir, backupPrefix+"*.tar.gz"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}
	names := tarEntryNames(t, matches[0])
	if !names[config.DatabaseFilename] {
		t.Fatalf("backup missing %s; entries=%v", config.DatabaseFilename, names)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "db-snapshot.tmp")); err != nil {
		t.Fatalf("stale snapshot should be left untouched: %v", err)
	}
}

func tarEntryNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip backup: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("tar next: %v", err)
		}
		out[strings.TrimSpace(hdr.Name)] = true
	}
	return out
}
