package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/restore"
	"github.com/screwys/igloo/internal/storage"
)

func writeStateRootMarker(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunImportsCurrentFullExportZipFreshInstall(t *testing.T) {
	dataDir := t.TempDir()
	writeStateRootMarker(t, dataDir)
	configDir := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", dataDir)
	t.Setenv("IGLOO_CONFIG_DIR", configDir)
	repoDir := filepath.Clean("../..")
	t.Setenv("IGLOO_REPO_DIR", repoDir)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"enabled_platforms":`), 0o600); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(t.TempDir(), "igloo-full-test.zip")
	writeFullExportZipFixture(t, zipPath)

	var stdout, stderr strings.Builder
	if code := run([]string{zipPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "format=zip_backup") {
		t.Fatalf("stdout missing format summary: %s", stdout.String())
	}
	if !restore.HasPending(dataDir) {
		t.Fatal("import command did not leave a pending startup restore")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "igloo.db")); !os.IsNotExist(err) {
		t.Fatalf("import command applied database before startup: %v", err)
	}
	if err := restore.ApplyPending(config.Load()); err != nil {
		t.Fatalf("apply staged restore: %v", err)
	}

	store, err := db.OpenPath(filepath.Join(dataDir, "igloo.db"), dataDir)
	if err != nil {
		t.Fatalf("open imported db: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	var categories, bookmarks, likes int
	if err := store.QueryRow(`SELECT COUNT(*) FROM bookmark_categories WHERE name='Watch Later'`).Scan(&categories); err != nil {
		t.Fatalf("category missing: %v", err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id='test_bookmarked_video'`).Scan(&bookmarks); err != nil {
		t.Fatalf("bookmark missing: %v", err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE tweet_id='liked_post'`).Scan(&likes); err != nil {
		t.Fatalf("like missing: %v", err)
	}
	if categories != 1 || bookmarks != 1 || likes != 1 {
		t.Fatalf("fresh import state = categories %d bookmarks %d likes %d, want one each", categories, bookmarks, likes)
	}

	restoredConfig := map[string]string{
		"config.json":                 `{"enabled_platforms":["youtube"]}`,
		"custom.env":                  "CUSTOM_SECRET=example\n",
		"auth_users.json":             `{"admin":{"role":"admin"}}` + "\n",
		"auth_secret":                 "secret-key",
		"cookies/twitter_cookies.txt": "cookie-data",
	}
	for rel, want := range restoredConfig {
		got, err := os.ReadFile(filepath.Join(configDir, rel))
		if err != nil {
			t.Fatalf("read restored config %s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("restored config %s = %q, want %q", rel, string(got), want)
		}
	}
	nginxConf, err := os.ReadFile(filepath.Join(configDir, "nginx.conf"))
	if err != nil {
		t.Fatalf("read restored nginx.conf: %v", err)
	}
	nginxText := string(nginxConf)
	for _, want := range []string{
		filepath.Join(dataDir, "nginx.pid"),
		filepath.Join(configDir, "server.crt"),
		filepath.Join(repoDir, "static"),
	} {
		if !strings.Contains(nginxText, want) {
			t.Fatalf("restored nginx.conf missing rewritten path %q:\n%s", want, nginxText)
		}
	}
	for _, oldPath := range []string{"/old/data", "/old/config", "/old/repo"} {
		if strings.Contains(nginxText, oldPath) {
			t.Fatalf("restored nginx.conf still contains old path %q:\n%s", oldPath, nginxText)
		}
	}

	if _, err := store.GetChannelByID("youtube_test_fresh_channel"); err != nil {
		t.Fatalf("imported channel missing: %v", err)
	}
	if video, err := store.GetVideo("test_bookmarked_video"); err != nil || video == nil {
		t.Fatalf("imported video missing: video=%#v err=%v", video, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close imported database: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{zipPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("second run exit = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if err := restore.ApplyPending(config.Load()); err != nil {
		t.Fatalf("apply second staged restore: %v", err)
	}
	store, err = db.OpenPath(filepath.Join(dataDir, "igloo.db"), dataDir)
	if err != nil {
		t.Fatalf("reopen imported db: %v", err)
	}
	var bookmarkCount int
	if err := store.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id='test_bookmarked_video'`).Scan(&bookmarkCount); err != nil {
		t.Fatalf("bookmark count: %v", err)
	}
	if bookmarkCount != 1 {
		t.Fatalf("after rerun bookmarkCount=%d, want 1", bookmarkCount)
	}
}

func TestRunDoesNotProvisionMissingStateRoot(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "missing-state")
	t.Setenv("IGLOO_DATA_DIR", dataDir)
	t.Setenv("IGLOO_CONFIG_DIR", filepath.Join(base, "config"))
	t.Setenv("IGLOO_REPO_DIR", filepath.Clean("../.."))

	var stdout, stderr strings.Builder
	if code := run([]string{filepath.Join(base, "missing.zip")}, &stdout, &stderr); code != 1 {
		t.Fatalf("run exit = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "state root") {
		t.Fatalf("stderr did not identify the unavailable state root: %s", stderr.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("import created the missing state root: %v", err)
	}
}

func writeFullExportZipFixture(t *testing.T, path string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer func() {
		_ = out.Close()
	}()

	zw := zip.NewWriter(out)
	exportFile, err := zw.Create("export.json")
	if err != nil {
		t.Fatalf("create export.json: %v", err)
	}
	cfg := db.ConfigExport{
		Version:    db.ConfigExportVersion,
		ExportedAt: time.Unix(1700000000, 0).UTC(),
		Settings: map[string]string{
			"starting_page": "feed",
		},
		Subscriptions: []db.ChannelExport{{
			ChannelID: "youtube_test_fresh_channel",
			Name:      "Fresh Channel",
			Platform:  "youtube",
			IsStarred: true,
		}},
		BookmarkCategories: []db.BookmarkCatExport{{
			Name: "Watch Later",
		}},
		Bookmarks: []db.BookmarkExport{{
			VideoID:      "test_bookmarked_video",
			CategoryName: "Watch Later",
			CustomTitle:  "Saved clip",
		}},
		LikedPosts: []db.LikedPostExport{{
			TweetID:      "liked_post",
			AuthorHandle: "author",
			BodyText:     "liked text",
			Platform:     "twitter",
			PublishedAt:  "2026-05-01T12:00:00Z",
		}},
		BookmarkedVideos: []db.BookmarkedVideoExport{{
			VideoID:      "test_bookmarked_video",
			ChannelID:    "youtube_test_fresh_channel",
			OwnerKind:    "youtube_video",
			Title:        "Saved Video",
			Duration:     42,
			PublishedAt:  "2026-05-01T12:00:00Z",
			CategoryName: "Watch Later",
		}},
	}
	if err := json.NewEncoder(exportFile).Encode(cfg); err != nil {
		t.Fatalf("encode export.json: %v", err)
	}
	sourceStateRoot := t.TempDir()
	writeStateRootMarker(t, sourceStateRoot)
	sourceLayout, err := storage.New(sourceStateRoot, "")
	if err != nil {
		t.Fatalf("create source layout: %v", err)
	}
	sourceStore, err := db.Open(sourceLayout)
	if err != nil {
		t.Fatalf("open source database: %v", err)
	}
	if _, err := sourceStore.ImportConfig(cfg, true); err != nil {
		_ = sourceStore.Close()
		t.Fatalf("seed source database: %v", err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}
	databaseFile, err := zw.Create(config.DatabaseFilename)
	if err != nil {
		t.Fatalf("create database entry: %v", err)
	}
	databaseSource, err := os.Open(sourceLayout.DatabasePath())
	if err != nil {
		t.Fatalf("open source database file: %v", err)
	}
	if _, err := io.Copy(databaseFile, databaseSource); err != nil {
		_ = databaseSource.Close()
		t.Fatalf("write database entry: %v", err)
	}
	if err := databaseSource.Close(); err != nil {
		t.Fatalf("close source database file: %v", err)
	}
	runtimeFile, err := zw.Create("runtime.json")
	if err != nil {
		t.Fatalf("create runtime.json: %v", err)
	}
	if _, err := runtimeFile.Write([]byte(`{"version":2,"data_dir":"/old/data","config_dir":"/old/config","repo_dir":"/old/repo"}`)); err != nil {
		t.Fatalf("write runtime.json: %v", err)
	}
	configEntries := map[string]string{
		"config/nginx.conf":                  "pid /old/data/nginx.pid;\nssl_certificate /old/config/server.crt;\nroot /old/repo/static;\n",
		"config/config.json":                 `{"enabled_platforms":["youtube"]}`,
		"config/custom.env":                  "CUSTOM_SECRET=example\n",
		"config/auth_users.json":             `{"admin":{"role":"admin"}}` + "\n",
		"config/auth_secret":                 "secret-key",
		"config/cookies/twitter_cookies.txt": "cookie-data",
	}
	for name, content := range configEntries {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}
