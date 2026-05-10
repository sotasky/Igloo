package worker

import (
	"fmt"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

func TestMergeVideoRefsSortsRepostsIntoSourceWindow(t *testing.T) {
	var originals []download.VideoRef
	for i := 0; i < 20; i++ {
		originals = append(originals, download.VideoRef{
			VideoID:       fmt.Sprintf("orig_%02d", i),
			PublishedAtMs: int64(1000 + i),
		})
	}
	reposts := []download.VideoRef{{
		VideoID:        "repost_new",
		IsRepost:       true,
		ChannelID:      "tiktok_author",
		RepostedAtMs:   5000,
		ReposterHandle: "source",
	}}

	merged := mergeVideoRefs(originals, reposts, 20)
	if len(merged) != 20 {
		t.Fatalf("len(merged) = %d, want 20", len(merged))
	}
	if merged[0].VideoID != "repost_new" {
		t.Fatalf("first video = %s, want repost_new", merged[0].VideoID)
	}
	for _, ref := range merged {
		if ref.VideoID == "orig_00" {
			t.Fatalf("oldest original should be pushed out by newer repost: %v", merged)
		}
	}
}

func TestPrimeShortFormMentionProfilesSeedsRefTitles(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir()), avatarRequest: make(chan string, 1)}

	m.primeShortFormMentionProfiles("instagram", []download.VideoRef{{
		VideoID: "instagram_reel_sample",
		Title:   "new reel with @sample.artist",
	}})

	got, err := d.GetChannelProfile("instagram_sample.artist")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Platform != "instagram" || got.Handle != "sample.artist" {
		t.Fatalf("profile row mismatch: %+v", got)
	}
	select {
	case queued := <-m.avatarRequest:
		if queued != "instagram_sample.artist" {
			t.Fatalf("queued profile = %q, want instagram_sample.artist", queued)
		}
	default:
		t.Fatal("expected mention profile to be queued for refresh")
	}
}

func TestEnsureIntroducedInstagramOwnerQueuesProfileRefresh(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir()), avatarRequest: make(chan string, 1)}

	m.ensureIntroducedOwner(download.VideoRef{
		ChannelID:         "instagram_by.bansoi",
		AuthorHandle:      "by.bansoi",
		AuthorDisplayName: "soi",
		AuthorAvatarURL:   "https://cdn.example/media-avatar.jpg",
	})

	got, err := d.GetChannelProfile("instagram_by.bansoi")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.AvatarURL != "" || got.FetchedAt != nil {
		t.Fatalf("media-derived avatar should not be stored before profile refresh: %+v", got)
	}
	select {
	case queued := <-m.avatarRequest:
		if queued != "instagram_by.bansoi" {
			t.Fatalf("queued profile = %q, want instagram_by.bansoi", queued)
		}
	default:
		t.Fatal("expected introduced instagram owner to be queued for background profile refresh")
	}
}

func TestInstagramUsesOwnGlobalSchedulerSettings(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.SetSetting("", "tiktok_fetch_delay", "3"); err != nil {
		t.Fatalf("SetSetting tiktok_fetch_delay: %v", err)
	}
	if err := d.SetSetting("", "instagram_fetch_delay", "7"); err != nil {
		t.Fatalf("SetSetting instagram_fetch_delay: %v", err)
	}
	if err := d.SetSetting("", "shorts_max_videos", "20"); err != nil {
		t.Fatalf("SetSetting shorts_max_videos: %v", err)
	}
	if err := d.SetSetting("", "instagram_max_videos", "45"); err != nil {
		t.Fatalf("SetSetting instagram_max_videos: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	ch := model.Channel{ChannelID: "instagram_cinema", Platform: "instagram"}

	if got := m.platformFetchDelay(ch.Platform); got != 7*time.Second {
		t.Fatalf("instagram fetch delay = %s, want 7s", got)
	}
	if got := m.platformDiscoveryCycleInterval(ch.Platform, 4); got != 28*time.Second {
		t.Fatalf("instagram cycle interval = %s, want 28s", got)
	}
	if got := m.getChannelMaxVideos(ch); got != 45 {
		t.Fatalf("instagram max videos = %d, want 45", got)
	}
}

func TestDiscoveryChannelsResumeByStaleness(t *testing.T) {
	now := time.Unix(100, 0)
	old := now.Add(-30 * time.Second)
	fresh := now.Add(-5 * time.Second)
	channels := []model.Channel{
		{ChannelID: "youtube_fresh", Platform: "youtube", LastChecked: &fresh},
		{ChannelID: "youtube_never", Platform: "youtube"},
		{ChannelID: "youtube_old", Platform: "youtube", LastChecked: &old},
	}

	sortChannelsByLastChecked(channels)
	ready := readyDiscoveryChannels(channels, 20*time.Second, now)

	got := make([]string, 0, len(ready))
	for _, ch := range ready {
		got = append(got, ch.ChannelID)
	}
	want := []string{"youtube_never", "youtube_old"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ready channels = %v, want %v", got, want)
	}
}

func TestDiscoverySelectionForceBypassesDueWindow(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.SetSetting("", "youtube_fetch_delay", "60"); err != nil {
		t.Fatalf("SetSetting youtube_fetch_delay: %v", err)
	}
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	now := time.Unix(100, 0)
	fresh := now.Add(-1 * time.Second)

	byPlatform := m.discoveryChannelsByPlatform([]model.Channel{
		{ChannelID: "youtube_fresh", Platform: "youtube", LastChecked: &fresh},
		{ChannelID: "youtube_never", Platform: "youtube"},
	}, true, now)

	if got, want := schedulerChannelIDs(byPlatform["youtube"]), []string{"youtube_never", "youtube_fresh"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("forced channels = %v, want %v", got, want)
	}
}

func TestDiscoverySelectionSkipsDisabledPlatforms(t *testing.T) {
	d := newTestWorkerDB(t)
	cfg := testCfg(t.TempDir())
	cfg.EnabledPlatforms = []string{"youtube"}
	cfg.EnabledPlatformSet = map[string]bool{"youtube": true}
	m := &Manager{db: d, cfg: cfg}

	byPlatform := m.discoveryChannelsByPlatform([]model.Channel{
		{ChannelID: "youtube_enabled", Platform: "youtube"},
		{ChannelID: "tiktok_disabled", Platform: "tiktok"},
		{ChannelID: "twitter_feed", Platform: "twitter"},
	}, false, time.Unix(100, 0))

	if got, want := schedulerChannelIDs(byPlatform["youtube"]), []string{"youtube_enabled"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("youtube channels = %v, want %v", got, want)
	}
	if _, ok := byPlatform["tiktok"]; ok {
		t.Fatalf("disabled platform should be skipped: %#v", byPlatform)
	}
	if _, ok := byPlatform["twitter"]; ok {
		t.Fatalf("twitter feed channel should not enter discovery scheduler: %#v", byPlatform)
	}
}

func TestPlatformFetchDelayDefaults(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}

	tests := map[string]time.Duration{
		"youtube":   10 * time.Second,
		"tiktok":    2 * time.Second,
		"instagram": 2 * time.Second,
	}
	for platform, want := range tests {
		if got := m.platformFetchDelay(platform); got != want {
			t.Fatalf("%s fetch delay = %s, want %s", platform, got, want)
		}
	}
}

func schedulerChannelIDs(channels []model.Channel) []string {
	ids := make([]string, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.ChannelID)
	}
	return ids
}
