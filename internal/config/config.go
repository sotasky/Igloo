package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DatabaseFilename = "igloo.db"
)

var SupportedPlatforms = []string{"youtube", "twitter", "tiktok", "instagram"}

var secretKeyRandomReader io.Reader = rand.Reader

type Config struct {
	DatabasePath      string
	DataDir           string
	ConfDir           string
	RepoDir           string
	StaticDir         string
	LocaleDir         string
	ListenAddr        string
	SecretKey         string
	CookiesDir        string
	TLSCert           string
	TLSKey            string
	AuthUsersPath     string
	RuntimeConfigPath string

	EnabledPlatforms   []string
	EnabledPlatformSet map[string]bool
	ConfigError        error
}

func Load() *Config {
	dataDir := envOr("IGLOO_DATA_DIR", filepath.Join(homeDir(), ".local", "share", "igloo"))
	configDir := envOr("IGLOO_CONFIG_DIR", filepath.Join(homeDir(), ".config", "igloo"))
	repoDir := envOr("IGLOO_REPO_DIR", findRepoDir())
	databasePath := envOr("IGLOO_DB_PATH", DefaultDatabasePath(dataDir))
	runtimePath := filepath.Join(configDir, "config.json")
	runtimeConfig, runtimeErr := loadRuntimeConfig(runtimePath)
	enabledPlatforms, platformErr := resolveEnabledPlatforms(configDir, runtimeConfig)

	return &Config{
		DatabasePath:      databasePath,
		DataDir:           dataDir,
		ConfDir:           configDir,
		RepoDir:           repoDir,
		StaticDir:         filepath.Join(repoDir, "static"),
		LocaleDir:         envOr("IGLOO_LOCALE_DIR", filepath.Join(repoDir, "locales", "app")),
		ListenAddr:        ":" + envOr("IGLOO_PORT", "5001"),
		SecretKey:         loadSecretKey(configDir),
		CookiesDir:        filepath.Join(configDir, "cookies"),
		TLSCert:           filepath.Join(configDir, "server.crt"),
		TLSKey:            filepath.Join(configDir, "server.key"),
		AuthUsersPath:     filepath.Join(configDir, "auth_users.json"),
		RuntimeConfigPath: runtimePath,

		EnabledPlatforms:   enabledPlatforms,
		EnabledPlatformSet: platformSet(enabledPlatforms),
		ConfigError:        firstErr(runtimeErr, platformErr),
	}
}

func DefaultDatabasePath(dataDir string) string {
	return filepath.Join(dataDir, DatabaseFilename)
}

type RuntimeConfig struct {
	EnabledPlatforms []string `json:"enabled_platforms"`
}

func ParseEnabledPlatforms(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "all") {
		return append([]string(nil), SupportedPlatforms...), nil
	}
	if raw == "" || strings.EqualFold(raw, "none") {
		return []string{}, nil
	}
	return NormalizeEnabledPlatforms(strings.Split(raw, ","))
}

func NormalizeEnabledPlatforms(platforms []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(platforms))
	for _, part := range platforms {
		if strings.TrimSpace(part) == "" {
			continue
		}
		p := NormalizePlatform(part)
		if !isSupportedPlatform(p) {
			return nil, fmt.Errorf("IGLOO_ENABLED_PLATFORMS contains unsupported platform %q; supported: %s", p, strings.Join(SupportedPlatforms, ","))
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

func (c *Config) ApplyRuntimeConfig(platforms []string) error {
	normalized, err := NormalizeEnabledPlatforms(platforms)
	if err != nil {
		return err
	}
	c.EnabledPlatforms = normalized
	c.EnabledPlatformSet = platformSet(normalized)
	return nil
}

func (c *Config) SaveRuntimeConfig(platforms []string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if err := c.ApplyRuntimeConfig(platforms); err != nil {
		return err
	}
	path := c.RuntimeConfigPath
	if path == "" {
		path = filepath.Join(c.ConfDir, "config.json")
		c.RuntimeConfigPath = path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	data, err := json.MarshalIndent(RuntimeConfig{
		EnabledPlatforms: c.EnabledPlatforms,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config_*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write runtime config: %w", err)
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write runtime config newline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close runtime config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod runtime config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename runtime config: %w", err)
	}
	return nil
}

func (c *Config) PlatformEnabled(platform string) bool {
	platform = NormalizePlatform(platform)
	if c == nil {
		return isSupportedPlatform(platform)
	}
	if c.EnabledPlatformSet == nil {
		return isSupportedPlatform(platform)
	}
	return c.EnabledPlatformSet[platform]
}

func (c *Config) EffectivePlatforms(platforms []string) []string {
	if len(platforms) == 0 {
		platforms = SupportedPlatforms
	}
	out := make([]string, 0, len(platforms))
	seen := make(map[string]bool)
	for _, p := range platforms {
		p = NormalizePlatform(p)
		if c.PlatformEnabled(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (c *Config) EnsureRuntimeDirs() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("data dir is empty")
	}
	if strings.TrimSpace(c.ConfDir) == "" {
		return fmt.Errorf("config dir is empty")
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data dir: %w", err)
	}
	if err := os.MkdirAll(c.ConfDir, 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	return nil
}

func platformSet(platforms []string) map[string]bool {
	set := make(map[string]bool, len(platforms))
	for _, p := range platforms {
		set[p] = true
	}
	return set
}

func resolveEnabledPlatforms(configDir string, runtimeConfig RuntimeConfig) ([]string, error) {
	if raw := os.Getenv("IGLOO_ENABLED_PLATFORMS"); strings.TrimSpace(raw) != "" {
		return ParseEnabledPlatforms(raw)
	}
	if runtimeConfig.EnabledPlatforms != nil {
		return NormalizeEnabledPlatforms(runtimeConfig.EnabledPlatforms)
	}
	if _, err := os.Stat(filepath.Join(configDir, "auth_users.json")); err == nil {
		return append([]string(nil), SupportedPlatforms...), nil
	}
	return []string{}, nil
}

func loadRuntimeConfig(path string) (RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return RuntimeConfig{}, nil
	}
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("read runtime config: %w", err)
	}
	var cfg RuntimeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return RuntimeConfig{}, fmt.Errorf("parse runtime config: %w", err)
	}
	return cfg, nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func NormalizePlatform(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch p {
	case "":
		return "youtube"
	case "x":
		return "twitter"
	default:
		return p
	}
}

func isSupportedPlatform(platform string) bool {
	for _, p := range SupportedPlatforms {
		if p == platform {
			return true
		}
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func findRepoDir() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func loadSecretKey(configDir string) string {
	if v := os.Getenv("AUTH_SECRET_KEY"); v != "" {
		return v
	}
	path := filepath.Join(configDir, "auth_secret")
	if data, err := os.ReadFile(path); err == nil {
		if s := string(data); len(s) > 0 {
			return s
		}
	}
	b := make([]byte, 32)
	if _, err := io.ReadFull(secretKeyRandomReader, b); err != nil {
		panic(fmt.Sprintf("config: generate secret key: %v", err))
	}
	secret := hex.EncodeToString(b)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		panic(fmt.Sprintf("config: create secret key dir: %v", err))
	}
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		panic(fmt.Sprintf("config: write secret key: %v", err))
	}
	return secret
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 10 * time.Minute
	}
	return d
}
