package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestCacheYouTubeCommentAvatarsDownloadsPublicThumbnail(t *testing.T) {
	oldLookup := lookupStoredMediaHost
	lookupStoredMediaHost = func(string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	t.Cleanup(func() { lookupStoredMediaHost = oldLookup })

	avatarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00})
	}))
	defer avatarServer.Close()

	dir := t.TempDir()
	m := &Manager{
		cfg:        testCfg(dir),
		downloader: testTwimgAvatarDownloader(avatarServer),
	}
	got := m.CacheYouTubeCommentAvatars(context.Background(), []db.CommentInput{{
		AuthorID:        "UCcommenterAvatar",
		AuthorThumbnail: "https://yt3.ggpht.com/avatar.jpg",
	}})
	if got != 1 {
		t.Fatalf("cached avatars = %d, want 1", got)
	}
	if !hasConventionalMediaFile(filepath.Join(dir, "thumbnails", "avatars"), "youtube_UCcommenterAvatar") {
		t.Fatal("expected cached YouTube commenter avatar")
	}
}
