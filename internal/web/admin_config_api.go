package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/restore"
	"github.com/screwys/igloo/internal/storage"
)

// ── Config export / import ────────────────────────────────────────────────────

const (
	configImportMaxBodyBytes     int64 = 512 << 20
	configImportMaxMemoryBytes   int64 = 8 << 20
	configImportJSONMaxBodyBytes int64 = 16 << 20
)

func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cfg, err := s.db.ExportConfig()
	if err != nil {
		slog.Error("ExportConfig", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export error"})
		return
	}

	if dir := s.configuredExportDir(); dir != "" {
		path, err := writeExportFile(r.Context(), s.cfg.Storage.MediaExecutor(), dir, "igloo-config", ".json", func(dst io.Writer) error {
			return writeConfigExportJSON(dst, cfg)
		})
		if err != nil {
			slog.Error("ExportConfig save", "dir", dir, "err", err)
			writeJSON(w, 500, map[string]any{"error": "export save error"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"saved":   true,
			"path":    path,
		})
		return
	}

	// Config export is a downloadable JSON document, not an API response —
	// envelope fields would pollute the archived file. apiPath() excludes it.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="igloo-config-%s.json"`,
			time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(200)
	if err := writeConfigExportJSON(w, cfg); err != nil {
		slog.Error("ExportConfig write", "err", err)
	}
}

func (s *Server) handleConfigExportSubscriptions(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	cfg, err := s.db.ExportSubscriptions()
	if err != nil {
		slog.Error("ExportSubscriptions", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export error"})
		return
	}

	if dir := s.configuredExportDir(); dir != "" {
		path, err := writeExportFile(r.Context(), s.cfg.Storage.MediaExecutor(), dir, "igloo-subscriptions", ".json", func(dst io.Writer) error {
			return writeSubscriptionsExportJSON(dst, cfg)
		})
		if err != nil {
			slog.Error("ExportSubscriptions save", "dir", dir, "err", err)
			writeJSON(w, 500, map[string]any{"error": "export save error"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"saved":   true,
			"path":    path,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="igloo-subscriptions-%s.json"`,
			time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(200)
	if err := writeSubscriptionsExportJSON(w, cfg); err != nil {
		slog.Error("ExportSubscriptions write", "err", err)
	}
}

func (s *Server) handleConfigExportFull(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	runtimeFiles, err := s.collectFullExportRuntimeConfigFiles()
	if err != nil {
		slog.Error("ExportFullData runtime config", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export config error"})
		return
	}
	runtimeManifest := s.fullExportRuntimeManifest()
	dbSnapshotPath, cleanupSnapshot, err := s.createFullExportDatabaseSnapshot(r.Context())
	if err != nil {
		slog.Error("ExportFullData database snapshot", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export snapshot error"})
		return
	}
	defer cleanupSnapshot()
	cfg, err := exportFullDataSnapshot(dbSnapshotPath, s.cfg.Storage)
	if err != nil {
		slog.Error("ExportFullData snapshot", "err", err)
		writeJSON(w, 500, map[string]any{"error": "export snapshot error"})
		return
	}

	if dir := s.configuredExportDir(); dir != "" {
		path, err := writeExportFile(r.Context(), s.cfg.Storage.MediaExecutor(), dir, "igloo-full", ".zip", func(dst io.Writer) error {
			return writeFullExportZip(dst, cfg, dbSnapshotPath, runtimeFiles, runtimeManifest)
		})
		if err != nil {
			slog.Error("ExportFullData save", "dir", dir, "err", err)
			writeJSON(w, 500, map[string]any{"error": "export save error"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success": true,
			"saved":   true,
			"path":    path,
		})
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="igloo-full-%s.zip"`,
			time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(200)
	if err := writeFullExportZip(w, cfg, dbSnapshotPath, runtimeFiles, runtimeManifest); err != nil {
		slog.Error("ExportFullData zip write", "err", err)
		panic(http.ErrAbortHandler)
	}
}

func writeConfigExportJSON(w io.Writer, cfg db.ConfigExport) error {
	return json.NewEncoder(w).Encode(cfg)
}

type subscriptionsExportDocument struct {
	Version       int                `json:"version"`
	Scope         string             `json:"scope"`
	ExportedAt    time.Time          `json:"exported_at"`
	Subscriptions []db.ChannelExport `json:"subscriptions"`
}

func subscriptionsExportPayload(cfg db.ConfigExport) subscriptionsExportDocument {
	if cfg.Version == 0 {
		cfg.Version = db.ConfigExportVersion
	}
	if cfg.ExportedAt.IsZero() {
		cfg.ExportedAt = time.Now().UTC()
	}
	subs := cfg.Subscriptions
	if subs == nil {
		subs = []db.ChannelExport{}
	}
	return subscriptionsExportDocument{
		Version:       cfg.Version,
		Scope:         "subscriptions",
		ExportedAt:    cfg.ExportedAt,
		Subscriptions: subs,
	}
}

func writeSubscriptionsExportJSON(w io.Writer, cfg db.ConfigExport) error {
	return json.NewEncoder(w).Encode(subscriptionsExportPayload(cfg))
}

type fullExportRuntimeManifest struct {
	Version   int    `json:"version"`
	DataDir   string `json:"data_dir,omitempty"`
	MediaDir  string `json:"media_dir,omitempty"`
	ConfigDir string `json:"config_dir,omitempty"`
	RepoDir   string `json:"repo_dir,omitempty"`
}

func writeFullExportZip(w io.Writer, cfg db.ConfigExport, databasePath string, runtimeFiles []fullExportRuntimeFile, runtimeManifest fullExportRuntimeManifest) error {
	zw := zip.NewWriter(w)
	if strings.TrimSpace(databasePath) != "" {
		if err := writeFullExportDatabaseFile(zw, databasePath); err != nil {
			_ = zw.Close()
			return err
		}
	}
	if err := writeFullExportJSON(zw, cfg); err != nil {
		_ = zw.Close()
		return err
	}
	if err := writeFullExportSubscriptionsJSON(zw, cfg); err != nil {
		_ = zw.Close()
		return err
	}
	if err := writeFullExportRuntimeManifest(zw, runtimeManifest); err != nil {
		_ = zw.Close()
		return err
	}
	for _, file := range runtimeFiles {
		if err := writeFullExportRuntimeConfigFile(zw, file); err != nil {
			return fmt.Errorf("write runtime config %s: %w", file.ArchivePath, err)
		}
	}
	return zw.Close()
}

func exportFullDataSnapshot(path string, layout storage.Layout) (db.ConfigExport, error) {
	store, err := db.OpenReadOnlyLayout(path, layout)
	if err != nil {
		return db.ConfigExport{}, err
	}
	cfg, exportErr := store.ExportFullData()
	closeErr := store.Close()
	return cfg, errors.Join(exportErr, closeErr)
}

func (s *Server) createFullExportDatabaseSnapshot(ctx context.Context) (string, func(), error) {
	if s == nil || s.db == nil {
		return "", func() {}, fmt.Errorf("database is unavailable")
	}
	dir := ""
	if s.cfg != nil {
		dir = strings.TrimSpace(s.cfg.Storage.StateRoot())
	}
	if dir == "" {
		dir = os.TempDir()
	}
	if err := storage.EnsureDirectory(dir, 0o755); err != nil {
		return "", func() {}, fmt.Errorf("create database snapshot dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".igloo-full-export-db-*.db")
	if err != nil {
		return "", func() {}, fmt.Errorf("create database snapshot temp path: %w", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, fmt.Errorf("close database snapshot temp path: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", func() {}, fmt.Errorf("prepare database snapshot path: %w", err)
	}
	if err := s.db.VacuumInto(ctx, path); err != nil {
		_ = os.Remove(path)
		return "", func() {}, fmt.Errorf("vacuum database snapshot: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func (s *Server) configuredExportDir() string {
	if !s.db.BoolSetting("backup_enabled") {
		return ""
	}
	dir, _ := s.db.GetSetting("backup_dir", "")
	dir = strings.TrimSpace(dir)
	if dir != "" && !filepath.IsAbs(dir) {
		slog.Error("configured export dir is not absolute", "dir", dir)
		return ""
	}
	return dir
}

func writeExportFile(ctx context.Context, executor *storage.MediaExecutor, dir, prefix, ext string, write func(io.Writer) error) (string, error) {
	var finalPath string
	err := executor.Run(ctx, storage.MediaLaneBulkForeground, func() error {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return fmt.Errorf("export dir is required")
		}
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("export dir must be absolute: %s", dir)
		}
		if err := storage.EnsureDirectory(dir, 0o755); err != nil {
			return fmt.Errorf("create export dir: %w", err)
		}
		stamp := time.Now().UTC().Format("2006-01-02-150405")
		name := fmt.Sprintf("%s-%s%s", prefix, stamp, ext)
		tmp, err := os.CreateTemp(dir, "."+prefix+"-*.tmp")
		if err != nil {
			return fmt.Errorf("create temp export: %w", err)
		}
		tmpPath := tmp.Name()
		closed := false
		defer func() {
			if !closed {
				_ = tmp.Close()
			}
			_ = os.Remove(tmpPath)
		}()
		if err := write(contextIO{ctx: ctx, dst: tmp}); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tmp.Sync(); err != nil {
			return fmt.Errorf("sync export: %w", err)
		}
		if err := tmp.Close(); err != nil {
			closed = true
			return err
		}
		closed = true
		finalPath = filepath.Join(dir, name)
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return fmt.Errorf("rename export: %w", err)
		}
		if err := storage.SyncDirectory(dir); err != nil {
			return fmt.Errorf("sync export directory: %w", err)
		}
		return nil
	})
	return finalPath, err
}

type fullExportRuntimeFile struct {
	SourcePath  string
	ArchivePath string
}

func writeFullExportJSON(zw *zip.Writer, cfg db.ConfigExport) error {
	f, err := zw.Create("export.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func writeFullExportSubscriptionsJSON(zw *zip.Writer, cfg db.ConfigExport) error {
	f, err := zw.Create("subscriptions.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(subscriptionsExportPayload(cfg))
}

func writeFullExportRuntimeManifest(zw *zip.Writer, manifest fullExportRuntimeManifest) error {
	f, err := zw.Create("runtime.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

func writeFullExportDatabaseFile(zw *zip.Writer, databasePath string) error {
	src, err := os.Open(databasePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = src.Close()
	}()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("database snapshot path is not a regular file")
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = config.DatabaseFilename
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func writeFullExportRuntimeConfigFile(zw *zip.Writer, file fullExportRuntimeFile) error {
	src, err := os.Open(file.SourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = src.Close()
	}()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("runtime config path is not a regular file")
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = file.ArchivePath
	hdr.Method = zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func (s *Server) fullExportRuntimeManifest() fullExportRuntimeManifest {
	if s == nil || s.cfg == nil {
		return fullExportRuntimeManifest{Version: 2}
	}
	return fullExportRuntimeManifest{
		Version:   2,
		DataDir:   s.cfg.Storage.StateRoot(),
		MediaDir:  s.cfg.Storage.MediaRoot(),
		ConfigDir: s.cfg.ConfDir,
		RepoDir:   s.repoDirForRuntimeExport(),
	}
}

func (s *Server) repoDirForRuntimeExport() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if strings.TrimSpace(s.cfg.RepoDir) != "" {
		return s.cfg.RepoDir
	}
	if strings.TrimSpace(s.cfg.StaticDir) != "" {
		return filepath.Dir(s.cfg.StaticDir)
	}
	return ""
}

func (s *Server) collectFullExportRuntimeConfigFiles() ([]fullExportRuntimeFile, error) {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.ConfDir) == "" {
		return nil, nil
	}
	root := filepath.Clean(s.cfg.ConfDir)
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat runtime config root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runtime config root is not a directory: %s", root)
	}
	var files []fullExportRuntimeFile
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk runtime config %s: %w", path, err)
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("resolve runtime config path %s: %w", path, err)
		}
		if skipFullExportRuntimeConfigPath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime config symlink is unsupported: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat runtime config %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("runtime config entry is not a regular file: %s", path)
		}
		files = append(files, fullExportRuntimeFile{
			SourcePath:  path,
			ArchivePath: filepath.ToSlash(filepath.Join("config", rel)),
		})
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ArchivePath < files[j].ArchivePath
	})
	return files, nil
}

func skipFullExportRuntimeConfigPath(rel string) bool {
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	base := filepath.Base(rel)
	if base == "" || strings.HasPrefix(base, ".") {
		return true
	}
	lowerBase := strings.ToLower(base)
	if lowerBase == "subscriptions.json" || (strings.HasPrefix(lowerBase, "subscriptions_") && strings.HasSuffix(lowerBase, ".json")) {
		return true
	}
	for _, prefix := range []string{".auth_users_", ".config_", ".upload_", ".import-media-", ".import-config-"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	isHTMX := r.Header.Get("HX-Request") != ""
	if !requireAdmin(w, r) {
		return
	}

	importErr := func(code int, msg string) {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, `<span class="status-message error">%s</span>`, template.HTMLEscapeString(msg))
			return
		}
		writeJSON(w, code, map[string]any{"error": msg})
	}
	importOK := func(msg string) {
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<span class="status-message success">%s</span><script>setTimeout(function(){window.location.reload()},2000)</script>`, template.HTMLEscapeString(msg))
			return
		}
	}

	if requestContentLengthTooLarge(r, configImportMaxBodyBytes) {
		importErr(http.StatusRequestEntityTooLarge, requestBodyTooLargeMessage)
		return
	}
	limitRequestBody(w, r, configImportMaxBodyBytes)
	if err := r.ParseMultipartForm(configImportMaxMemoryBytes); err != nil {
		if requestBodyTooLarge(err) {
			importErr(http.StatusRequestEntityTooLarge, requestBodyTooLargeMessage)
			return
		}
		importErr(400, "multipart parse error")
		return
	}
	if r.MultipartForm != nil {
		defer func() {
			_ = r.MultipartForm.RemoveAll()
		}()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		importErr(400, "missing file")
		return
	}
	defer func() {
		_ = file.Close()
	}()

	prefix, err := readUploadPrefix(file, 4)
	if err != nil {
		importErr(500, "read error")
		return
	}
	replace := r.FormValue("mode") == "replace"

	if isZipPrefix(prefix) {
		if !replace {
			importErr(400, "replace mode required for restore")
			return
		}
		if err := restore.StageZip(file, header.Size, s.cfg.Storage); err != nil {
			slog.Error("StageZip", "err", err)
			importErr(400, "zip backup error: "+err.Error())
			return
		}
		slog.Info("restore: staged zip backup, exiting for systemd restart")
		if isHTMX {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<span class="status-message success">Restore staged. Igloo is restarting…</span><script>setTimeout(function(){window.location.reload()},12000)</script>`)
		} else {
			writeJSON(w, 200, map[string]any{"success": true, "format": "zip_backup", "restart": true})
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(1)
		}()
		return
	}

	data, err := readLimitedBody(file, configImportJSONMaxBodyBytes)
	if err != nil {
		if requestBodyTooLarge(err) {
			importErr(http.StatusRequestEntityTooLarge, requestBodyTooLargeMessage)
			return
		}
		importErr(500, "read error")
		return
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		importErr(400, "empty file")
		return
	}

	switch trimmed[0] {
	case '{':
		var cfgExport db.ConfigExport
		if err := json.Unmarshal(trimmed, &cfgExport); err != nil {
			importErr(400, "invalid config JSON")
			return
		}
		result, err := s.db.ImportConfig(cfgExport, replace)
		if err != nil {
			slog.Error("ImportConfig", "err", err)
			importErr(500, "import error")
			return
		}
		var parts []string
		if result.AddedChannels > 0 {
			parts = append(parts, fmt.Sprintf("%d subscriptions", result.AddedChannels))
		}
		if result.AddedBookmarks > 0 {
			parts = append(parts, fmt.Sprintf("%d bookmarks", result.AddedBookmarks))
		}
		if result.AddedCategories > 0 {
			parts = append(parts, fmt.Sprintf("%d categories", result.AddedCategories))
		}
		if result.UpdatedSettings > 0 {
			parts = append(parts, fmt.Sprintf("%d settings", result.UpdatedSettings))
		}
		summary := "Import complete"
		if len(parts) > 0 {
			summary = "Imported: " + strings.Join(parts, ", ")
		}
		importOK(summary)
		if !isHTMX {
			writeJSON(w, 200, map[string]any{
				"success": true, "format": "full_config",
				"added_channels": result.AddedChannels, "added_bookmarks": result.AddedBookmarks,
				"added_categories": result.AddedCategories, "updated_settings": result.UpdatedSettings,
				"skipped": result.Skipped,
			})
		}

	case '[':
		var urls []string
		if err := json.Unmarshal(trimmed, &urls); err != nil {
			importErr(400, "invalid subscription array JSON")
			return
		}
		added, skipped := s.importSubscriptionList(r.Context(), urls)
		importOK(fmt.Sprintf("Imported %d channels (%d skipped)", added, skipped))
		if !isHTMX {
			writeJSON(w, 200, map[string]any{"success": true, "format": "subscription_list", "added_channels": added, "skipped": skipped})
		}

	case '<':
		channels := parseOPML(trimmed)
		added, skipped := s.importChannelList(r.Context(), channels)
		importOK(fmt.Sprintf("Imported %d channels (%d skipped)", added, skipped))
		if !isHTMX {
			writeJSON(w, 200, map[string]any{"success": true, "format": "opml", "added_channels": added, "skipped": skipped})
		}

	default:
		importErr(400, "unrecognized format")
	}
}

func readUploadPrefix(file io.ReadSeeker, n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return buf[:read], nil
}

func isZipPrefix(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04
}
