package restore

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/config"
	igloodb "github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/storage"
)

func restoreTestConfig(t *testing.T, dataDir, confDir string) *config.Config {
	t.Helper()
	return &config.Config{Storage: restoreTestLayout(t, dataDir), ConfDir: confDir}
}

func restoreTestLayout(t *testing.T, dataDir string) storage.Layout {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := storage.New(dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func restoreSplitTestConfig(t *testing.T, stateDir, mediaDir, confDir string) *config.Config {
	t.Helper()
	if err := os.WriteFile(filepath.Join(stateDir, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, ".igloo-media-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := storage.New(stateDir, mediaDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	return &config.Config{Storage: layout, ConfDir: confDir}
}

func restoreDatabaseBytes(t *testing.T, value string) []byte {
	t.Helper()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, config.DatabaseFilename)
	store, err := igloodb.OpenPath(path, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("restore_probe", value); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return mustReadRestoreFile(t, path)
}

func buildZipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestStageZipRoundTripRepairsCurrentConfig(t *testing.T) {
	dataDir := t.TempDir()
	confDir := t.TempDir()
	cfg := restoreTestConfig(t, dataDir, confDir)
	seedRestoreDatabase(t, cfg, "live")
	if err := os.WriteFile(filepath.Join(confDir, "config.json"), []byte(`{"enabled_platforms":`), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename: restoreDatabaseBytes(t, "zip-restored"),
		"config/config.json":    []byte(`{"enabled_platforms":["youtube"]}`),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatalf("StageZip: %v", err)
	}
	if !HasPending(dataDir) {
		t.Fatal("restore marker missing after staging")
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	assertRestoreProbe(t, cfg, "zip-restored")
	if got := string(mustReadRestoreFile(t, filepath.Join(confDir, "config.json"))); got != `{"enabled_platforms":["youtube"]}` {
		t.Fatalf("restored config = %q", got)
	}
}

func TestApplyPendingResetsAndroidSyncIdentityOnPreparedDatabase(t *testing.T) {
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, config.DatabaseFilename)
	source, err := igloodb.OpenPath(sourcePath, sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.SetSetting("restore_probe", "restored"); err != nil {
		t.Fatal(err)
	}
	if err := source.SetSetting("translate_target_lang", "tr"); err != nil {
		t.Fatal(err)
	}
	sourceClock, err := source.GetAndroidSyncClock()
	if err != nil {
		t.Fatal(err)
	}
	if sourceClock.Revision == 0 {
		t.Fatal("source database did not produce a sync head")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	cfg := restoreTestConfig(t, dataDir, t.TempDir())
	seedRestoreDatabase(t, cfg, "live")
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename: mustReadRestoreFile(t, sourcePath),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatal(err)
	}

	restored, err := igloodb.Open(cfg.Storage)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	clock, err := restored.GetAndroidSyncClock()
	if err != nil {
		t.Fatal(err)
	}
	if clock.Epoch == sourceClock.Epoch || clock.Revision != 0 {
		t.Fatalf("restored clock = %+v, source = %+v", clock, sourceClock)
	}
	var heads int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM android_sync_heads`).Scan(&heads); err != nil {
		t.Fatal(err)
	}
	if heads != 0 {
		t.Fatalf("restored heads = %d, want 0", heads)
	}
	if value, err := restored.GetSetting("restore_probe", ""); err != nil || value != "restored" {
		t.Fatalf("restored canonical data = %q, %v", value, err)
	}
}

func TestDatabaseRestorePreservesAssetRowsAndMediaFiles(t *testing.T) {
	const key = "media/twitter/sample_post/image.jpg"
	sourceLayout := restoreTestLayout(t, t.TempDir())
	sourcePath, err := sourceLayout.WritePath(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("source-media"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceDB, err := igloodb.Open(sourceLayout)
	if err != nil {
		t.Fatal(err)
	}
	asset := igloodb.Asset{
		AssetID: "sample_media", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post",
		SourceURL: "https://example.test/media.jpg", FilePath: key, ContentType: "image/jpeg", RequiredReason: "like",
	}
	if err := sourceDB.StoreReadyAsset(asset, 1234); err != nil {
		_ = sourceDB.Close()
		t.Fatal(err)
	}
	want, err := sourceDB.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil || want == nil {
		t.Fatalf("source asset = %+v, %v", want, err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	cfg := restoreTestConfig(t, dataDir, t.TempDir())
	destinationPath, err := cfg.Storage.WritePath(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationPath, []byte("destination-media"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename: mustReadRestoreFile(t, sourceLayout.DatabasePath()),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatalf("StageZip: %v", err)
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	restored, err := igloodb.Open(cfg.Storage)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil || got == nil {
		t.Fatalf("restored asset = %+v, %v", got, err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	if got.FilePath != want.FilePath || got.State != want.State || got.SHA256 != want.SHA256 || got.SizeBytes != want.SizeBytes {
		t.Fatalf("restored asset = %#v, want path/state/hash/size from %#v", got, want)
	}
	if got := string(mustReadRestoreFile(t, destinationPath)); got != "destination-media" {
		t.Fatalf("canonical media changed to %q", got)
	}
}

func TestStageRejectsInvalidRuntimeAndAuthFilesBeforeMarker(t *testing.T) {
	tests := []struct {
		name, path, body, want string
	}{
		{"runtime syntax", runtimeName, `{"version":`, "parse staged runtime.json"},
		{"runtime version", runtimeName, `{"version":3}`, "unsupported staged runtime.json version"},
		{"runtime config", "config/config.json", `{"enabled_platforms":`, "parse runtime config"},
		{"auth users", "config/auth_users.json", `{"sample":"invalid"}`, "parse auth users"},
		{"auth secret", "config/auth_secret", "", "staged auth secret is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			archive := buildZipBytes(t, map[string][]byte{
				config.DatabaseFilename: restoreDatabaseBytes(t, "invalid-config"),
				test.path:               []byte(test.body),
			})
			err := StageZip(bytes.NewReader(archive), int64(len(archive)), restoreTestLayout(t, dataDir))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("StageZip error = %v, want %q", err, test.want)
			}
			if HasPending(dataDir) {
				t.Fatal("invalid restore left a marker")
			}
		})
	}
}

func TestApplyPendingRejectsTamperedConfigWithoutPublishing(t *testing.T) {
	dataDir := t.TempDir()
	confDir := t.TempDir()
	cfg := restoreTestConfig(t, dataDir, confDir)
	seedRestoreDatabase(t, cfg, "live")
	liveConfigPath := filepath.Join(confDir, "config.json")
	liveConfig := []byte(`{"enabled_platforms":["twitter"]}`)
	if err := os.WriteFile(liveConfigPath, liveConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename: restoreDatabaseBytes(t, "staged"),
		"config/config.json":    []byte(`{"enabled_platforms":["youtube"]}`),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir(dataDir), "config", "config.json"), []byte(`{"enabled_platforms":`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ApplyPending(cfg)
	if err == nil || !strings.Contains(err.Error(), "validate staged config") {
		t.Fatalf("ApplyPending error = %v", err)
	}
	assertRestoreProbe(t, cfg, "live")
	if got := mustReadRestoreFile(t, liveConfigPath); !bytes.Equal(got, liveConfig) {
		t.Fatalf("live config changed to %q", got)
	}
	if !HasPending(dataDir) {
		t.Fatal("tampered restore did not remain pending")
	}
}

func TestRuntimePathsAreRewrittenWithoutTouchingMedia(t *testing.T) {
	stateDir := t.TempDir()
	mediaDir := t.TempDir()
	confDir := t.TempDir()
	repoDir := t.TempDir()
	cfg := restoreSplitTestConfig(t, stateDir, mediaDir, confDir)
	cfg.RepoDir = repoDir
	mediaPath := filepath.Join(mediaDir, "youtube", "sample", "video.mp4")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("keep-media"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename:          restoreDatabaseBytes(t, "staged"),
		runtimeName:                      []byte(`{"version":2,"data_dir":"/old/data","media_dir":"/old/media","config_dir":"/old/config","repo_dir":"/old/repo"}`),
		"config/nginx.conf":              []byte("pid /old/data/nginx.pid;\nalias /old/media/;\nssl_certificate /old/config/server.crt;\nroot /old/repo/static;\n"),
		"media/youtube/sample/video.mp4": []byte("archive-media"),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatal(err)
	}
	mediaMarker := filepath.Join(mediaDir, ".igloo-media-root")
	if err := os.Remove(mediaMarker); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPending(cfg); err == nil || !strings.Contains(err.Error(), "missing marker") {
		t.Fatalf("ApplyPending without media root = %v", err)
	}
	if !HasPending(stateDir) {
		t.Fatal("restore did not remain pending")
	}
	if err := os.WriteFile(mediaMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatal(err)
	}
	nginx := string(mustReadRestoreFile(t, filepath.Join(confDir, "nginx.conf")))
	for _, want := range []string{stateDir, mediaDir, confDir, repoDir} {
		if !strings.Contains(nginx, want) {
			t.Fatalf("restored config missing %q: %s", want, nginx)
		}
	}
	if got := string(mustReadRestoreFile(t, mediaPath)); got != "keep-media" {
		t.Fatalf("media changed to %q", got)
	}
}

func TestStageZipRejectsUnsafeAndDuplicateEntries(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		dataDir := t.TempDir()
		archive := buildZipBytes(t, map[string][]byte{
			config.DatabaseFilename: restoreDatabaseBytes(t, "staged"),
			"config/../../escape":   []byte("escape"),
		})
		err := StageZip(bytes.NewReader(archive), int64(len(archive)), restoreTestLayout(t, dataDir))
		if err == nil || !strings.Contains(err.Error(), "unsafe backup path") {
			t.Fatalf("StageZip error = %v", err)
		}
		if HasPending(dataDir) {
			t.Fatal("unsafe archive left a marker")
		}
	})

	t.Run("duplicate database", func(t *testing.T) {
		dataDir := t.TempDir()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for range 2 {
			entry, err := zw.Create(config.DatabaseFilename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write(restoreDatabaseBytes(t, "staged")); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		err := StageZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), restoreTestLayout(t, dataDir))
		if err == nil || !strings.Contains(err.Error(), "duplicate backup entry") {
			t.Fatalf("StageZip error = %v", err)
		}
	})
}

func TestStageZipRejectsCorruptAndIncompatibleDatabases(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"corrupt", []byte("not-a-database"), "validate staged db"},
		{"incompatible", incompatibleRestoreDatabaseBytes(t), "database schema does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			archive := buildZipBytes(t, map[string][]byte{config.DatabaseFilename: test.data})
			err := StageZip(bytes.NewReader(archive), int64(len(archive)), restoreTestLayout(t, dataDir))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("StageZip error = %v, want %q", err, test.want)
			}
			if HasPending(dataDir) {
				t.Fatal("rejected database left a marker")
			}
		})
	}
}

func TestApplyPendingRetriesAfterPartialFileReplacement(t *testing.T) {
	dataDir := t.TempDir()
	confDir := t.TempDir()
	cfg := restoreTestConfig(t, dataDir, confDir)
	seedRestoreDatabase(t, cfg, "live")
	configPath := filepath.Join(confDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"enabled_platforms":["twitter"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := buildZipBytes(t, map[string][]byte{
		config.DatabaseFilename: restoreDatabaseBytes(t, "staged"),
		"config/config.json":    []byte(`{"enabled_platforms":["youtube"]}`),
	})
	if err := StageZip(bytes.NewReader(archive), int64(len(archive)), cfg.Storage); err != nil {
		t.Fatal(err)
	}
	blockingSidecar := cfg.Storage.DatabasePath() + "-wal"
	if err := os.Mkdir(blockingSidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ApplyPending(cfg)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("ApplyPending error = %v", err)
	}
	if err := os.Remove(blockingSidecar); err != nil {
		t.Fatal(err)
	}
	assertRestoreProbe(t, cfg, "live")
	if got := string(mustReadRestoreFile(t, configPath)); got != `{"enabled_platforms":["youtube"]}` {
		t.Fatalf("first replacement was not retained for retry: %q", got)
	}
	if !HasPending(dataDir) {
		t.Fatal("failed restore did not remain pending")
	}
	if err := ApplyPending(cfg); err != nil {
		t.Fatal(err)
	}
	assertRestoreProbe(t, cfg, "staged")
	if HasPending(dataDir) {
		t.Fatal("successful retry left restore pending")
	}
}

func TestStageZipMissingDatabaseAndApplyWithoutMarker(t *testing.T) {
	dataDir := t.TempDir()
	archive := buildZipBytes(t, map[string][]byte{"config/config.json": []byte("{}")})
	err := StageZip(bytes.NewReader(archive), int64(len(archive)), restoreTestLayout(t, dataDir))
	if !errors.Is(err, ErrMissingDatabase) {
		t.Fatalf("StageZip error = %v", err)
	}
	if _, err := os.Stat(stagingDir(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("missing database left staging: %v", err)
	}
	if err := ApplyPending(restoreTestConfig(t, dataDir, t.TempDir())); err != nil {
		t.Fatalf("ApplyPending without marker: %v", err)
	}
}

func seedRestoreDatabase(t *testing.T, cfg *config.Config, probe string) {
	t.Helper()
	store, err := igloodb.Open(cfg.Storage)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("restore_probe", probe); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertRestoreProbe(t *testing.T, cfg *config.Config, want string) {
	t.Helper()
	store, err := igloodb.OpenPath(cfg.Storage.DatabasePath(), cfg.Storage.StateRoot())
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := store.GetSetting("restore_probe", "")
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(errors.Join(readErr, closeErr))
	}
	if got != want {
		t.Fatalf("restore probe = %q, want %q", got, want)
	}
}

func incompatibleRestoreDatabaseBytes(t *testing.T) []byte {
	t.Helper()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, config.DatabaseFilename)
	store, err := igloodb.OpenPath(path, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ExecRaw(`ALTER TABLE settings ADD COLUMN retired_value TEXT`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return mustReadRestoreFile(t, path)
}

func mustReadRestoreFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
