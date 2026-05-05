package worker

import (
	"archive/tar"
	"compress/gzip"
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
)

const (
	backupCheckInterval = 1 * time.Hour
	backupMinAge        = 24 * time.Hour
	backupKeepCount     = 5
	backupPrefix        = "igloo-backup-"
)

func (m *Manager) runBackupWorker(ctx context.Context) {
	m.tryBackup()
	t := time.NewTicker(backupCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tryBackup()
		}
	}
}

func (m *Manager) tryBackup() {
	enabled, _ := m.db.GetSetting("backup_enabled", "false")
	if enabled != "true" && enabled != "1" {
		return
	}
	dir, _ := m.db.GetSetting("backup_dir", "")
	if dir == "" {
		return
	}

	if !m.backupDue(dir) {
		return
	}

	m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, Detail: "creating backup"})
	m.Emit("backup", "starting daily backup", "info")

	if err := m.createBackup(dir); err != nil {
		slog.Error("backup: failed", "err", err)
		m.setStatus("backup", WorkerStatus{Name: "backup", Running: true, LastRunAt: time.Now(), Error: err.Error()})
		m.Emit("backup", "backup failed: "+err.Error(), "error")
		return
	}

	m.pruneBackups(dir)
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
		if !strings.HasPrefix(e.Name(), backupPrefix) || !strings.HasSuffix(e.Name(), ".tar.gz") {
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

func (m *Manager) createBackup(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s%s.tar.gz", backupPrefix, stamp)
	tmpPath := filepath.Join(dir, name+".tmp")
	finalPath := filepath.Join(dir, name)

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		f.Close()
		os.Remove(tmpPath)
	}()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	dbSnapFile, err := os.CreateTemp(dir, ".igloo-db-snapshot-*.db")
	if err != nil {
		return fmt.Errorf("create db snapshot temp path: %w", err)
	}
	dbSnap := dbSnapFile.Name()
	if err := dbSnapFile.Close(); err != nil {
		os.Remove(dbSnap)
		return fmt.Errorf("close db snapshot temp path: %w", err)
	}
	if err := os.Remove(dbSnap); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("prepare db snapshot path: %w", err)
	}
	if err := m.db.VacuumInto(dbSnap); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	defer os.Remove(dbSnap)

	if err := addFileToTar(tw, dbSnap, config.DatabaseFilename); err != nil {
		return fmt.Errorf("tar db: %w", err)
	}

	configDir := m.cfg.ConfDir
	if configDir != "" {
		if err := addDirToTar(tw, configDir, "config"); err != nil {
			return fmt.Errorf("tar config: %w", err)
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (m *Manager) pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupPrefix) && strings.HasSuffix(e.Name(), ".tar.gz") {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups)
	if len(backups) <= backupKeepCount {
		return
	}
	for _, name := range backups[:len(backups)-backupKeepCount] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			slog.Warn("backup: prune failed", "file", name, "err", err)
		} else {
			slog.Info("backup: pruned old backup", "file", name)
		}
	}
}

func addFileToTar(tw *tar.Writer, srcPath, tarName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name:    tarName,
		Size:    info.Size(),
		Mode:    0o644,
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func addDirToTar(tw *tar.Writer, srcDir, tarPrefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		tarName := filepath.Join(tarPrefix, rel)

		if info.IsDir() {
			hdr := &tar.Header{
				Name:     tarName + "/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
				ModTime:  info.ModTime(),
			}
			return tw.WriteHeader(hdr)
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		hdr := &tar.Header{
			Name:    tarName,
			Size:    info.Size(),
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}
