package worker

import (
	"fmt"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
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

func TestEnsureIntroducedInstagramOwnerQueuesProfileRefresh(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir()), profileKick: make(chan struct{}, 1)}

	m.ensureIntroducedOwner(download.VideoRef{
		ChannelID:         "instagram_test_owner",
		AuthorHandle:      "test_owner",
		AuthorDisplayName: "Sample Owner",
		AuthorAvatarURL:   "https://cdn.example/media-avatar.jpg",
	})

	got, err := d.GetChannelProfile("instagram_test_owner")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.AvatarURL != "" || got.FetchedAt != nil {
		t.Fatalf("media-derived avatar should not be stored before profile refresh: %+v", got)
	}
	job, err := d.GetProfileJob("instagram_test_owner")
	if err != nil || job == nil || job.RequestedRevision <= job.CompletedRevision {
		t.Fatalf("durable introduced-owner job = %+v, err=%v", job, err)
	}
}

func TestInstagramUsesOwnGlobalSchedulerSettings(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.SetSetting("tiktok_fetch_delay", "3"); err != nil {
		t.Fatalf("SetSetting tiktok_fetch_delay: %v", err)
	}
	if err := d.SetSetting("instagram_fetch_delay", "7"); err != nil {
		t.Fatalf("SetSetting instagram_fetch_delay: %v", err)
	}
	if err := d.SetSetting("shorts_max_videos", "20"); err != nil {
		t.Fatalf("SetSetting shorts_max_videos: %v", err)
	}
	if err := d.SetSetting("instagram_max_videos", "45"); err != nil {
		t.Fatalf("SetSetting instagram_max_videos: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	ch := model.Channel{ChannelID: "instagram_cinema", Platform: "instagram"}

	if got := m.platformFetchDelay(ch.Platform); got != 7*time.Second {
		t.Fatalf("instagram fetch delay = %s, want 7s", got)
	}
	if got := m.shortFormDownloadDelay(ch.Platform); got != 7*time.Second {
		t.Fatalf("instagram download delay = %s, want 7s", got)
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
	if err := d.SetSetting("youtube_fetch_delay", "60"); err != nil {
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

func TestMaterializeDiscoveryChannelsQueuesDueNonXOnly(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Unix(1000, 0)
	old := now.Add(-3 * time.Hour).UnixMilli()
	fresh := now.Add(-1 * time.Minute).UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES
			('youtube_sample_channel_due_shared_queue', 'YouTube Due', 'youtube', ?, 1),
			('youtube_sample_channel_fresh_shared_queue', 'YouTube Fresh', 'youtube', ?, 1),
			('tiktok_sample_channel_disabled_shared_queue', 'TikTok Disabled', 'tiktok', ?, 1),
			('twitter_sample_channel_skip_shared_queue', 'Twitter Skip', 'twitter', ?, 1)
	`, old, fresh, old, old); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES
			('youtube_sample_channel_due_shared_queue', 1),
			('youtube_sample_channel_fresh_shared_queue', 1),
			('tiktok_sample_channel_disabled_shared_queue', 1),
			('twitter_sample_channel_skip_shared_queue', 1)
	`); err != nil {
		t.Fatalf("insert follows: %v", err)
	}
	cfg := testCfg(t.TempDir())
	cfg.EnabledPlatforms = []string{"youtube"}
	cfg.EnabledPlatformSet = map[string]bool{"youtube": true}
	m := &Manager{db: d, cfg: cfg}

	if queued := m.materializeDiscoveryChannels(now, false); queued != 1 {
		t.Fatalf("materialized = %d, want 1", queued)
	}

	entries, err := d.GetPendingChannelQueue(10)
	if err != nil {
		t.Fatalf("GetPendingChannelQueue: %v", err)
	}
	if got, want := schedulerQueueIDs(entries), []string{"youtube_sample_channel_due_shared_queue"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("queued channels = %v, want %v", got, want)
	}
}

func TestPlatformDiscoveryGateAllowsOneActiveYoutube(t *testing.T) {
	gate := newPlatformDiscoveryGate()
	now := time.Unix(1000, 0)
	delayFor := func(string) time.Duration { return 120 * time.Second }

	if got := gate.eligiblePlatforms([]string{"youtube"}, delayFor, now); fmt.Sprint(got) != "[youtube]" {
		t.Fatalf("initial eligible platforms = %v, want [youtube]", got)
	}
	gate.markStart("youtube", now)
	if got := gate.eligiblePlatforms([]string{"youtube"}, delayFor, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("active youtube should not be eligible: %v", got)
	}
	gate.markDone("youtube")
	if got := gate.eligiblePlatforms([]string{"youtube"}, delayFor, now.Add(119*time.Second)); len(got) != 0 {
		t.Fatalf("youtube before delay should not be eligible: %v", got)
	}
	if got := gate.eligiblePlatforms([]string{"youtube"}, delayFor, now.Add(120*time.Second)); fmt.Sprint(got) != "[youtube]" {
		t.Fatalf("youtube after delay eligible = %v, want [youtube]", got)
	}
}

func TestPlatformFetchDelayDefaults(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}

	tests := map[string]time.Duration{
		"youtube":   120 * time.Second,
		"tiktok":    60 * time.Second,
		"instagram": 60 * time.Second,
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

func schedulerQueueIDs(channels []db.ChannelQueueRow) []string {
	ids := make([]string, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.ChannelID)
	}
	return ids
}
