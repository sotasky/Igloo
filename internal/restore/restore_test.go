package restore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/config"
)

func buildTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Size:     int64(len(body)),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestStageTarballRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	confDir := t.TempDir()

	tarBytes := buildTarball(t, map[string]string{
		"igloo.db":               "fake-db-bytes",
		"config/auth_users.json": `{"alpha":"hash"}`,
		"config/cookies/x.txt":   "cookie-contents",
	})

	if err := StageTarball(bytes.NewReader(tarBytes), dataDir); err != nil {
		t.Fatalf("StageTarball: %v", err)
	}
	if !HasPending(dataDir) {
		t.Fatal("HasPending returned false after staging")
	}

	cfg := &config.Config{
		DataDir:      dataDir,
		ConfDir:      confDir,
		DatabasePath: filepath.Join(dataDir, "igloo.db"),
	}

	if err := os.WriteFile(cfg.DatabasePath, []byte("old-db"), 0o644); err != nil {
		t.Fatalf("seed old db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "untouched.txt"), []byte("keep-me"), 0o644); err != nil {
		t.Fatalf("seed untouched: %v", err)
	}

	if err := ApplyPending(cfg); err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}

	dbBytes, err := os.ReadFile(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("read restored db: %v", err)
	}
	if string(dbBytes) != "fake-db-bytes" {
		t.Errorf("db not restored: got %q", string(dbBytes))
	}

	bakBytes, err := os.ReadFile(cfg.DatabasePath + ".pre-restore.bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(bakBytes) != "old-db" {
		t.Errorf("pre-restore backup wrong: got %q", string(bakBytes))
	}

	authBytes, err := os.ReadFile(filepath.Join(confDir, "auth_users.json"))
	if err != nil {
		t.Fatalf("read auth_users.json: %v", err)
	}
	if string(authBytes) != `{"alpha":"hash"}` {
		t.Errorf("auth file not restored: got %q", string(authBytes))
	}

	cookieBytes, err := os.ReadFile(filepath.Join(confDir, "cookies", "x.txt"))
	if err != nil {
		t.Fatalf("read cookies/x.txt: %v", err)
	}
	if string(cookieBytes) != "cookie-contents" {
		t.Errorf("cookie file not restored: got %q", string(cookieBytes))
	}

	keepBytes, err := os.ReadFile(filepath.Join(confDir, "untouched.txt"))
	if err != nil {
		t.Fatalf("read untouched.txt: %v", err)
	}
	if string(keepBytes) != "keep-me" {
		t.Errorf("untouched file should be preserved: got %q", string(keepBytes))
	}

	if HasPending(dataDir) {
		t.Error("HasPending should be false after ApplyPending")
	}
	if _, err := os.Stat(filepath.Join(dataDir, stagingSubdir)); !os.IsNotExist(err) {
		t.Errorf("staging dir should be removed after ApplyPending")
	}
}

func TestStageTarballRejectsMissingDB(t *testing.T) {
	dataDir := t.TempDir()
	tarBytes := buildTarball(t, map[string]string{
		"config/x.txt": "data",
	})
	if err := StageTarball(bytes.NewReader(tarBytes), dataDir); err == nil {
		t.Fatal("expected error for tarball without igloo.db")
	}
}

func TestStageTarballRejectsUnsafePaths(t *testing.T) {
	dataDir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "../escape", Size: 4, Mode: 0o644, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	if err := StageTarball(&buf, dataDir); err == nil {
		t.Fatal("expected error for tar entry escaping staging dir")
	}
}

func TestApplyPendingNoMarkerNoOp(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Config{
		DataDir:      dataDir,
		ConfDir:      t.TempDir(),
		DatabasePath: filepath.Join(dataDir, "igloo.db"),
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatalf("ApplyPending without marker: %v", err)
	}
}
