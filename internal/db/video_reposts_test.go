package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestVideoRepostSourcesReplaceAndMomentsTabs(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_followed', 'followed', 'Followed', 'tiktok')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_author', 'author', 'Author', 'tiktok')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'tiktok_followed', 1)`,
		`INSERT INTO videos (video_id, channel_id, title, duration, published_at) VALUES ('orig_1', 'tiktok_followed', 'Original', 0, 1000)`,
		`INSERT INTO videos (video_id, channel_id, title, duration, published_at) VALUES ('repost_1', 'tiktok_author', 'Reposted', 0, 500)`,
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

	if err := d.SetSetting("", "moments_include_reposts_default", "true"); err != nil {
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

func TestMomentsRepostOrderingFallsBackToFirstSeenAt(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_followed', 'followed', 'Followed', 'tiktok')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('tiktok_author', 'author', 'Author', 'tiktok')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'tiktok_followed', 1)`,
		`INSERT INTO videos (video_id, channel_id, title, duration, published_at) VALUES ('orig_newer', 'tiktok_followed', 'Original newer', 0, 2000)`,
		`INSERT INTO videos (video_id, channel_id, title, duration, published_at) VALUES ('repost_older', 'tiktok_author', 'Repost older', 0, 1000)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("", "moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}
	if _, err := d.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "repost_older",
		ReposterChannelID:   "tiktok_followed",
		ReposterHandle:      "followed",
		ReposterDisplayName: "Followed",
		FirstSeenAtMs:       5000,
		UpdatedAtMs:         5000,
	}}); err != nil {
		t.Fatalf("UpsertVideoRepostSources: %v", err)
	}

	all, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos all: %v", err)
	}
	if got := videoIDs(all); len(got) != 2 || got[0] != "orig_newer" || got[1] != "repost_older" {
		t.Fatalf("all ids = %v, want [orig_newer repost_older]", got)
	}
	if all[1].EffectiveMomentAtMs != 5000 {
		t.Fatalf("repost effective time = %d, want first_seen_at_ms 5000", all[1].EffectiveMomentAtMs)
	}
}

func TestInstagramTaggedMomentsUseInstagramSetting(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('instagram_followed', 'followed', 'Followed', 'instagram')`,
		`INSERT INTO channels (channel_id, source_id, name, platform) VALUES ('instagram_author', 'author', 'Author', 'instagram')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'instagram_followed', 1)`,
		`INSERT INTO videos (video_id, channel_id, title, duration, published_at) VALUES ('tagged_1', 'instagram_author', 'Tagged', 0, 1000)`,
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
	if err := d.SetSetting("", "moments_include_reposts_default", "true"); err != nil {
		t.Fatalf("SetSetting moments_include_reposts_default: %v", err)
	}

	tiktokOnly, err := d.GetVideos(GetVideosOpts{Platform: "shorts", MomentsMode: "all", OrderAsc: true, Limit: 10})
	if err != nil {
		t.Fatalf("GetVideos tiktok only: %v", err)
	}
	if got := videoIDs(tiktokOnly); len(got) != 0 {
		t.Fatalf("TikTok repost setting should not include Instagram tagged rows: %v", got)
	}

	if err := d.SetSetting("", "instagram_include_tagged_default", "true"); err != nil {
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
	var handle, avatar string
	var fetchedAt int64
	if err := d.QueryRow(`
		SELECT COALESCE(handle,''), COALESCE(avatar_url,''), fetched_at
		FROM channel_profiles
		WHERE channel_id = 'instagram_collab.one'
	`).Scan(&handle, &avatar, &fetchedAt); err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if handle != "collab.one" || avatar != "" {
		t.Fatalf("profile handle/avatar = %q/%q", handle, avatar)
	}
	if fetchedAt != 0 {
		t.Fatalf("fetched_at = %d, want 0 so profile worker can refresh", fetchedAt)
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
