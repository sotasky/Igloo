package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/restore"
	"github.com/screwys/igloo/internal/toolenv"
	"github.com/screwys/igloo/internal/translate"
	"github.com/screwys/igloo/internal/web"
	"github.com/screwys/igloo/internal/worker"
)

func main() {
	startupStart := time.Now()
	toolenv.ApplyCommonToolPaths()
	cfg := config.Load()
	if cfg.ConfigError != nil {
		slog.Error("invalid configuration", "err", cfg.ConfigError)
		os.Exit(1)
	}
	if err := cfg.EnsureRuntimeDirs(); err != nil {
		slog.Error("failed to create runtime directories", "err", err)
		os.Exit(1)
	}
	if logFile := setupServerLogging(cfg); logFile != nil {
		defer func() {
			_ = logFile.Close()
		}()
	}

	auth.InitCache(cfg.AuthUsersPath)
	logStartupPhase("config_auth", time.Since(startupStart))

	phaseStart := time.Now()
	if err := restore.ApplyPending(cfg); err != nil {
		slog.Error("restore: apply failed", "err", err)
		os.Exit(1)
	}
	logStartupPhase("restore", time.Since(phaseStart))

	phaseStart = time.Now()
	database, err := db.OpenWithOptions(cfg.DatabasePath, cfg.DataDir, db.OpenOptions{
		Phase: func(name string, elapsed time.Duration) {
			logStartupPhase(name, elapsed)
		},
	})
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DatabasePath, "err", err)
		os.Exit(1)
	}
	defer func() {
		_ = database.Close()
	}()
	slog.Info("database opened", "path", cfg.DatabasePath)
	logStartupPhase("db_open", time.Since(phaseStart))

	// Build static version cache
	phaseStart = time.Now()
	staticVersions := buildStaticVersionCache(cfg.StaticDir)
	staticV := func(path string) string {
		if v, ok := staticVersions[path]; ok {
			return "/static/" + path + "?v=" + v
		}
		return "/static/" + path
	}
	logStartupPhase("static_version_cache", time.Since(phaseStart))

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	phaseStart = time.Now()
	workers := worker.NewManager(database, cfg)
	go workers.StartAll()
	go translate.RunBackground(appCtx, database)
	logStartupPhase("worker_launch", time.Since(phaseStart))

	handler := web.NewServer(database, cfg, workers, staticV)
	srv := newHTTPServer(cfg.ListenAddr, handler)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		slog.Info("shutting down")
		cancelApp()
		if !workers.ShutdownTimeout(3 * time.Second) {
			slog.Warn("worker shutdown timed out; continuing server shutdown")
		}
		_ = srv.Shutdown(context.Background())
	}()

	phaseStart = time.Now()
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		slog.Error("listener bind failed", "addr", cfg.ListenAddr, "err", err)
		os.Exit(1)
	}
	logStartupPhase("listener_bind", time.Since(phaseStart))

	// Use TLS if cert/key exist, otherwise plain HTTP
	if _, err := os.Stat(cfg.TLSCert); err == nil {
		slog.Info("listening (TLS)", "addr", cfg.ListenAddr)
		if err := srv.ServeTLS(listener, cfg.TLSCert, cfg.TLSKey); err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	} else {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.Serve(listener); err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
}

func setupServerLogging(cfg *config.Config) io.Closer {
	logDir := filepath.Join(cfg.DataDir, "logs", "server")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil
	}
	logPath := filepath.Join(logDir, "server.log")
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 5*1024*1024 {
		_ = os.Rename(logPath, logPath+".1")
	}
	lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	w := io.MultiWriter(os.Stderr, lf)
	log.SetOutput(w)
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return lf
}

func logStartupPhase(name string, elapsed time.Duration) {
	slog.Info("startup phase complete", "phase", name, "dur", elapsed.String(), "dur_ms", elapsed.Milliseconds())
}

func buildStaticVersionCache(staticDir string) map[string]string {
	versions := make(map[string]string)
	_ = filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(staticDir, path)
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		versions[rel] = fmt.Sprintf("%d", info.ModTime().Unix())
		return nil
	})
	return versions
}
