package db

import (
	"slices"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestVideoRepostSourcesReplaceAndMomentsTabs(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_followed', 'followed', 'Followed', 'tiktok')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_author', 'author', 'Author', 'tiktok')`,
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('tiktok_followed', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('orig_1', 'tiktok_followed', 'tiktok_video', 'Original', 0, 1000)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('repost_1', 'tiktok_author', 'tiktok_video', 'Reposted', 0, 500)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "repost_1",
		ReposterChannelID:   "tiktok_followed",
		ReposterHandle:      "followed",
		ReposterDisplayName: "Followed",
		RepostedAtMs:        2000,
		FirstSeenAtMs:       1500,
		UpdatedAtMs:         2500,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	following, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "following", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos following: %v", err)
	}
	if got := videoIDs(following); len(got) != 1 || got[0] != "orig_1" {
		t.Fatalf("following ids = %v, want [orig_1]", got)
	}

	allOff, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all off: %v", err)
	}
	if got := videoIDs(allOff); len(got) != 1 || got[0] != "orig_1" {
		t.Fatalf("all off ids = %v, want [orig_1]", got)
	}
	if allOff[0].RepostIntroduced || allOff[0].ReposterHandle != "" || allOff[0].RepostCount != 0 {
		t.Fatalf("all off should not add repost metadata: %+v", allOff[0])
	}

	if err := d.SetSetting("moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}
	allOn, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all on: %v", err)
	}
	if got := videoIDs(allOn); len(got) != 2 || got[0] != "orig_1" || got[1] != "repost_1" {
		t.Fatalf("all on ids = %v, want [orig_1 repost_1]", got)
	}
	if !allOn[1].RepostIntroduced || allOn[1].ReposterHandle != "followed" || allOn[1].RepostCount != 1 {
		t.Fatalf("unexpected repost metadata: %+v", allOn[1])
	}

	if err := d.ReplaceVideoRepostSources("repost_1", nil); err != nil {
		t.Fatalf("ReplaceVideoRepostSources clear: %v", err)
	}
	cleared, err := d.GetVideoRepostSources("repost_1")
	if err != nil {
		t.Fatalf("GetVideoRepostSources: %v", err)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared repost sources len = %d, want 0", len(cleared))
	}
}

func TestMomentsRepostOrderingUsesActualTimestampOrFirstSeenTime(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_sample_source', 'sample_source', 'Sample Source', 'tiktok')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_sample_second_source', 'sample_second_source', 'Sample Second Source', 'tiktok')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_sample_author', 'sample_author', 'Sample Author', 'tiktok')`,
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('tiktok_sample_source', 1)`,
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('tiktok_sample_second_source', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('sample_orig_newer', 'tiktok_sample_source', 'tiktok_video', 'Sample original newer', 0, 2000)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('sample_repost_older', 'tiktok_sample_author', 'tiktok_video', 'Sample repost older', 0, 1000)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('sample_repost_dated', 'tiktok_sample_author', 'tiktok_video', 'Sample repost dated', 0, 500)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{
		{
			VideoID:             "sample_repost_older",
			ReposterChannelID:   "tiktok_sample_source",
			ReposterHandle:      "sample_source",
			ReposterDisplayName: "Sample Source",
			FirstSeenAtMs:       5000,
			UpdatedAtMs:         5000,
		},
		{
			VideoID:             "sample_repost_dated",
			ReposterChannelID:   "tiktok_sample_source",
			ReposterHandle:      "sample_source",
			ReposterDisplayName: "Sample Source",
			RepostedAtMs:        6000,
			FirstSeenAtMs:       7000,
			UpdatedAtMs:         7000,
		},
		{
			VideoID:             "sample_repost_dated",
			ReposterChannelID:   "tiktok_sample_second_source",
			ReposterHandle:      "sample_second_source",
			ReposterDisplayName: "Sample Second Source",
			FirstSeenAtMs:       8000,
			UpdatedAtMs:         8000,
		},
	}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	all, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all: %v", err)
	}
	if got := videoIDs(all); len(got) != 3 || got[0] != "sample_orig_newer" || got[1] != "sample_repost_older" || got[2] != "sample_repost_dated" {
		t.Fatalf("all ids = %v, want [sample_orig_newer sample_repost_older sample_repost_dated]", got)
	}
	if all[1].EffectiveMomentAtMs != 5000 {
		t.Fatalf("unknown-date repost effective time = %d, want discovery time 5000", all[1].EffectiveMomentAtMs)
	}
	if all[2].EffectiveMomentAtMs != 6000 || all[2].ReposterChannelID != "tiktok_sample_source" {
		t.Fatalf("dated repost = %+v, want actual repost timestamp and dated source", all[2])
	}
	count, err := d.GetVideoCount(GetVideosOpts{Platform: "shorts", MomentsMode: "all"})
	if err != nil {
		t.Fatalf("GetVideoCount all: %v", err)
	}
	if count != len(all) {
		t.Fatalf("GetVideoCount all = %d, want %d", count, len(all))
	}
	for i, videoID := range []string{"sample_orig_newer", "sample_repost_older", "sample_repost_dated"} {
		ordinal, visible, err := d.GetShortsOrdinal(videoID, "all")
		if err != nil {
			t.Fatalf("GetShortsOrdinal %s: %v", videoID, err)
		}
		if !visible || ordinal != i+1 {
			t.Fatalf("GetShortsOrdinal %s = %d, %v; want %d, true", videoID, ordinal, visible, i+1)
		}
	}
	for videoID, wantSortAt := range map[string]int64{
		"sample_orig_newer":   2000,
		"sample_repost_older": 5000,
		"sample_repost_dated": 6000,
	} {
		sortAt, visible, err := d.GetShortsVisibleSortAt(videoID, "all")
		if err != nil {
			t.Fatalf("GetShortsVisibleSortAt %s: %v", videoID, err)
		}
		if !visible || sortAt != wantSortAt {
			t.Fatalf("GetShortsVisibleSortAt %s = %d, %v; want %d, true", videoID, sortAt, visible, wantSortAt)
		}
	}
}

func TestMomentsVisibilitySkipsMutedOwnersAndRepostSources(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.SetSetting("moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('tiktok_sample_source_direct_open', 'sample_source_direct_open', 'Direct Open', 'tiktok'),
			('tiktok_sample_muted_direct', 'sample_muted_direct', 'Direct Muted', 'tiktok'),
			('tiktok_sample_source_owner', 'sample_source_owner', 'Source Owner', 'tiktok'),
			('tiktok_sample_muted_reposter', 'sample_muted_reposter', 'Muted Reposter', 'tiktok'),
			('tiktok_sample_reposter_open', 'sample_reposter_open', 'Open Reposter', 'tiktok'),
			('tiktok_sample_muted_source_owner', 'sample_muted_source_owner', 'Muted Source Owner', 'tiktok')`,
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES
			('tiktok_sample_source_direct_open', 1),
			('tiktok_sample_muted_direct', 1),
			('tiktok_sample_muted_reposter', 1),
			('tiktok_sample_reposter_open', 1)`,
		`INSERT INTO muted_channels (channel_id, muted_at) VALUES
			('tiktok_sample_muted_direct', 1),
			('tiktok_sample_muted_reposter', 1),
			('tiktok_sample_muted_source_owner', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES
			('sample_video_direct_open', 'tiktok_sample_source_direct_open', 'tiktok_video', 'Direct Open', 0, 100),
			('sample_video_direct_muted', 'tiktok_sample_muted_direct', 'tiktok_video', 'Direct Muted', 0, 200),
			('sample_video_source_only_muted', 'tiktok_sample_source_owner', 'tiktok_video', 'Muted Source', 0, 300),
			('sample_video_source_open', 'tiktok_sample_source_owner', 'tiktok_video', 'Open Source', 0, 400),
			('sample_video_shared_sources', 'tiktok_sample_source_owner', 'tiktok_video', 'Shared Sources', 0, 500),
			('sample_video_muted_owner_reposted', 'tiktok_sample_muted_source_owner', 'tiktok_video', 'Muted Owner', 0, 600)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{
		{
			VideoID: "sample_video_source_only_muted", ReposterChannelID: "tiktok_sample_muted_reposter",
			ReposterHandle: "sample_muted_reposter", RepostedAtMs: 650, FirstSeenAtMs: 650,
		},
		{
			VideoID: "sample_video_source_open", ReposterChannelID: "tiktok_sample_reposter_open",
			ReposterHandle: "sample_reposter_open", RepostedAtMs: 700, FirstSeenAtMs: 700,
		},
		{
			VideoID: "sample_video_shared_sources", ReposterChannelID: "tiktok_sample_muted_reposter",
			ReposterHandle: "sample_muted_reposter", RepostedAtMs: 800, FirstSeenAtMs: 800,
		},
		{
			VideoID: "sample_video_shared_sources", ReposterChannelID: "tiktok_sample_reposter_open",
			ReposterHandle: "sample_reposter_open", RepostedAtMs: 900, FirstSeenAtMs: 900,
		},
		{
			VideoID: "sample_video_muted_owner_reposted", ReposterChannelID: "tiktok_sample_reposter_open",
			ReposterHandle: "sample_reposter_open", RepostedAtMs: 950, FirstSeenAtMs: 950,
		},
	}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	all, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all: %v", err)
	}
	if got, want := videoIDs(all), []string{"sample_video_direct_open", "sample_video_source_open", "sample_video_shared_sources"}; !slices.Equal(got, want) {
		t.Fatalf("all ids = %v, want %v", got, want)
	}
	if all[2].ReposterChannelID != "tiktok_sample_reposter_open" || all[2].RepostCount != 1 {
		t.Fatalf("shared source presentation = %+v, want unmuted source only", all[2])
	}

	count, err := d.GetVideoCount(GetVideosOpts{Platform: "shorts", MomentsMode: "all"})
	if err != nil {
		t.Fatalf("GetVideoCount all: %v", err)
	}
	if count != len(all) {
		t.Fatalf("GetVideoCount all = %d, want %d", count, len(all))
	}

	for index, videoID := range []string{"sample_video_direct_open", "sample_video_source_open", "sample_video_shared_sources"} {
		ordinal, visible, err := d.GetShortsOrdinal(videoID, "all")
		if err != nil {
			t.Fatalf("GetShortsOrdinal %s: %v", videoID, err)
		}
		if !visible || ordinal != index+1 {
			t.Fatalf("GetShortsOrdinal %s = %d, %v; want %d, true", videoID, ordinal, visible, index+1)
		}
	}
	for _, videoID := range []string{
		"sample_video_direct_muted",
		"sample_video_source_only_muted",
		"sample_video_muted_owner_reposted",
	} {
		ordinal, visible, err := d.GetShortsOrdinal(videoID, "all")
		if err != nil {
			t.Fatalf("GetShortsOrdinal hidden %s: %v", videoID, err)
		}
		if visible || ordinal != 0 {
			t.Fatalf("GetShortsOrdinal hidden %s = %d, %v; want 0, false", videoID, ordinal, visible)
		}
		if _, visible, err := d.GetShortsVisibleSortAt(videoID, "all"); err != nil || visible {
			t.Fatalf("GetShortsVisibleSortAt hidden %s = visible %v, err %v; want false, nil", videoID, visible, err)
		}
	}

	targetID, ordinal, found, err := d.GetNearestShortsCursorTarget("sample_video_source_only_muted", "all", 750)
	if err != nil {
		t.Fatalf("GetNearestShortsCursorTarget: %v", err)
	}
	if !found || targetID != "sample_video_shared_sources" || ordinal != 3 {
		t.Fatalf("nearest muted-source target = %q, %d, %v; want sample_video_shared_sources, 3, true", targetID, ordinal, found)
	}

	following, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "following", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos following: %v", err)
	}
	if got, want := videoIDs(following), []string{"sample_video_direct_open"}; !slices.Equal(got, want) {
		t.Fatalf("following ids = %v, want %v", got, want)
	}
	followingCount, err := d.GetVideoCount(GetVideosOpts{Platform: "shorts", MomentsMode: "following"})
	if err != nil {
		t.Fatalf("GetVideoCount following: %v", err)
	}
	if followingCount != 1 {
		t.Fatalf("GetVideoCount following = %d, want 1", followingCount)
	}
	if ordinal, visible, err := d.GetShortsOrdinal("sample_video_direct_muted", "following"); err != nil || visible || ordinal != 0 {
		t.Fatalf("GetShortsOrdinal muted following = %d, %v, %v; want 0, false, nil", ordinal, visible, err)
	}
}

func TestInstagramTaggedMomentsUseInstagramSetting(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('instagram_followed', 'followed', 'Followed', 'instagram')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('instagram_author', 'author', 'Author', 'instagram')`,
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('instagram_followed', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at) VALUES ('tagged_1', 'instagram_author', 'instagram_reel', 'Tagged', 0, 1000)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "tagged_1",
		ReposterChannelID:   "instagram_followed",
		ReposterHandle:      "followed",
		ReposterDisplayName: "Followed",
		FirstSeenAtMs:       2000,
		UpdatedAtMs:         2000,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	if err := d.SetSetting("moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	tiktokOnly, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos tiktok only: %v", err)
	}
	if got := videoIDs(tiktokOnly); len(got) != 0 {
		t.Fatalf("TikTok repost setting should not include Instagram tagged rows: %v", got)
	}

	if err := d.SetSetting("instagram_include_tagged_default", "true"); err != nil {
		t.Fatalf("SetSetting instagram_include_tagged_default: %v", err)
	}
	all, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos with Instagram tagged: %v", err)
	}
	if got := videoIDs(all); len(got) != 1 || got[0] != "tagged_1" {
		t.Fatalf("Instagram tagged ids = %v, want [tagged_1]", got)
	}
	if !all[0].RepostIntroduced || all[0].ReposterHandle != "followed" {
		t.Fatalf("unexpected tagged metadata: %+v", all[0])
	}
	count, err := d.GetVideoCount(GetVideosOpts{Platform: "shorts", MomentsMode: "all"})
	if err != nil {
		t.Fatalf("GetVideoCount with Instagram tagged: %v", err)
	}
	if count != 1 {
		t.Fatalf("Instagram tagged count = %d, want 1", count)
	}
	ordinal, ok, err := d.GetShortsOrdinal("tagged_1", "all")
	if err != nil {
		t.Fatalf("GetShortsOrdinal with Instagram tagged: %v", err)
	}
	if !ok || ordinal != 1 {
		t.Fatalf("Instagram tagged ordinal = %d ok=%v, want 1 true", ordinal, ok)
	}
}

func TestVideoRepostSourcesExposeAuthorLabel(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{
		{
			VideoID:             "display_name_repost",
			ReposterChannelID:   "tiktok_display",
			ReposterHandle:      "display_handle",
			ReposterDisplayName: "Display Name",
			FirstSeenAtMs:       100,
		},
		{
			VideoID:           "handle_repost",
			ReposterChannelID: "tiktok_handle",
			ReposterHandle:    "@handle_only",
			FirstSeenAtMs:     101,
		},
		{
			VideoID:           "missing_label_repost",
			ReposterChannelID: "tiktok_missing",
			FirstSeenAtMs:     102,
		},
	}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	byVideo, err := d.GetVideoRepostSourcesForVideoIDs([]string{
		"display_name_repost",
		"handle_repost",
		"missing_label_repost",
	})
	if err != nil {
		t.Fatalf("GetVideoRepostSourcesForVideoIDs: %v", err)
	}

	if got := byVideo["display_name_repost"][0].RepostAuthorLabel; got != "Display Name" {
		t.Fatalf("display-name label = %q, want Display Name", got)
	}
	if got := byVideo["handle_repost"][0].RepostAuthorLabel; got != "@handle_only" {
		t.Fatalf("handle label = %q, want @handle_only", got)
	}
	if got := byVideo["missing_label_repost"][0].RepostAuthorLabel; got != "@missing" {
		t.Fatalf("missing label = %q, want @missing", got)
	}
}

func TestEnsureTikTokChannelForRepostCanonicalizesInternalAuthorID(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.EnsureTikTokChannelForRepost("tiktok_7000000000000000001", "sample_creator_42", "Sample Creator"); err != nil {
		t.Fatalf("EnsureTikTokChannelForRepost: %v", err)
	}

	ch, err := d.GetChannelByID("tiktok_sample_creator_42")
	if err != nil {
		t.Fatalf("GetChannelByID canonical: %v", err)
	}
	if ch.SourceID != "sample_creator_42" || ch.Name != "Sample Creator" || ch.URL != "https://www.tiktok.com/@sample_creator_42" {
		t.Fatalf("unexpected canonical channel: %+v", ch)
	}
	var stale int
	if err := d.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id = 'tiktok_7000000000000000001'`).Scan(&stale); err != nil {
		t.Fatalf("count stale channel: %v", err)
	}
	if stale != 0 {
		t.Fatalf("stale numeric channel count = %d, want 0", stale)
	}
	var fetchedAt int64
	if err := d.QueryRow(`SELECT fetched_at FROM channel_profiles WHERE channel_id = 'tiktok_sample_creator_42'`).Scan(&fetchedAt); err != nil {
		t.Fatalf("query fetched_at: %v", err)
	}
	if fetchedAt != 0 {
		t.Fatalf("placeholder fetched_at = %d, want 0 so profile worker refreshes it", fetchedAt)
	}
}

func TestEnsureTikTokChannelForRepostDropsInternalAuthorIDWithoutHandle(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.EnsureTikTokChannelForRepost("tiktok_7000000000000000001", "", "Sample Creator"); err != nil {
		t.Fatalf("EnsureTikTokChannelForRepost: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&count); err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if count != 0 {
		t.Fatalf("channel count = %d, want 0", count)
	}
}

func TestEnsureInstagramChannelForTaggedPreservesDottedHandleWithoutMediaAvatar(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.EnsureInstagramChannelForTagged("instagram_numeric_placeholder", "collab.one", "Collab One", "https://cdn.example/collab.jpg"); err != nil {
		t.Fatalf("EnsureInstagramChannelForTagged: %v", err)
	}

	ch, err := d.GetChannelByID("instagram_collab.one")
	if err != nil {
		t.Fatalf("GetChannelByID: %v", err)
	}
	if ch.SourceID != "collab.one" || ch.Name != "Collab One" || ch.URL != "https://www.instagram.com/collab.one/" || ch.Platform != "instagram" {
		t.Fatalf("unexpected channel: %+v", ch)
	}
	var handle string
	var fetchedAt int64
	if err := d.QueryRow(`
		SELECT COALESCE(handle,''), fetched_at
		FROM channel_profiles
		WHERE channel_id = 'instagram_collab.one'
	`).Scan(&handle, &fetchedAt); err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if handle != "collab.one" {
		t.Fatalf("profile handle = %q", handle)
	}
	if fetchedAt != 0 {
		t.Fatalf("fetched_at = %d, want 0 so profile worker can refresh", fetchedAt)
	}
	avatar, err := d.GetAssetByOwnerIdentity("avatar", "channel", "instagram_collab.one", 0)
	if err != nil || avatar != nil {
		t.Fatalf("unexpected canonical avatar = %+v / %v", avatar, err)
	}
}

func TestUpsertVideoRepostSourcesPreservesInstagramNumericHandle(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:           "instagram_post_NUMERIC",
		ReposterChannelID: "instagram_1234567890123456",
		ReposterHandle:    "1234567890123456",
		FirstSeenAtMs:     100,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}
	rows, err := d.GetVideoRepostSources("instagram_post_NUMERIC")
	if err != nil {
		t.Fatalf("GetVideoRepostSources: %v", err)
	}
	if len(rows) != 1 || rows[0].ReposterChannelID != "instagram_1234567890123456" || rows[0].ReposterHandle != "1234567890123456" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpsertVideoRepostSourcesPreservesKnownEventWhenObservationOmitsIt(t *testing.T) {
	d := openWritableTestDB(t)
	row := model.VideoRepostSource{
		VideoID:           "sample_repost",
		ReposterChannelID: "tiktok_sample_source",
		ReposterHandle:    "sample_source",
		RepostedAtMs:      2000,
		FirstSeenAtMs:     1000,
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{row}); err != nil {
		t.Fatalf("seed UpsertVideoRepostSources: %v", err)
	}

	row.RepostedAtMs = 0
	row.FirstSeenAtMs = 0
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{row}); err != nil {
		t.Fatalf("missing-event UpsertVideoRepostSources: %v", err)
	}
	rows, err := d.GetVideoRepostSources(row.VideoID)
	if err != nil {
		t.Fatalf("GetVideoRepostSources: %v", err)
	}
	if len(rows) != 1 || rows[0].RepostedAtMs != 2000 {
		t.Fatalf("rows after missing event = %+v", rows)
	}

	row.RepostedAtMs = 3000
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{row}); err != nil {
		t.Fatalf("new-event UpsertVideoRepostSources: %v", err)
	}
	rows, err = d.GetVideoRepostSources(row.VideoID)
	if err != nil {
		t.Fatalf("GetVideoRepostSources after new event: %v", err)
	}
	if len(rows) != 1 || rows[0].RepostedAtMs != 3000 {
		t.Fatalf("rows after new event = %+v", rows)
	}
}

func TestReplaceVideoRepostSourcesForReposterOnlyReplacesThatSource(t *testing.T) {
	d := openWritableTestDB(t)

	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{
		{VideoID: "old_for_source", ReposterChannelID: "instagram_followed", ReposterHandle: "followed", FirstSeenAtMs: 100},
		{VideoID: "keep_for_source", ReposterChannelID: "instagram_followed", ReposterHandle: "followed", FirstSeenAtMs: 200},
		{VideoID: "old_for_source", ReposterChannelID: "instagram_other", ReposterHandle: "other", FirstSeenAtMs: 300},
	}); err != nil {
		t.Fatalf("seed UpsertVideoRepostSources: %v", err)
	}

	removed, err := d.ReplaceVideoRepostSourcesForReposter("instagram_followed", []model.VideoRepostSource{{
		VideoID:             "keep_for_source",
		ReposterChannelID:   "instagram_followed",
		ReposterHandle:      "followed",
		ReposterDisplayName: "Followed",
		FirstSeenAtMs:       250,
	}})
	if err != nil {
		t.Fatalf("ReplaceVideoRepostSourcesForReposter: %v", err)
	}
	if len(removed) != 1 || removed[0] != "old_for_source" {
		t.Fatalf("removed = %v, want [old_for_source]", removed)
	}

	oldRows, err := d.GetVideoRepostSources("old_for_source")
	if err != nil {
		t.Fatalf("GetVideoRepostSources old: %v", err)
	}
	if len(oldRows) != 1 || oldRows[0].ReposterChannelID != "instagram_other" {
		t.Fatalf("old rows = %+v, want only instagram_other", oldRows)
	}
	keepRows, err := d.GetVideoRepostSources("keep_for_source")
	if err != nil {
		t.Fatalf("GetVideoRepostSources keep: %v", err)
	}
	if len(keepRows) != 1 || keepRows[0].ReposterChannelID != "instagram_followed" || keepRows[0].ReposterDisplayName != "Followed" {
		t.Fatalf("keep rows = %+v", keepRows)
	}
}

func videoIDs(videos []model.Video) []string {
	ids := make([]string, len(videos))
	for i := range videos {
		ids[i] = videos[i].VideoID
	}
	return ids
}
