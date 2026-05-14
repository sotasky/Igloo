package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// POST /api/bookmark/{videoID} must emit exactly one sync_change row per
// mutation. Previously the handler called RecordSyncChange while AddBookmark
// also emitted one inside its transaction, producing two rows per add.
func TestHandleBookmarkAdd_EmitsExactlyOneSyncChange(t *testing.T) {
	srv := newTestServer(t)

	body := strings.NewReader(`{"category_id":0}`)
	req := httptest.NewRequest("POST", "/api/bookmark/vid_dup_add", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d — %s", rr.Code, rr.Body.String())
	}

	var n int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = 'bookmark' AND item_id = ?`,
		"vid_dup_add",
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("sync_changes rows after add: got %d, want 1", n)
	}
}

func TestHandleBookmarkAdd_DoesNotRearchiveUnchangedBookmark(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := t.TempDir()
	categoryID, err := srv.db.CreateBookmarkCategory("alice", "Archive", archiveDir)
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}

	relPath := filepath.Join("feed_media", "tw_archive_once_0.jpg")
	fullPath := filepath.Join(srv.cfg.DataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		 VALUES ('feed_media', 'tw_archive_once', 0, ?, 'photo', ?)`,
		relPath, len("image-bytes"),
	); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}

	body := fmt.Sprintf(`{"category_id":%d,"custom_title":"Saved Label","account_handles":["author_handle"],"media_indices":[0]}`, categoryID)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/bookmark/tw_archive_once", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = attachTestAuthRole(req, "alice", "admin")
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status: got %d — %s", i+1, rr.Code, rr.Body.String())
		}
		if i == 0 {
			waitForFile(t, filepath.Join(archiveDir, "author_handle Saved Label 001.jpg"))
		}
	}

	assertFileDoesNotAppear(t, filepath.Join(archiveDir, "author_handle Saved Label 002.jpg"), 300*time.Millisecond)
}

func TestHandleBookmarkCategoryCreateRejectsRelativeArchivePath(t *testing.T) {
	srv := newTestServer(t)

	body := strings.NewReader(`{"name":"Archive","archive_path":"../../tmp/archive"}`)
	req := httptest.NewRequest("POST", "/api/bookmark-categories", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "alice", "admin")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d — %s", rr.Code, rr.Body.String())
	}
}

func TestHandleBookmarkCategoryCreateCreatesArchivePath(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := filepath.Join(t.TempDir(), "bookmarks", "cinema")
	body := fmt.Sprintf(`{"name":"Cinema","archive_path":%q}`, archiveDir)
	req := httptest.NewRequest("POST", "/api/bookmark-categories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "alice", "admin")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}
	info, err := os.Stat(archiveDir)
	if err != nil {
		t.Fatalf("archive path was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("archive path is not a directory")
	}
}

func TestHandleBookmarkCategoryCreateIgnoresArchivePathForNonAdmin(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := filepath.Join(t.TempDir(), "bookmarks", "cinema")
	body := fmt.Sprintf(`{"name":"Cinema","archive_path":%q}`, archiveDir)
	req := httptest.NewRequest("POST", "/api/bookmark-categories", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(archiveDir); !os.IsNotExist(err) {
		t.Fatalf("non-admin archive path should not be created, stat err=%v", err)
	}
	var archivePath string
	if err := srv.db.QueryRow(
		`SELECT COALESCE(archive_path, '') FROM bookmark_categories WHERE user_id = ? AND name = ?`,
		"alice", "Cinema",
	).Scan(&archivePath); err != nil {
		t.Fatalf("select archive path: %v", err)
	}
	if archivePath != "" {
		t.Fatalf("archive_path = %q, want empty", archivePath)
	}
}

func TestHandleBookmarkAddRejectsCategoryOwnedByAnotherUser(t *testing.T) {
	srv := newTestServer(t)

	bobCategoryID, err := srv.db.CreateBookmarkCategory("bob", "Private", "")
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}

	body := fmt.Sprintf(`{"category_id":%d}`, bobCategoryID)
	req := httptest.NewRequest("POST", "/api/bookmark/vid_cross_category", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "admin", "admin")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}
	var count int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM bookmarks WHERE user_id = ? AND video_id = ?`,
		"admin", "vid_cross_category",
	).Scan(&count); err != nil {
		t.Fatalf("count bookmarks: %v", err)
	}
	if count != 0 {
		t.Fatalf("cross-user category created bookmark count = %d, want 0", count)
	}
}

func TestHandleBookmarkAddDoesNotArchiveForNonAdmin(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := t.TempDir()
	categoryID, err := srv.db.CreateBookmarkCategory("alice", "Legacy Archive", archiveDir)
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}
	relPath := filepath.Join("feed_media", "tw_nonadmin_archive_0.jpg")
	fullPath := filepath.Join(srv.cfg.DataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		 VALUES ('feed_media', 'tw_nonadmin_archive', 0, ?, 'photo', ?)`,
		relPath, len("image-bytes"),
	); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}

	body := fmt.Sprintf(`{"category_id":%d,"custom_title":"Saved Label","account_handles":["author_handle"],"media_indices":[0]}`, categoryID)
	req := httptest.NewRequest("POST", "/api/bookmark/tw_nonadmin_archive", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	assertFileDoesNotAppear(t, filepath.Join(archiveDir, "author_handle Saved Label 001.jpg"), 300*time.Millisecond)
}

func TestHandleBookmarkCategoriesListHidesArchivePathForNonAdmin(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.db.CreateBookmarkCategory("alice", "Archive", "/archive/alice"); err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/bookmark-categories", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("non-admin status: got %d - %s", rr.Code, rr.Body.String())
	}
	var nonAdmin struct {
		Categories []map[string]any `json:"categories"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &nonAdmin); err != nil {
		t.Fatalf("decode non-admin response: %v", err)
	}
	if got := nonAdmin.Categories[0]["archive_path"]; got != "" {
		t.Fatalf("non-admin archive_path = %#v, want empty", got)
	}

	req = httptest.NewRequest("GET", "/api/bookmark-categories", nil)
	req = attachTestAuthRole(req, "alice", "admin")
	rr = httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin status: got %d - %s", rr.Code, rr.Body.String())
	}
	var admin struct {
		Categories []map[string]any `json:"categories"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &admin); err != nil {
		t.Fatalf("decode admin response: %v", err)
	}
	if got := admin.Categories[0]["archive_path"]; got != "/archive/alice" {
		t.Fatalf("admin archive_path = %#v, want /archive/alice", got)
	}
}

func TestHandleBookmarkCategoryCreateLeavesArchivePathEmptyByDefault(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/bookmark-categories", strings.NewReader(`{"name":"Cinema"}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rr.Code, rr.Body.String())
	}

	var archivePath string
	if err := srv.db.QueryRow(
		`SELECT COALESCE(archive_path, '') FROM bookmark_categories WHERE user_id = ? AND name = ?`,
		"alice", "Cinema",
	).Scan(&archivePath); err != nil {
		t.Fatalf("select archive path: %v", err)
	}
	if archivePath != "" {
		t.Fatalf("archive_path = %q, want empty", archivePath)
	}
}

func TestHandleBookmarkRemove_EmitsExactlyOneSyncChange(t *testing.T) {
	srv := newTestServer(t)

	// Seed a bookmark via the DB (one sync_change for this add).
	if err := srv.db.AddBookmark("alice", "vid_dup_rm", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Drop that row so we can count only the remove's emission.
	if err := srv.db.ExecRaw(
		`DELETE FROM sync_changes WHERE item_id = ?`,
		"vid_dup_rm",
	); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/api/bookmark/vid_dup_rm", nil)
	req = attachTestAuth(req, "alice")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d — %s", rr.Code, rr.Body.String())
	}

	var n int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = 'bookmark' AND item_id = ?`,
		"vid_dup_rm",
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("sync_changes rows after remove: got %d, want 1", n)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %q not created", filepath.Base(path))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertFileDoesNotAppear(t *testing.T, path string, duration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("unexpected file %q was created", filepath.Base(path))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
