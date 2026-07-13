package worker

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/settings"
	"github.com/screwys/igloo/internal/storage"
)

const (
	backupCheckInterval = 1 * time.Hour
	backupMinAge        = 24 * time.Hour
	backupMaxDuration   = 10 * time.Minute
	backupPrefix        = "igloo-backup-"
)

func (m *Manager) runBackupWorker(ctx context.Context) {
	m.tryBackup(ctx)
	t := time.NewTicker(backupCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tryBackup(ctx)
		}
	}
}

func (m *Manager) tryBackup(ctx context.Context) {
	enabled, _ := m.db.GetSetting("backup_enabled", "false")
	if enabled != "true" && enabled != "1" {
		return
	}
	dir, _ := m.db.GetSetting("backup_dir", "")
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		err := fmt.Errorf("backup dir must be absolute: %s", dir)
		slog.Error("backup: invalid dir", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}

	if !m.backupDue(dir) {
		return
	}

	m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, Detail: "creating backup"})
	m.Emit("backup", "starting daily backup", "info")

	backupCtx, cancel := context.WithTimeout(ctx, backupMaxDuration)
	defer cancel()
	if err := m.createBackup(backupCtx, dir); err != nil {
		slog.Error("backup: failed", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}

	if err := m.pruneBackups(backupCtx, dir, m.backupKeepCount()); err != nil {
		slog.Error("backup: prune failed", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}
	m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Detail: "idle"})
	m.Emit("backup", "backup completed", "info")
}

func (m *Manager) backupDue(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	var latest time.Time
	for _, e := range entries {
		if !isBackupArchiveName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	if latest.IsZero() {
		return true
	}
	return time.Since(latest) >= backupMinAge
}

func (m *Manager) createBackup(ctx context.Context, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("backup dir is required")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("backup dir must be absolute: %s", dir)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dbSnapFile, err := os.CreateTemp(m.cfg.Storage.StateRoot(), ".igloo-db-snapshot-*.db")
	if err != nil {
		return fmt.Errorf("create db snapshot temp path: %w", err)
	}
	dbSnap := dbSnapFile.Name()
	if err := dbSnapFile.Close(); err != nil {
		_ = os.Remove(dbSnap)
		return fmt.Errorf("close db snapshot temp path: %w", err)
	}
	if err := os.Remove(dbSnap); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("prepare db snapshot path: %w", err)
	}
	defer func() {
		_ = os.Remove(dbSnap)
	}()
	if err := m.db.VacuumInto(ctx, dbSnap); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}

	return m.cfg.Storage.MediaExecutor().Run(ctx, storage.MediaLaneBulkBackground, func() error {
		if err := storage.EnsureDirectory(dir, 0o755); err != nil {
			return fmt.Errorf("create backup dir: %w", err)
		}

		stamp := time.Now().Format("20060102-150405")
		name := fmt.Sprintf("%s%s.zip", backupPrefix, stamp)
		finalPath := filepath.Join(dir, name)

		f, err := os.CreateTemp(dir, "."+backupPrefix+"*.tmp")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		tmpPath := f.Name()
		defer func() {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}()

		zw := zip.NewWriter(f)
		if err := addFileToZip(ctx, zw, dbSnap, config.DatabaseFilename); err != nil {
			_ = zw.Close()
			return fmt.Errorf("zip db: %w", err)
		}

		configDir := m.cfg.ConfDir
		if configDir != "" {
			if err := addDirToZip(ctx, zw, configDir, "config", config.RuntimeConfigBackupAllowed); err != nil {
				_ = zw.Close()
				return fmt.Errorf("zip config: %w", err)
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("close zip: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return fmt.Errorf("sync backup: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close file: %w", err)
		}

		if err := os.Rename(tmpPath, finalPath); err != nil {
			return fmt.Errorf("rename: %w", err)
		}
		if err := storage.SyncDirectory(dir); err != nil {
			return fmt.Errorf("sync backup directory: %w", err)
		}
		return nil
	})
}

func (m *Manager) backupKeepCount() int {
	return settings.ClampBackupKeepCount(m.db.IntSetting("backup_keep_count"))
}

func (m *Manager) pruneBackups(ctx context.Context, dir string, keepCount int) error {
	keepCount = settings.ClampBackupKeepCount(keepCount)
	return m.cfg.Storage.MediaExecutor().Run(ctx, storage.MediaLaneBulkBackground, func() error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var backups []string
		for _, e := range entries {
			if isBackupArchiveName(e.Name()) {
				backups = append(backups, e.Name())
			}
		}
		sort.Strings(backups)
		if len(backups) <= keepCount {
			return nil
		}
		for _, name := range backups[:len(backups)-keepCount] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("remove %s: %w", name, err)
			}
			slog.Info("backup: pruned old backup", "file", name)
		}
		if err := storage.SyncDirectory(dir); err != nil {
			return fmt.Errorf("sync pruned directory: %w", err)
		}
		return nil
	})
}

func isBackupArchiveName(name string) bool {
	return strings.HasPrefix(name, backupPrefix) && strings.HasSuffix(name, ".zip")
}

func addFileToZip(ctx context.Context, zw *zip.Writer, srcPath, zipName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(zipName)
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	return copyBackupData(ctx, dst, f)
}

func addDirToZip(ctx context.Context, zw *zip.Writer, srcDir, zipPrefix string, include func(string) bool) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if include != nil && !include(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(zipPrefix, rel))

		if info.IsDir() {
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = strings.TrimSuffix(zipName, "/") + "/"
			_, err = zw.CreateHeader(hdr)
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = zipName
		hdr.Method = zip.Deflate
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			_ = f.Close()
		}()
		return copyBackupData(ctx, dst, f)
	})
}

func copyBackupData(ctx context.Context, dst io.Writer, src io.Reader) error {
	_, err := io.CopyBuffer(dst, backupContextReader{ctx: ctx, src: src}, make([]byte, 256*1024))
	return err
}

type backupContextReader struct {
	ctx context.Context
	src io.Reader
}

func (r backupContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}
