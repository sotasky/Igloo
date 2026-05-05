package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/restore"
	"github.com/screwys/igloo/internal/translate"
	"github.com/screwys/igloo/internal/web"
	"github.com/screwys/igloo/internal/worker"
)

func main() {
	cfg := config.Load()
	if cfg.ConfigError != nil {
		slog.Error("invalid configuration", "err", cfg.ConfigError)
		os.Exit(1)
	}
	auth.InitCache(cfg.AuthUsersPath)

	// Tee all log output to logs/server/server.log
	logDir := filepath.Join(cfg.DataDir, "logs", "server")
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		logPath := filepath.Join(logDir, "server.log")
		if fi, err := os.Stat(logPath); err == nil && fi.Size() > 5*1024*1024 {
			_ = os.Rename(logPath, logPath+".1")
		}
		if lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			defer lf.Close()
			w := io.MultiWriter(os.Stderr, lf)
			log.SetOutput(w)
			slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
		}
	}

	if err := restore.ApplyPending(cfg); err != nil {
		slog.Error("restore: apply failed", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DatabasePath, cfg.DataDir)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DatabasePath, "err", err)
		os.Exit(1)
	}
	defer database.Close()
	slog.Info("database opened", "path", cfg.DatabasePath)

	// Build static version cache
	staticVersions := buildStaticVersionCache(cfg.StaticDir)
	staticV := func(path string) string {
		if v, ok := staticVersions[path]; ok {
			return "/static/" + path + "?v=" + v
		}
		return "/static/" + path
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	workers := worker.NewManager(database, cfg)
	go workers.StartAll()
	go translate.RunBackground(appCtx, database)

	handler := web.NewServer(database, cfg, workers, staticV)
	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		slog.Info("shutting down")
		cancelApp()
		workers.Shutdown()
		_ = srv.Shutdown(context.Background())
	}()

	// Use TLS if cert/key exist, otherwise plain HTTP
	if _, err := os.Stat(cfg.TLSCert); err == nil {
		slog.Info("listening (TLS)", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	} else {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}
}

func buildStaticVersionCache(staticDir string) map[string]string {
	versions := make(map[string]string)
	filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
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
