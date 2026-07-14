package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

func TestProfileJobPipelinePublishesCanonicalAvatar(t *testing.T) {
	stateRoot := t.TempDir()
	database := newTestWorkerDBAt(t, stateRoot)
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	t.Cleanup(imageServer.Close)

	seedFeedProfileJob(t, database, "twitter_sample_author", "sample_author", "Observed Name")
	manager := &Manager{
		db:          database,
		cfg:         testCfg(stateRoot),
		downloader:  testDownloader(),
		profileKick: make(chan struct{}, 1),
	}
	fetch := func(_ context.Context, channelID string) (*fetchprofile.Profile, error) {
		return &fetchprofile.Profile{
			ChannelID:   channelID,
			Platform:    "twitter",
			Handle:      "sample_author",
			DisplayName: "Fetched Name",
			Bio:         "Fetched metadata",
			AvatarURL:   imageServer.URL + "/avatar.png",
		}, nil
	}

	if !manager.processProfileJobBatch(context.Background(), fetch) {
		t.Fatal("profile job was not claimed")
	}
	profile, err := database.GetChannelProfile("twitter_sample_author")
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if profile == nil || profile.DisplayName != "Fetched Name" || profile.Bio != "Fetched metadata" || profile.FetchedAt == nil {
		t.Fatalf("completed profile = %+v", profile)
	}
	job, err := database.GetProfileJob("twitter_sample_author")
	if err != nil || job == nil || job.CompletedRevision != job.RequestedRevision {
		t.Fatalf("completed profile job = %+v, err=%v", job, err)
	}

	asset, err := database.GetAsset(
		db.BuildAssetID("twitter", "channel", "twitter_sample_author", "avatar", 0),
		"avatar",
	)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if asset == nil || asset.State != db.AssetStateReady || asset.SizeBytes <= 0 || asset.ContentType != "image/png" || asset.FilePath == "" {
		t.Fatalf("ready avatar asset = %+v", asset)
	}
	if want := profileMediaOwnerKey("twitter_sample_author") + "-r1-"; !strings.Contains(asset.FilePath, want) {
		t.Fatalf("avatar path = %q, want revision-specific %q", asset.FilePath, want)
	}
	path, err := manager.cfg.Storage.Path(asset.FilePath)
	if err != nil {
		t.Fatalf("resolve avatar path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("published avatar: %v", err)
	}
}

func TestProfileJobWorkerDiscardsSupersededFetch(t *testing.T) {
	stateRoot := t.TempDir()
	database := newTestWorkerDBAt(t, stateRoot)
	seedFeedProfileJob(t, database, "twitter_test_revisioned", "test_revisioned", "Observed Name")
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testProfilePNGBytes())
	}))
	t.Cleanup(imageServer.Close)
	manager := &Manager{db: database, cfg: testCfg(stateRoot), downloader: testDownloader()}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fetch := func(_ context.Context, channelID string) (*fetchprofile.Profile, error) {
		once.Do(func() { close(started) })
		<-release
		return &fetchprofile.Profile{
			ChannelID:   channelID,
			Platform:    "twitter",
			Handle:      "test_revisioned",
			DisplayName: "Stale Fetch",
			AvatarURL:   imageServer.URL + "/avatar.png",
		}, nil
	}
	done := make(chan struct{})
	go func() {
		manager.processProfileJobBatch(context.Background(), fetch)
		close(done)
	}()
	<-started
	if err := database.RequestProfileJob("twitter_test_revisioned", time.Now().UnixMilli()); err != nil {
		t.Fatalf("RequestProfileJob while fetch active: %v", err)
	}
	close(release)
	<-done

	profile, err := database.GetChannelProfile("twitter_test_revisioned")
	if err != nil || profile == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, profile)
	}
	if profile.DisplayName == "Stale Fetch" {
		t.Fatalf("superseded fetch overwrote observed identity: %+v", profile)
	}
	job, err := database.GetProfileJob("twitter_test_revisioned")
	if err != nil || job == nil {
		t.Fatalf("GetProfileJob: %v / %+v", err, job)
	}
	if job.RequestedRevision != 2 || job.CompletedRevision != 0 || job.LeaseOwner != "" {
		t.Fatalf("superseded durable job = %+v", job)
	}
	if asset, err := database.GetAssetByOwnerIdentity("avatar", "channel", "twitter_test_revisioned", 0); err != nil || asset != nil {
		t.Fatalf("superseded avatar published: %+v err=%v", asset, err)
	}
	assertNoRevisionFile(t, stateRoot, "avatars", "twitter_test_revisioned", 1)
}

func TestProfileJobWorkerRetriesTransientFailureDurably(t *testing.T) {
	database := newTestWorkerDB(t)
	seedFeedProfileJob(t, database, "twitter_retryable", "retryable", "Retryable")
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return nil, errors.New("temporary upstream failure")
	}

	if !manager.processProfileJobBatch(context.Background(), fetch) {
		t.Fatal("profile job was not claimed")
	}
	job, err := database.GetProfileJob("twitter_retryable")
	if err != nil || job == nil {
		t.Fatalf("GetProfileJob: %v / %+v", err, job)
	}
	if job.Attempts != 1 || job.NextAttemptAt == nil || job.LastError == "" || job.LeaseOwner != "" {
		t.Fatalf("durable retry state = %+v", job)
	}
}

func TestProfileJobWorkerReleasesCanceledFetchWithoutChargingAttempt(t *testing.T) {
	database := newTestWorkerDB(t)
	seedFeedProfileJob(t, database, "twitter_canceled", "canceled", "Canceled")
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return nil, context.Canceled
	}

	if !manager.processProfileJobBatch(context.Background(), fetch) {
		t.Fatal("profile job was not claimed")
	}
	job, err := database.GetProfileJob("twitter_canceled")
	if err != nil || job == nil {
		t.Fatalf("GetProfileJob: %v / %+v", err, job)
	}
	if job.Attempts != 0 || job.NextAttemptAt != nil || job.LastError != "" || job.LeaseOwner != "" {
		t.Fatalf("canceled fetch changed durable failure state: %+v", job)
	}
}

func TestProfileJobWorkerRetriesTwitterNotFoundWithoutHidingObservedIdentity(t *testing.T) {
	database := newTestWorkerDB(t)
	seedFeedProfileJob(t, database, "twitter_still_visible", "still_visible", "Observed Name")
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return nil, fetchprofile.ErrNotFound
	}

	if !manager.processProfileJobBatch(context.Background(), fetch) {
		t.Fatal("profile job was not claimed")
	}
	profile, err := database.GetChannelProfile("twitter_still_visible")
	if err != nil || profile == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, profile)
	}
	if profile.Tombstone || profile.DisplayName != "Observed Name" {
		t.Fatalf("twitter not-found hid observed identity: %+v", profile)
	}
	job, err := database.GetProfileJob("twitter_still_visible")
	if err != nil || job == nil || job.Attempts != 1 || job.NextAttemptAt == nil {
		t.Fatalf("durable not-found retry = %+v, err=%v", job, err)
	}
}

func TestProfileJobPublishesMetadataAndAvatarWhileBannerRetries(t *testing.T) {
	stateRoot := t.TempDir()
	database := newTestWorkerDBAt(t, stateRoot)
	const channelID = "twitter_test_preserved"
	seedFeedProfileJob(t, database, channelID, "test_preserved", "Observed Name")
	oldAvatar, oldAvatarPath := storeReadyProfileAsset(t, database, stateRoot, channelID, "avatar", "https://example.test/old-avatar.png", "old")
	oldBanner, oldBannerPath := storeReadyProfileAsset(t, database, stateRoot, channelID, "banner", "https://example.test/old-banner.png", "old")
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/avatar.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(testProfilePNGBytes())
			return
		}
		http.Error(w, "temporary", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	manager := &Manager{db: database, cfg: testCfg(stateRoot), downloader: testDownloader()}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return &fetchprofile.Profile{
			ChannelID: channelID, Platform: "twitter", Handle: "test_preserved",
			DisplayName: "Fetched Immediately", AvatarURL: failing.URL + "/avatar.png",
			BannerURL: failing.URL + "/banner.png",
		}, nil
	}

	manager.processProfileJobBatch(context.Background(), fetch)
	avatar, err := database.GetAsset(oldAvatar.AssetID, oldAvatar.AssetKind)
	if err != nil || avatar == nil || avatar.State != db.AssetStateReady || avatar.SourceURL != failing.URL+"/avatar.png" || avatar.FilePath == oldAvatar.FilePath {
		t.Fatalf("successful avatar was not published: old=%+v got=%+v err=%v", oldAvatar, avatar, err)
	}
	banner, err := database.GetAsset(oldBanner.AssetID, oldBanner.AssetKind)
	if err != nil || banner == nil || banner.State != db.AssetStateReady || banner.SourceURL != oldBanner.SourceURL || banner.FilePath != oldBanner.FilePath || banner.FileMtimeNs != oldBanner.FileMtimeNs {
		t.Fatalf("failed banner replaced ready asset: old=%+v got=%+v err=%v", oldBanner, banner, err)
	}
	if _, err := os.Stat(oldAvatarPath); !os.IsNotExist(err) {
		t.Fatalf("replaced avatar still exists: %v", err)
	}
	if _, err := os.Stat(oldBannerPath); err != nil {
		t.Fatalf("old ready banner removed after failure: %v", err)
	}
	profile, err := database.GetChannelProfile(channelID)
	if err != nil || profile == nil || profile.DisplayName != "Fetched Immediately" || profile.FetchedAt == nil {
		t.Fatalf("fetched metadata was delayed by banner failure: %+v err=%v", profile, err)
	}
	job, err := database.GetProfileJob(channelID)
	if err != nil || job == nil || job.Attempts != 1 || job.CompletedRevision != 0 {
		t.Fatalf("failed replacement job = %+v err=%v", job, err)
	}
	assertNoRevisionFile(t, stateRoot, "banners", channelID, 1)
}

func TestProfileJobReplacementPublishesAtomicallyAndRemovesOldFile(t *testing.T) {
	stateRoot := t.TempDir()
	database := newTestWorkerDBAt(t, stateRoot)
	const channelID = "twitter_test_replaced"
	seedFeedProfileJob(t, database, channelID, "test_replaced", "Observed Name")
	oldAsset, oldPath := storeReadyProfileAsset(t, database, stateRoot, channelID, "avatar", "https://example.test/old-avatar.png", "old")
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testProfilePNGBytes())
	}))
	t.Cleanup(imageServer.Close)
	manager := &Manager{db: database, cfg: testCfg(stateRoot), downloader: testDownloader()}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return &fetchprofile.Profile{
			ChannelID: channelID, Platform: "twitter", Handle: "test_replaced",
			DisplayName: "Fetched Name", AvatarURL: imageServer.URL + "/avatar.png",
		}, nil
	}

	manager.processProfileJobBatch(context.Background(), fetch)
	asset, err := database.GetAsset(oldAsset.AssetID, "avatar")
	if err != nil || asset == nil {
		t.Fatalf("GetAsset: %v / %+v", err, asset)
	}
	if asset.State != db.AssetStateReady || asset.SourceURL == oldAsset.SourceURL || asset.FilePath == oldAsset.FilePath || !strings.Contains(asset.FilePath, profileMediaOwnerKey(channelID)+"-r1") {
		t.Fatalf("published replacement = %+v", asset)
	}
	newPath, err := manager.cfg.Storage.Path(asset.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new avatar missing: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old avatar still present after commit: %v", err)
	}
	profile, err := database.GetChannelProfile(channelID)
	if err != nil || profile == nil || profile.DisplayName != "Fetched Name" || profile.AvatarURL != asset.SourceURL || profile.FetchedAt == nil {
		t.Fatalf("published profile = %+v err=%v", profile, err)
	}
}

func TestProfileJobReusesMatchingReadyAvatarWithoutDownloading(t *testing.T) {
	stateRoot := t.TempDir()
	database := newTestWorkerDBAt(t, stateRoot)
	const channelID = "twitter_test_reused"
	seedFeedProfileJob(t, database, channelID, "test_reused", "Observed Name")
	var hits atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(testProfilePNGBytes())
	}))
	t.Cleanup(imageServer.Close)
	oldAsset, oldPath := storeReadyProfileAsset(t, database, stateRoot, channelID, "avatar", imageServer.URL+"/avatar.png", "ready")
	manager := &Manager{db: database, cfg: testCfg(stateRoot), downloader: testDownloader()}
	fetch := func(context.Context, string) (*fetchprofile.Profile, error) {
		return &fetchprofile.Profile{
			ChannelID: channelID, Platform: "twitter", Handle: "test_reused",
			DisplayName: "Fetched Name", AvatarURL: oldAsset.SourceURL,
		}, nil
	}

	manager.processProfileJobBatch(context.Background(), fetch)
	if hits.Load() != 0 {
		t.Fatalf("matching ready avatar downloaded %d times", hits.Load())
	}
	asset, err := database.GetAsset(oldAsset.AssetID, "avatar")
	if err != nil || asset == nil || asset.FilePath != oldAsset.FilePath || asset.FileMtimeNs != oldAsset.FileMtimeNs {
		t.Fatalf("reused avatar = %+v err=%v", asset, err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("reused avatar removed: %v", err)
	}
	assertNoRevisionFile(t, stateRoot, "avatars", channelID, 1)
}

func TestNormalizeDownloadedImageKeepsRealExtensionAndRejectsUnsafeKey(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "avatar.download")
	if err := os.WriteFile(tmpPath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	finalPath, err := normalizeDownloadedImage(tmpPath, dir, "twitter_avatar")
	if err != nil {
		t.Fatalf("normalize image: %v", err)
	}
	if finalPath != filepath.Join(dir, "twitter_avatar.png") {
		t.Fatalf("final path = %q", finalPath)
	}

	unsafePath := filepath.Join(dir, "unsafe.download")
	if err := os.WriteFile(unsafePath, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatalf("write unsafe image: %v", err)
	}
	if _, err := normalizeDownloadedImage(unsafePath, dir, "../avatar"); err == nil {
		t.Fatal("normalizeDownloadedImage accepted unsafe key")
	}
	if _, err := os.Stat(unsafePath); !os.IsNotExist(err) {
		t.Fatalf("rejected download was not removed: %v", err)
	}
}

func seedFeedProfileJob(t *testing.T, database *db.DB, channelID, handle, displayName string) {
	t.Helper()
	publishedAt := time.Now()
	_, err := database.UpsertFeedItems([]model.FeedItem{{
		TweetID:           "test_post_" + handle,
		SourceHandle:      channelID,
		ChannelID:         channelID,
		AuthorHandle:      handle,
		AuthorDisplayName: displayName,
		PublishedAt:       &publishedAt,
	}})
	if err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
}

func storeReadyProfileAsset(t *testing.T, database *db.DB, stateRoot, channelID, kind, sourceURL, suffix string) (db.Asset, string) {
	t.Helper()
	dirName := "avatars"
	if kind == "banner" {
		dirName = "banners"
	}
	key := filepath.Join("thumbnails", dirName, channelID+"-"+suffix+".png")
	path := filepath.Join(stateRoot, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, testProfilePNGBytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := db.Asset{
		AssetID:   db.BuildAssetID("twitter", "channel", channelID, kind, 0),
		AssetKind: kind, OwnerKind: "channel", OwnerID: channelID,
		SourceURL: sourceURL, FilePath: key, ContentType: "image/png",
		RequiredReason: "identity",
	}
	if err := database.StoreReadyAsset(asset, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetAsset(asset.AssetID, kind)
	if err != nil || stored == nil {
		t.Fatalf("GetAsset: %v / %+v", err, stored)
	}
	return *stored, path
}

func assertNoRevisionFile(t *testing.T, stateRoot, dirName, channelID string, revision int64) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(stateRoot, "thumbnails", dirName, fmt.Sprintf("%s-r%d-*", profileMediaOwnerKey(channelID), revision)))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staged revision files remain: %v", matches)
	}
}

func TestProfileMediaOwnerKeyRemovesPathSyntax(t *testing.T) {
	channelID := "tiktok_" + strings.Repeat(".", 2) + "sample"
	key := profileMediaOwnerKey(channelID)
	if strings.ContainsAny(key, `./\`) || key == channelID {
		t.Fatalf("profile media key = %q", key)
	}
}

func testProfilePNGBytes() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
		0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 'B', 0x60, 0x82,
	}
}
