package web

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

var updateContractGoldens = flag.Bool("update-contracts", false, "update web API contract golden files")

func TestAPIContractGoldens(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) any
	}{
		{name: "feed_enriched_item", build: buildFeedEnrichedItemContract},
		{name: "android_sync_latest_generation", build: buildAndroidSyncLatestGenerationContract},
		{name: "android_sync_items_page", build: buildAndroidSyncItemsPageContract},
		{name: "android_sync_assets_page", build: buildAndroidSyncAssetsPageContract},
		{name: "mutation_envelopes", build: buildMutationEnvelopeContract},
		{name: "media_serving_states", build: buildMediaServingContract},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGoldenJSON(t, tt.name, tt.build(t))
		})
	}
}

func TestFeedItemEndpointsReturnEnrichedContractShape(t *testing.T) {
	tests := []struct {
		name string
		path string
		seed func(*testing.T, *testServer)
	}{
		{
			name: "x feed",
			path: "/api/feed/x?limit=5",
			seed: func(t *testing.T, srv *testServer) {
				seedContractFeedFixture(t, srv)
			},
		},
		{
			name: "liked feed",
			path: "/api/feed/liked?limit=5",
			seed: func(t *testing.T, srv *testServer) {
				seedContractFeedFixture(t, srv)
				if err := srv.db.InsertFeedLike("alice", "sample_tweet_main", map[string]string{}); err != nil {
					t.Fatalf("InsertFeedLike: %v", err)
				}
			},
		},
		{
			name: "bookmarked feed",
			path: "/api/feed/bookmarked?limit=5",
			seed: func(t *testing.T, srv *testServer) {
				seedContractFeedFixture(t, srv)
			},
		},
		{
			name: "channel feed",
			path: "/api/channels/twitter_sample_author/feed?limit=5",
			seed: func(t *testing.T, srv *testServer) {
				seedContractFeedFixture(t, srv)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			tt.seed(t, srv)
			body := requestJSON(t, srv, "GET", tt.path, "alice", nil)
			item := findContractFeedItem(t, body)
			assertContractEnrichedFeedItem(t, item)
		})
	}
}

func TestAndroidSyncProfilePayloadUsesChannelProfiles(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, sync_seq)
		VALUES ('twitter_sample_profile', 'sample_profile', 'Stale Channel Name', 'https://x.com/sample_profile', 'twitter', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	fetchedAt := time.UnixMilli(1_700_000_004_000)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_sample_profile",
		Platform:    "twitter",
		Handle:      "sample_profile",
		DisplayName: "Fresh Profile Name",
		AvatarURL:   "https://cdn.example.invalid/fresh-profile.jpg",
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"twitter_sample_profile": {},
		},
	})
	if err != nil {
		t.Fatalf("buildAndroidSyncItems: %v", err)
	}
	var profile map[string]any
	for _, item := range items {
		if item.ItemKind != "channels" || item.ItemID != "twitter_sample_profile" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(item.PayloadJSON, &payload); err != nil {
			t.Fatalf("decode channel payload: %v", err)
		}
		attachments := payload["attachments"].(map[string]any)
		profile = attachments["channel_profile"].(map[string]any)
	}
	if profile == nil {
		t.Fatalf("channel_profile attachment missing from Android sync items: %+v", items)
	}
	if profile["display_name"] != "Fresh Profile Name" || profile["avatar_url"] != "https://cdn.example.invalid/fresh-profile.jpg" {
		t.Fatalf("profile attachment = %#v, want channel_profiles values", profile)
	}
}

func TestAndroidSyncContractFixtureUsesSingleUserFollowState(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidContractRows(t, srv)

	items, _, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"sample_tweet_sync": {},
		},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"twitter_sample_channel": {},
		},
	})
	if err != nil {
		t.Fatalf("buildAndroidSyncItems: %v", err)
	}

	for _, item := range items {
		if item.ItemKind != "channels" || item.ItemID != "twitter_sample_channel" {
			continue
		}
		var bundle deltaBundle
		if err := json.Unmarshal(item.PayloadJSON, &bundle); err != nil {
			t.Fatalf("decode channel payload: %v", err)
		}
		rows := userStateRows(t, bundle, "channel_follows")
		if len(rows) != 1 || rows[0]["followed"] != true {
			t.Fatalf("contract fixture follow state = %#v, want followed single-user row", rows)
		}
		return
	}
	t.Fatalf("contract channel item missing from Android sync items: %+v", items)
}

func TestContractGoldenNormalizationRejectsMissingOrWrongStableFields(t *testing.T) {
	tests := []struct {
		name      string
		normalize func() error
	}{
		{
			name: "missing generation_id",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"created_at_ms":  float64(1_700_000_000_000),
					"source_version": "contract-source",
				})
			},
		},
		{
			name: "wrong generation_id type",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"generation_id":  float64(123),
					"created_at_ms":  float64(1_700_000_000_000),
					"source_version": "contract-source",
				})
			},
		},
		{
			name: "missing created_at_ms",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"generation_id":  "android-sync-contract",
					"source_version": "contract-source",
				})
			},
		},
		{
			name: "wrong created_at_ms type",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"generation_id":  "android-sync-contract",
					"created_at_ms":  "1700000000000",
					"source_version": "contract-source",
				})
			},
		},
		{
			name: "missing source_version",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"generation_id": "android-sync-contract",
					"created_at_ms": float64(1_700_000_000_000),
				})
			},
		},
		{
			name: "wrong source_version type",
			normalize: func() error {
				return normalizeAndroidSyncLatestGenerationFields(map[string]any{
					"generation_id":  "android-sync-contract",
					"created_at_ms":  float64(1_700_000_000_000),
					"source_version": false,
				})
			},
		},
		{
			name: "missing sync_version",
			normalize: func() error {
				return normalizeMutationSuccessFields(map[string]any{"ok": true})
			},
		},
		{
			name: "wrong sync_version type",
			normalize: func() error {
				return normalizeMutationSuccessFields(map[string]any{
					"ok":           true,
					"sync_version": "1",
				})
			},
		},
		{
			name: "missing snapshot_at",
			normalize: func() error {
				return normalizeAndroidSyncItemsPageFields(contractItemsPageWithPrimary("bookmark_metadata", map[string]any{
					"version": float64(1),
				}))
			},
		},
		{
			name: "wrong snapshot_at type",
			normalize: func() error {
				return normalizeAndroidSyncItemsPageFields(contractItemsPageWithPrimary("feed_rank", map[string]any{
					"row_count":   float64(0),
					"rows":        []any{},
					"snapshot_at": "1700000000000",
				}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.normalize(); err == nil {
				t.Fatalf("normalization accepted %s", tt.name)
			}
		})
	}
}

func TestMutationSideTablesAndSyncChangesContract(t *testing.T) {
	srv := newTestServer(t)

	requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id":      "sample_tweet_like_mutation",
		"action":        "set",
		"updated_at_ms": 1_700_000_005_000,
	})
	assertDBCount(t, srv, "feed_likes", `username = 'alice' AND tweet_id = 'sample_tweet_like_mutation'`, 1)
	assertSyncChange(t, srv, "like", "sample_tweet_like_mutation")

	requestJSON(t, srv, "POST", "/api/mutations/bookmark", "alice", map[string]any{
		"video_id":      "sample_tweet_bookmark_mutation",
		"action":        "set",
		"updated_at_ms": 1_700_000_005_001,
	})
	assertDBCount(t, srv, "bookmarks", `user_id = 'alice' AND video_id = 'sample_tweet_bookmark_mutation'`, 1)
	assertSyncChange(t, srv, "bookmark", "sample_tweet_bookmark_mutation")

	requestJSON(t, srv, "PUT", "/api/mutations/progress", "alice", map[string]any{
		"video_id":      "sample_video_progress",
		"position":      12.5,
		"duration":      60,
		"source":        "contract",
		"updated_at_ms": 1_700_000_005_002,
	})
	assertDBCount(t, srv, "watch_history", `user_id = 'alice' AND video_id = 'sample_video_progress'`, 1)
	assertSyncChange(t, srv, "progress", "sample_video_progress")

	requestJSON(t, srv, "POST", "/api/mutations/follow", "alice", map[string]any{
		"channel_id":    "youtube_sample_followed",
		"action":        "set",
		"updated_at_ms": 1_700_000_005_003,
	})
	assertDBCount(t, srv, "channel_follows", `channel_id = 'youtube_sample_followed'`, 1)
	assertSyncChange(t, srv, "follow", "youtube_sample_followed")

	requestJSON(t, srv, "POST", "/api/mutations/seen", "alice", map[string]any{
		"tweet_ids":     []string{"sample_tweet_seen"},
		"updated_at_ms": 1_700_000_005_004,
	})
	assertDBCount(t, srv, "feed_seen", `username = 'alice' AND tweet_id = 'sample_tweet_seen'`, 1)
	assertSyncChange(t, srv, "seen", "sample_tweet_seen")
}

func TestAndroidSyncProfileAssetUsesProfileMediaPath(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	mustWriteFile(t, filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars", "twitter_sample_avatar.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})
	fetchedAt := time.UnixMilli(1_700_000_006_000)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_sample_avatar",
		Platform:    "twitter",
		Handle:      "sample_avatar",
		DisplayName: "Contract Asset",
		AvatarURL:   "https://cdn.example.invalid/asset.jpg",
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	assets, _, err := srv.buildAndroidSyncAssets("alice", db.AndroidSyncDesiredSets{})
	if err != nil {
		t.Fatalf("buildAndroidSyncAssets: %v", err)
	}
	for _, asset := range assets {
		if asset.AssetKind != "avatar" || asset.OwnerID != "twitter_sample_avatar" {
			continue
		}
		if asset.State != "ready" || asset.ServerURL == "" || asset.RequiredReason != "profile" {
			t.Fatalf("profile avatar asset = %+v, want ready profile asset", asset)
		}
		return
	}
	t.Fatalf("profile avatar asset missing from Android sync assets: %+v", assets)
}

func buildFeedEnrichedItemContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	seedContractFeedFixture(t, srv)

	body := requestJSON(t, srv, "GET", "/api/feed/x?limit=5", "alice", nil)
	items := body["items"].([]any)
	var seeded map[string]any
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["tweet_id"] == "sample_tweet_main" {
			seeded = item
			break
		}
	}
	if seeded == nil {
		t.Fatalf("contract feed item missing from response: %#v", body)
	}
	return map[string]any{
		"items": []any{seeded},
	}
}

func seedContractFeedFixture(t *testing.T, srv *testServer) {
	t.Helper()
	published := time.UnixMilli(1_700_000_000_000)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter_sample_author",
		SourceID:     "sample_author",
		Name:         "Contract Author",
		URL:          "https://x.com/sample_author",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel author: %v", err)
	}
	item := model.FeedItem{
		TweetID:                "sample_tweet_main",
		SourceHandle:           "sample_source",
		AuthorHandle:           "sample_author",
		AuthorDisplayName:      "Contract Author",
		BodyText:               "contract body",
		CanonicalURL:           "https://x.com/sample_author/status/sample_tweet_main",
		PublishedAt:            &published,
		QuoteTweetID:           "sample_tweet_quote",
		QuoteAuthorHandle:      "sample_quote",
		QuoteAuthorDisplayName: "Contract Quote",
		QuoteBodyText:          "quoted contract body",
	}
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := srv.db.AddBookmark("alice", "sample_tweet_main", 0, "Contract Bookmark", "", ""); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}
	if err := srv.db.ExecRaw(
		`UPDATE bookmarks SET bookmarked_at = ? WHERE user_id = 'alice' AND video_id = 'sample_tweet_main'`,
		int64(1_700_000_000_500),
	); err != nil {
		t.Fatalf("fix bookmark time: %v", err)
	}
}

func findContractFeedItem(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %#v, want array", body["items"])
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["tweet_id"] == "sample_tweet_main" {
			return item
		}
	}
	t.Fatalf("sample_tweet_main missing from response: %#v", body)
	return nil
}

func assertContractEnrichedFeedItem(t *testing.T, item map[string]any) {
	t.Helper()
	want := map[string]any{
		"channel_id":              "twitter_sample_author",
		"author_avatar_url":       "/api/media/avatar/twitter_sample_author",
		"avatar_url":              "/api/media/avatar/twitter_sample_author",
		"channel_is_followed":     true,
		"subscribe_url":           "https://x.com/sample_author",
		"is_bookmarked":           true,
		"quote_channel_id":        "twitter_sample_quote",
		"quote_author_avatar_url": "/api/media/avatar/twitter_sample_quote",
	}
	for key, expected := range want {
		if got := item[key]; got != expected {
			t.Fatalf("%s = %#v, want %#v in item %#v", key, got, expected, item)
		}
	}
	if _, ok := item["bookmarked_at"]; !ok {
		t.Fatalf("bookmarked_at missing from enriched item: %#v", item)
	}
}

func buildAndroidSyncLatestGenerationContract(t *testing.T) any {
	t.Helper()
	srv := newAndroidSyncTestServer(t)
	seedAndroidContractRows(t, srv)

	body := requestJSON(t, srv, "GET", "/api/android/sync/generation/latest?feed_days=7&youtube_days=7&moments_days=7&story_hours=48", "alice", nil)
	gen, err := contractObjectField(body, "generation")
	if err != nil {
		t.Fatalf("generation contract: %v", err)
	}
	if err := normalizeAndroidSyncLatestGenerationFields(gen); err != nil {
		t.Fatalf("generation contract: %v", err)
	}
	return map[string]any{"generation": gen}
}

func buildAndroidSyncItemsPageContract(t *testing.T) any {
	t.Helper()
	srv := newAndroidSyncTestServer(t)
	seedAndroidContractRows(t, srv)
	items, counts, err := srv.buildAndroidSyncItems("alice", db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{
			"sample_tweet_sync": {},
		},
		Videos: map[string]struct{}{},
		Channels: map[string]struct{}{
			"twitter_sample_channel": {},
			"youtube_sample_profile": {},
		},
	})
	if err != nil {
		t.Fatalf("buildAndroidSyncItems: %v", err)
	}
	for i := range items {
		items[i].Seq = int64(i + 1)
	}
	storeContractGeneration(t, srv, counts, items, nil)

	body := requestJSON(t, srv, "GET", "/api/android/sync/generation/android-sync-contract/items", "alice", nil)
	if err := normalizeAndroidSyncItemsPageFields(body); err != nil {
		t.Fatalf("android sync items contract: %v", err)
	}
	return body
}

func buildAndroidSyncAssetsPageContract(t *testing.T) any {
	t.Helper()
	srv := newAndroidSyncTestServer(t)
	ready := true
	assets := []model.AndroidSyncAsset{
		{
			Seq:            1,
			AssetID:        "asset-contract-ready",
			AssetKind:      "avatar",
			OwnerID:        "twitter_sample_channel",
			OwnerKind:      "channel_profile",
			Bucket:         "profile",
			ContentType:    "image/jpeg",
			SizeBytes:      12,
			SHA256:         "sha-contract-ready",
			State:          "ready",
			RequiredReason: "profile",
			IsAuto:         &ready,
		},
		{
			Seq:            2,
			AssetID:        "asset-contract-missing",
			AssetKind:      "thumbnail",
			OwnerID:        "sample_video_missing",
			OwnerKind:      "videos",
			Bucket:         "videos",
			State:          "server_missing",
			RequiredReason: "retention",
		},
	}
	storeContractGeneration(t, srv, nil, nil, assets)

	return requestJSON(t, srv, "GET", "/api/android/sync/generation/android-sync-contract/assets", "alice", nil)
}

func seedAndroidContractRows(t *testing.T, srv *testServer) {
	t.Helper()
	now := int64(1_700_000_001_000)
	fetchedAt := time.UnixMilli(now)
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, sync_seq)
		VALUES ('twitter_sample_channel', 'sample_channel', 'Contract Channel', 'https://x.com/sample_channel', 'twitter', 11)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'twitter_sample_channel', ?)
	`, now); err != nil {
		t.Fatalf("insert channel follow: %v", err)
	}
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_sample_channel",
		Platform:    "twitter",
		Handle:      "sample_channel",
		DisplayName: "Contract Channel Profile",
		AvatarURL:   "https://cdn.example.invalid/avatar.jpg",
		Followers:   1200,
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("upsert channel profile: %v", err)
	}
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "youtube_sample_profile",
		Platform:    "youtube",
		Handle:      "sample_profile",
		DisplayName: "Contract Profile Only",
		Followers:   42,
		FetchedAt:   &fetchedAt,
	}); err != nil {
		t.Fatalf("upsert profile-only channel: %v", err)
	}
	published := time.UnixMilli(now)
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{{
		TweetID:           "sample_tweet_sync",
		SourceHandle:      "sample_source",
		AuthorHandle:      "sample_channel",
		AuthorDisplayName: "Inline Stale Name",
		AuthorAvatarURL:   "https://inline.example.invalid/stale.jpg",
		BodyText:          "android sync contract body",
		CanonicalURL:      "https://x.com/sample_channel/status/sample_tweet_sync",
		PublishedAt:       &published,
	}}); err != nil {
		t.Fatalf("upsert feed item: %v", err)
	}
	if err := srv.db.AddBookmark("alice", "sample_tweet_sync", 0, "Sync Contract Label", "", ""); err != nil {
		t.Fatalf("bookmark sync item: %v", err)
	}
	if err := srv.db.ExecRaw(
		`UPDATE bookmarks SET bookmarked_at = ? WHERE user_id = 'alice' AND video_id = 'sample_tweet_sync'`,
		now,
	); err != nil {
		t.Fatalf("fix sync bookmark time: %v", err)
	}
	if err := srv.db.ReplaceFeedRankSnapshot("alice", []db.SnapshotRow{{
		TweetID:      "sample_tweet_sync",
		RankPosition: 1,
		FinalScore:   10,
	}}); err != nil {
		t.Fatalf("replace feed rank snapshot: %v", err)
	}
}

func storeContractGeneration(t *testing.T, srv *testServer, counts map[string]int, items []model.AndroidSyncItem, assets []model.AndroidSyncAsset) {
	t.Helper()
	if counts == nil {
		counts = map[string]int{}
	}
	gen := model.AndroidSyncGeneration{
		GenerationID:    "android-sync-contract",
		CreatedAtMs:     1_700_000_002_000,
		Status:          "ready",
		SourceVersion:   "contract-source-version",
		Retention:       map[string]int{"feed_days": 7, "youtube_days": 7, "moments_days": 7, "story_hours": 48},
		ItemCount:       len(items),
		AssetCount:      len(assets),
		ReadyAssetCount: countReadyAssets(assets),
		ContentCounts:   counts,
		AssetCounts:     map[string]int{"avatar": countAssetKind(assets, "avatar"), "thumbnail": countAssetKind(assets, "thumbnail")},
	}
	gen.ServerMissingAssetCount = gen.AssetCount - gen.ReadyAssetCount
	for i := range items {
		items[i].GenerationID = gen.GenerationID
	}
	for i := range assets {
		assets[i].GenerationID = gen.GenerationID
	}
	if err := srv.db.StoreAndroidSyncGeneration(gen, items, assets); err != nil {
		t.Fatalf("StoreAndroidSyncGeneration: %v", err)
	}
}

func buildMutationEnvelopeContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	success := requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id":      "sample_tweet_like",
		"action":        "set",
		"updated_at_ms": 1_700_000_003_000,
	})
	if err := normalizeMutationSuccessFields(success); err != nil {
		t.Fatalf("mutation success contract: %v", err)
	}

	errorBody := requestJSON(t, srv, "POST", "/api/mutations/like", "alice", map[string]any{
		"tweet_id":      "sample_tweet_like",
		"action":        "toggle",
		"updated_at_ms": 1_700_000_003_001,
	})
	return map[string]any{
		"success": success,
		"error":   errorBody,
	}
}

func buildMediaServingContract(t *testing.T) any {
	t.Helper()
	srv := newTestServer(t)
	mustWriteFile(t, filepath.Join(srv.cfg.DataDir, "thumbnails", "avatars", "twitter_sample_media.jpg"), []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43})

	ready := requestSummary(t, srv, "GET", "/api/media/avatar/twitter_sample_media", "")
	missing := requestSummary(t, srv, "GET", "/api/media/avatar/twitter_sample_missing", "")
	return map[string]any{
		"ready":   ready,
		"missing": missing,
	}
}

func requestJSON(t *testing.T, srv *testServer, method, path, user string, body any) map[string]any {
	t.Helper()
	rec := requestRecorder(t, srv, method, path, user, body)
	if rec.Code < 200 || rec.Code >= 500 {
		t.Fatalf("%s %s status = %d body=%s", method, path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s: %v body=%s", method, path, err, rec.Body.String())
	}
	delete(out, "server_time_ms")
	return out
}

func requestSummary(t *testing.T, srv *testServer, method, path, user string) map[string]any {
	t.Helper()
	rec := requestRecorder(t, srv, method, path, user, nil)
	return map[string]any{
		"status":        rec.Code,
		"content_type":  rec.Header().Get("Content-Type"),
		"cache_control": rec.Header().Get("Cache-Control"),
		"body_bytes":    rec.Body.Len(),
	}
}

func requestRecorder(t *testing.T, srv *testServer, method, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

func assertGoldenJSON(t *testing.T, name string, got any) {
	t.Helper()
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden %s: %v", name, err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", "contracts", name+".golden.json")
	if *updateContractGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create contract golden dir: %v", err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write contract golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract golden %s: %v\nRun: go test ./internal/web -run TestAPIContractGoldens -update-contracts -count=1", name, err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("contract golden %s drifted\nRun: go test ./internal/web -run TestAPIContractGoldens/%s -update-contracts -count=1\n\nGot:\n%s", name, name, raw)
	}
}

func normalizeAndroidSyncLatestGenerationFields(gen map[string]any) error {
	if _, err := contractStringField(gen, "generation_id"); err != nil {
		return err
	}
	if _, err := contractNumberField(gen, "created_at_ms"); err != nil {
		return err
	}
	if _, err := contractStringField(gen, "source_version"); err != nil {
		return err
	}
	gen["generation_id"] = "<generated>"
	gen["created_at_ms"] = float64(0)
	gen["source_version"] = "<source-version>"
	return nil
}

func normalizeMutationSuccessFields(success map[string]any) error {
	if _, err := contractNumberField(success, "sync_version"); err != nil {
		return err
	}
	success["sync_version"] = float64(1)
	return nil
}

func normalizeAndroidSyncItemsPageFields(body map[string]any) error {
	items, err := contractArrayField(body, "items")
	if err != nil {
		return err
	}
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("items[%d] = %T, want object", i, raw)
		}
		if _, err := contractNumberField(item, "seq"); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
		switch item["item_kind"] {
		case "bookmark_metadata":
			payload, err := contractObjectField(item, "payload")
			if err != nil {
				return fmt.Errorf("items[%d] bookmark_metadata: %w", i, err)
			}
			primary, err := contractObjectField(payload, "primary")
			if err != nil {
				return fmt.Errorf("items[%d] bookmark_metadata: %w", i, err)
			}
			if _, err := contractNumberField(primary, "snapshot_at"); err != nil {
				return fmt.Errorf("items[%d] bookmark_metadata: %w", i, err)
			}
			primary["snapshot_at"] = float64(0)
		case "feed_rank":
			payload, err := contractObjectField(item, "payload")
			if err != nil {
				return fmt.Errorf("items[%d] feed_rank: %w", i, err)
			}
			primary, err := contractObjectField(payload, "primary")
			if err != nil {
				return fmt.Errorf("items[%d] feed_rank: %w", i, err)
			}
			if _, err := contractNumberField(primary, "snapshot_at"); err != nil {
				return fmt.Errorf("items[%d] feed_rank: %w", i, err)
			}
			primary["snapshot_at"] = float64(0)
		default:
			continue
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i].(map[string]any)
		right := items[j].(map[string]any)
		return left["seq"].(float64) < right["seq"].(float64)
	})
	return nil
}

func contractItemsPageWithPrimary(kind string, primary map[string]any) map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"item_kind": kind,
				"seq":       float64(1),
				"payload": map[string]any{
					"primary": primary,
				},
			},
		},
	}
}

func contractArrayField(obj map[string]any, key string) ([]any, error) {
	value, ok := obj[key]
	if !ok {
		return nil, fmt.Errorf("%s missing", key)
	}
	out, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s = %T, want array", key, value)
	}
	return out, nil
}

func contractObjectField(obj map[string]any, key string) (map[string]any, error) {
	value, ok := obj[key]
	if !ok {
		return nil, fmt.Errorf("%s missing", key)
	}
	out, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s = %T, want object", key, value)
	}
	return out, nil
}

func contractStringField(obj map[string]any, key string) (string, error) {
	value, ok := obj[key]
	if !ok {
		return "", fmt.Errorf("%s missing", key)
	}
	out, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s = %T, want string", key, value)
	}
	return out, nil
}

func contractNumberField(obj map[string]any, key string) (float64, error) {
	value, ok := obj[key]
	if !ok {
		return 0, fmt.Errorf("%s missing", key)
	}
	out, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%s = %T, want number", key, value)
	}
	return out, nil
}

func countReadyAssets(assets []model.AndroidSyncAsset) int {
	n := 0
	for _, asset := range assets {
		if asset.State == "ready" {
			n++
		}
	}
	return n
}

func countAssetKind(assets []model.AndroidSyncAsset, kind string) int {
	n := 0
	for _, asset := range assets {
		if asset.AssetKind == kind {
			n++
		}
	}
	return n
}

func assertDBCount(t *testing.T, srv *testServer, table, where string, want int) {
	t.Helper()
	var got int
	if err := srv.db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + where).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows where %s = %d, want %d", table, where, got, want)
	}
}

func assertSyncChange(t *testing.T, srv *testServer, kind, itemID string) {
	t.Helper()
	var got int
	if err := srv.db.QueryRow(
		`SELECT COUNT(*) FROM sync_changes WHERE type = ? AND item_id = ?`,
		kind,
		itemID,
	).Scan(&got); err != nil {
		t.Fatalf("count sync_changes %s/%s: %v", kind, itemID, err)
	}
	if got == 0 {
		t.Fatalf("sync_changes missing %s/%s", kind, itemID)
	}
}
