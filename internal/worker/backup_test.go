package worker

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
)

func TestCreateBackupWritesIglooDBAndSkipsStaleSnapshotName(t *testing.T) {
	fx := newBackupFixture(t)

	cfg := testCfg(fx.dataDir)
	cfg.ConfDir = fx.confDir
	cfg.CookiesDir = filepath.Join(fx.confDir, "cookies")
	cfg.AuthUsersPath = filepath.Join(fx.confDir, "auth_users.json")
	m := NewManager(fx.database, cfg)
	if err := m.createBackup(fx.backupDir); err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(fx.backupDir, backupPrefix+"*.zip"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}
	names := zipEntryNames(t, matches[0])
	if !names[config.DatabaseFilename] {
		t.Fatalf("backup missing %s; entries=%v", config.DatabaseFilename, names)
	}
	for _, name := range []string{"config/config.json", "config/nginx.conf", "config/server.crt"} {
		if !names[name] {
			t.Fatalf("backup missing safe config entry %s; entries=%v", name, names)
		}
	}
	for name := range names {
		if strings.HasPrefix(name, "assets/") {
			t.Fatalf("backup included media payload %s; entries=%v", name, names)
		}
	}
	for _, name := range []string{
		"config/auth_secret",
		"config/auth_users.json",
		"config/cookies/twitter_cookies.txt",
		"config/custom.env",
		"config/nested/refresh_token.txt",
		"config/server.key",
	} {
		if names[name] {
			t.Fatalf("backup included sensitive config entry %s; entries=%v", name, names)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.backupDir, "db-snapshot.tmp")); err != nil {
		t.Fatalf("stale snapshot should be left untouched: %v", err)
	}
}

func TestPruneBackupsUsesConfiguredKeepCount(t *testing.T) {
	backupDir := t.TempDir()
	for _, stamp := range []string{"20260101-000001", "20260102-000001", "20260103-000001", "20260104-000001"} {
		path := filepath.Join(backupDir, backupPrefix+stamp+".zip")
		if err := os.WriteFile(path, []byte("backup"), 0o644); err != nil {
			t.Fatalf("write backup fixture %s: %v", stamp, err)
		}
	}

	(&Manager{}).pruneBackups(backupDir, 2)

	matches, err := filepath.Glob(filepath.Join(backupDir, backupPrefix+"*.zip"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	got := make([]string, 0, len(matches))
	for _, path := range matches {
		got = append(got, filepath.Base(path))
	}
	want := strings.Join([]string{
		backupPrefix + "20260103-000001.zip",
		backupPrefix + "20260104-000001.zip",
	}, ",")
	if strings.Join(got, ",") != want {
		t.Fatalf("remaining backups = %v, want %s", got, want)
	}
}

func TestBackupKeepCountClampsSetting(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.OpenPath(filepath.Join(dataDir, config.DatabaseFilename), dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = database.Close()
	}()
	m := NewManager(database, testCfg(dataDir))

	if got := m.backupKeepCount(); got != 5 {
		t.Fatalf("default keep count = %d, want 5", got)
	}
	if err := database.SetSetting("backup_keep_count", "0"); err != nil {
		t.Fatalf("set low keep count: %v", err)
	}
	if got := m.backupKeepCount(); got != 1 {
		t.Fatalf("low keep count = %d, want 1", got)
	}
	if err := database.SetSetting("backup_keep_count", "9"); err != nil {
		t.Fatalf("set high keep count: %v", err)
	}
	if got := m.backupKeepCount(); got != 5 {
		t.Fatalf("high keep count = %d, want 5", got)
	}
	if err := database.SetSetting("backup_keep_count", "3"); err != nil {
		t.Fatalf("set keep count: %v", err)
	}
	if got := m.backupKeepCount(); got != 3 {
		t.Fatalf("keep count = %d, want 3", got)
	}
}

func TestCreateBackupRejectsRelativeDir(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.OpenPath(filepath.Join(dataDir, config.DatabaseFilename), dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = database.Close()
	}()

	m := NewManager(database, testCfg(dataDir))
	if err := m.createBackup(filepath.Join("var", "mnt", "external_drive")); err == nil {
		t.Fatal("createBackup accepted a relative backup dir")
	}
	if _, err := os.Stat(filepath.Join("var", "mnt", "external_drive")); !os.IsNotExist(err) {
		t.Fatalf("relative backup dir was created or stat failed: %v", err)
	}
}

type backupFixture struct {
	dataDir   string
	backupDir string
	confDir   string
	database  *db.DB
}

func newBackupFixture(t *testing.T) backupFixture {
	t.Helper()

	dataDir := t.TempDir()
	backupDir := t.TempDir()
	confDir := t.TempDir()
	configFiles := map[string]string{
		"config.json":                 `{"enabled_platforms":["youtube"]}` + "\n",
		"auth_secret":                 "secret-key",
		"auth_users.json":             `{"sample_user":{"role":"admin"}}` + "\n",
		"cookies/twitter_cookies.txt": "cookie-data",
		"custom.env":                  "CUSTOM_SECRET=example\n",
		"nested/refresh_token.txt":    "token-data",
		"nginx.conf":                  "pid /run/nginx.pid;\n",
		"server.key":                  "tls-key",
		"server.crt":                  "tls-cert",
	}
	for rel, content := range configFiles {
		path := filepath.Join(confDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir config fixture %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write config fixture %s: %v", rel, err)
		}
	}

	database, err := db.OpenPath(filepath.Join(dataDir, config.DatabaseFilename), dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	if err := database.ExecRaw(`INSERT OR REPLACE INTO settings (key, value) VALUES ('sample', 'ok')`); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	if err := os.WriteFile(filepath.Join(backupDir, "db-snapshot.tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed stale snapshot: %v", err)
	}

	return backupFixture{
		dataDir:   dataDir,
		backupDir: backupDir,
		confDir:   confDir,
		database:  database,
	}
}

func zipEntryNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open backup zip: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()
	out := map[string]bool{}
	for _, f := range r.File {
		out[strings.TrimSpace(f.Name)] = true
	}
	return out
}
