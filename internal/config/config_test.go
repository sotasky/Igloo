package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLoadRSSHubBlankDisablesIngestConfig(t *testing.T) {
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("IGLOO_ENABLED_PLATFORMS", "youtube,tiktok")
	t.Setenv("RSSHUB_BASE", "")

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if cfg.RSSHubBase != "" {
		t.Fatalf("RSSHubBase = %q, want blank", cfg.RSSHubBase)
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
	t.Setenv("RSSHUB_BASE", "")

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
	if cfg.DatabasePath != filepath.Join(dataDir, DatabaseFilename) {
		t.Fatalf("DatabasePath = %q, want igloo.db default", cfg.DatabasePath)
	}
}

func TestLoadRespectsExplicitDBPath(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.db")
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", t.TempDir())
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("IGLOO_DB_PATH", custom)
	t.Setenv("RSSHUB_BASE", "")

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if cfg.DatabasePath != custom {
		t.Fatalf("DatabasePath = %q, want %q", cfg.DatabasePath, custom)
	}
}

func TestLoadKeepsExistingInstallPlatformsWhenNoRuntimeConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", t.TempDir())
	t.Setenv("IGLOO_CONFIG_DIR", configDir)
	t.Setenv("IGLOO_REPO_DIR", t.TempDir())
	t.Setenv("RSSHUB_BASE", "")
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
	t.Setenv("RSSHUB_BASE", "")
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"enabled_platforms":["youtube","twitter"],"rsshub_base":"http://rsshub:1200"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.ConfigError != nil {
		t.Fatal(cfg.ConfigError)
	}
	if got := strings.Join(cfg.EnabledPlatforms, ","); got != "youtube,twitter" {
		t.Fatalf("EnabledPlatforms = %q", got)
	}
	if cfg.RSSHubBase != "http://rsshub:1200" {
		t.Fatalf("RSSHubBase = %q", cfg.RSSHubBase)
	}
}
