package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/storage"
)

type errorReader struct {
	err error
}

func testStorage(t *testing.T, stateRoot string) storage.Layout {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestParseDurationFallback(t *testing.T) {
	if d := parseDuration("6h"); d.Hours() != 6 {
		t.Fatalf("got %v want 6h", d)
	}
	if d := parseDuration("nonsense"); d == 0 {
		t.Fatalf("fallback should be nonzero")
	}
}

func TestParseEnabledPlatformsBlankMeansNone(t *testing.T) {
	got, err := ParseEnabledPlatforms("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v want no platforms", got)
	}
}

func TestParseEnabledPlatformsAll(t *testing.T) {
	got, err := ParseEnabledPlatforms("all")
	if err != nil {
		t.Fatal(err)
	}
	want := "youtube,twitter,tiktok,instagram"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestParseEnabledPlatformsNone(t *testing.T) {
	got, err := ParseEnabledPlatforms("none")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v want no platforms", got)
	}
}

func TestParseEnabledPlatformsDedupesAndAliasesX(t *testing.T) {
	got, err := ParseEnabledPlatforms("youtube,x,twitter,tiktok,instagram")
	if err != nil {
		t.Fatal(err)
	}
	want := "youtube,twitter,tiktok,instagram"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestParseEnabledPlatformsRejectsUnknown(t *testing.T) {
	if _, err := ParseEnabledPlatforms("youtube,myspace"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestConfigEffectivePlatforms(t *testing.T) {
	platforms, err := ParseEnabledPlatforms("youtube,tiktok")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{EnabledPlatforms: platforms, EnabledPlatformSet: platformSet(platforms)}
	got := cfg.EffectivePlatforms([]string{"youtube", "twitter", "tiktok"})
	if strings.Join(got, ",") != "youtube,tiktok" {
		t.Fatalf("got %v", got)
	}
}

func TestEnsureRuntimeDirsUsesProvisionedStorageAndCreatesConfigDir(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Storage: testStorage(t, filepath.Join(root, "state", "data")),
		ConfDir: filepath.Join(root, "state", "config"),
	}

	if err := cfg.EnsureRuntimeDirs(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{cfg.Storage.StateRoot(), cfg.ConfDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestEnsureRuntimeDirsDoesNotProvisionMissingStateRoot(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "missing-state")
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Storage: layout, ConfDir: filepath.Join(root, "config")}
	if err := cfg.EnsureRuntimeDirs(); err == nil {
		t.Fatal("EnsureRuntimeDirs succeeded with a missing state root")
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("EnsureRuntimeDirs created the missing state root: %v", err)
	}
}

func TestEnsureRuntimeDirsRejectsEmptyPaths(t *testing.T) {
	if err := (&Config{ConfDir: t.TempDir()}).EnsureRuntimeDirs(); err == nil {
		t.Fatal("expected empty data dir error")
	}
	if err := (&Config{Storage: testStorage(t, t.TempDir())}).EnsureRuntimeDirs(); err == nil {
		t.Fatal("expected empty config dir error")
	}
}

func TestRuntimeConfigBackupAllowedSkipsSecretsAndEnv(t *testing.T) {
	tests := map[string]bool{
		".env":                          false,
		"auth_secret":                   false,
		"auth_users.json":               false,
		"config.json":                   true,
		"cookies/twitter.txt":           false,
		"custom.env":                    false,
		"nested/token.txt":              false,
		"nginx.conf":                    true,
		"server.crt":                    true,
		"server.key":                    false,
		filepath.Join("nested", ".tmp"): false,
	}
	for rel, want := range tests {
		if got := RuntimeConfigBackupAllowed(rel); got != want {
			t.Fatalf("RuntimeConfigBackupAllowed(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestPlatformEnabledNormalizesAliases(t *testing.T) {
	platforms, err := ParseEnabledPlatforms("youtube,twitter")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{EnabledPlatforms: platforms, EnabledPlatformSet: platformSet(platforms)}

	if !cfg.PlatformEnabled("x") {
		t.Fatal("x alias should map to twitter")
	}
	if !cfg.PlatformEnabled("") {
		t.Fatal("empty legacy platform should map to youtube")
	}
}

func TestLoadEnabledPlatformsCanDisableTwitter(t *testing.T) {
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("IGLOO_ENABLED_PLATFORMS", "youtube,tiktok")

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if cfg.PlatformEnabled("twitter") {
		t.Fatal("twitter should be disabled by IGLOO_ENABLED_PLATFORMS")
	}
}

func TestLoadFreshInstallDefaultsToNoPlatforms(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", dataDir)
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if len(cfg.EnabledPlatforms) != 0 {
		t.Fatalf("EnabledPlatforms = %v, want none", cfg.EnabledPlatforms)
	}
	if cfg.PlatformEnabled("youtube") {
		t.Fatal("youtube should be opt-in on fresh installs")
	}
	if cfg.Storage.DatabasePath() != filepath.Join(dataDir, DatabaseFilename) {
		t.Fatalf("DatabasePath = %q, want igloo.db default", cfg.Storage.DatabasePath())
	}
}

func TestLoadDefaultsSessionCookiesToLANHTTP(t *testing.T) {
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())

	cfg := Load()
	if cfg.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = true, want false")
	}
}

func TestLoadNormalizesPublishedServerURL(t *testing.T) {
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("IGLOO_PUBLISHED_SERVER_URL", "igloo.local:5001/")

	cfg := Load()
	if cfg.PublishedServerURL != "http://igloo.local:5001" {
		t.Fatalf("PublishedServerURL = %q", cfg.PublishedServerURL)
	}
}

func TestLoadCanRequireSecureSessionCookies(t *testing.T) {
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("IGLOO_SESSION_COOKIE_SECURE", "true")

	cfg := Load()
	if !cfg.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = false, want true")
	}
}

func TestLoadSecretKeyPanicsWhenRandomFails(t *testing.T) {
	oldReader := secretKeyRandomReader
	secretKeyRandomReader = errorReader{err: errors.New("random unavailable")}
	t.Cleanup(func() {
		secretKeyRandomReader = oldReader
	})
	t.Setenv("AUTH_SECRET_KEY", "")

	defer func() {
		if recover() == nil {
			t.Fatal("expected loadSecretKey to panic when random source fails")
		}
	}()
	_ = loadSecretKey(t.TempDir())
}

func TestLoadUsesExplicitMediaRoot(t *testing.T) {
	stateRoot := t.TempDir()
	mediaRoot := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", stateRoot)
	t.Setenv("IGLOO_MEDIA_DIR", mediaRoot)
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if cfg.Storage.DatabasePath() != filepath.Join(stateRoot, DatabaseFilename) {
		t.Fatalf("DatabasePath = %q", cfg.Storage.DatabasePath())
	}
	if cfg.Storage.MediaRoot() != mediaRoot {
		t.Fatalf("MediaRoot = %q, want %q", cfg.Storage.MediaRoot(), mediaRoot)
	}
}

func TestLoadKeepsExistingInstallPlatformsWhenNoRuntimeConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", configDir)
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(configDir, "auth_users.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if got := strings.Join(cfg.EnabledPlatforms, ","); got != "youtube,twitter,tiktok,instagram" {
		t.Fatalf("EnabledPlatforms = %q", got)
	}
}

func TestLoadRuntimeConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", configDir)
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"enabled_platforms":["youtube","twitter"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if got := strings.Join(cfg.EnabledPlatforms, ","); got != "youtube,twitter" {
		t.Fatalf("EnabledPlatforms = %q", got)
	}
}
