package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestProcessContentAssetPublishesFingerprintWithoutLegacyRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("sample-image-bytes"))
	}))
	defer server.Close()

	d, m, asset := claimedContentAsset(t, server.URL+"/sample.jpg", "post_media", 0)
	m.processContentAsset(context.Background(), asset)

	ready, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if ready == nil || ready.State != db.AssetStateReady || ready.FilePath == "" || ready.SizeBytes != int64(len("sample-image-bytes")) || len(ready.SHA256) != 64 || ready.FileMtimeNs <= 0 {
		t.Fatalf("ready asset lacks complete fingerprint: %+v", ready)
	}
	path, err := m.cfg.Storage.Path(ready.FilePath)
	if err != nil {
		t.Fatalf("resolve ready path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ready file: %v", err)
	}
}

func TestProcessContentAssetPublishesCommentAvatar(t *testing.T) {
	avatarBytes := []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(avatarBytes)
	}))
	defer server.Close()

	d, m, asset := claimedQueuedAsset(t, db.Asset{
		AssetID:        db.BuildAssetID("youtube", "comment_author", "youtube_test_channel", "avatar", 0),
		AssetKind:      "avatar",
		OwnerKind:      "comment_author",
		OwnerID:        "youtube_test_channel",
		SourceURL:      server.URL + "/avatar.jpg",
		ContentType:    "image/jpeg",
		State:          db.AssetStateQueued,
		RequiredReason: "comment_avatar",
	})
	m.processContentAsset(context.Background(), asset)

	ready, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if ready == nil || ready.State != db.AssetStateReady || ready.FilePath == "" || ready.SizeBytes != int64(len(avatarBytes)) {
		t.Fatalf("ready comment avatar = %+v", ready)
	}
}

func TestProcessContentAssetStoresTransientRetryOnAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	d, m, asset := claimedContentAsset(t, server.URL+"/sample.jpg", "post_media", 1)
	before := time.Now().UnixMilli()
	m.processContentAsset(context.Background(), asset)

	retried, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if retried == nil || retried.State != db.AssetStateQueued || retried.Attempts != 2 || retried.NextAttemptAtMs <= before || retried.LastErrorKind != "temporary" || retried.LeaseOwner != "" {
		t.Fatalf("retry asset = %+v", retried)
	}
}

func TestProcessContentAssetStoresPermanentFailureOnAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	d, m, asset := claimedContentAsset(t, server.URL+"/sample.jpg", "post_media", 0)
	m.processContentAsset(context.Background(), asset)

	failed, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if failed == nil || failed.State != db.AssetStatePermanentMissing || failed.Attempts != 1 || failed.LastErrorKind != "permanent_http" || failed.LeaseOwner != "" {
		t.Fatalf("permanent asset = %+v", failed)
	}
}

func TestProcessContentAssetDoesNotRetryMissingImmutableMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer server.Close()

	d, m, asset := claimedContentAsset(t, server.URL+"/sample.jpg", "post_media", 0)
	m.processContentAsset(context.Background(), asset)

	failed, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if failed == nil || failed.State != db.AssetStatePermanentMissing || failed.Attempts != 1 || failed.LastErrorKind != "not_found" || failed.LeaseOwner != "" {
		t.Fatalf("missing asset = %+v", failed)
	}
}

func TestProcessContentAssetCancellationReleasesClaim(t *testing.T) {
	d, m, asset := claimedContentAsset(t, "https://cdn.example/sample.jpg", "post_media", 0)
	m.failContentAsset(asset, context.Canceled)

	released, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if released == nil || released.State != db.AssetStateQueued || released.Attempts != 0 || released.LeaseOwner != "" || released.LeaseUntilMs != 0 {
		t.Fatalf("released asset = %+v", released)
	}
}

func claimedContentAsset(t *testing.T, sourceURL, kind string, attempts int) (*db.DB, *Manager, db.Asset) {
	t.Helper()
	return claimedQueuedAsset(t, db.Asset{
		AssetID: db.BuildAssetID("twitter", "tweet", "sample_tweet", kind, 0), AssetKind: kind, OwnerKind: "tweet", OwnerID: "sample_tweet",
		MediaIndex: 0, SourceURL: sourceURL, ContentType: "image/jpeg",
		State: db.AssetStateQueued, RequiredReason: "retention", Attempts: attempts,
	})
}

func claimedQueuedAsset(t *testing.T, asset db.Asset) (*db.DB, *Manager, db.Asset) {
	t.Helper()
	stateRoot := t.TempDir()
	d := newTestWorkerDBAt(t, stateRoot)
	now := time.Now().UnixMilli()
	if err := d.DeclareAsset(asset, now); err != nil {
		t.Fatalf("insert queued test asset: %v", err)
	}
	if asset.Attempts > 0 {
		if err := d.ExecRaw(`
			UPDATE media_objects SET attempts = ?
			WHERE object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ?)
		`, asset.Attempts, asset.AssetID); err != nil {
			t.Fatalf("set queued test attempts: %v", err)
		}
	}
	claimed, err := d.ClaimContentAssetDownloadBatch(db.LeaseOptions{
		Owner: "worker-a", NowMs: now + 1, LeaseMs: time.Minute.Milliseconds(), Limit: 1,
	})
	if err != nil {
		t.Fatalf("ClaimContentAssetDownloadBatch: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d assets, want 1", len(claimed))
	}
	m := &Manager{
		db: d, cfg: testCfg(stateRoot), downloader: testDownloader(),
		activity: NewActivityRing(10), feedActivity: NewActivityRing(10),
	}
	return d, m, claimed[0]
}
