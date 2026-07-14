package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

func TestYouTubeDiscoveryDoesNotRetryUsablePartialWindow(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := `#!/bin/sh
printf 'call\n' >> "$IGLOO_YTDLP_CALLS"
printf '{"_type":"url","id":"sample_partial","title":"Partial item"}\n'
printf 'source stopped before the full window was read\n' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("IGLOO_YTDLP_CALLS", calls)

	database := newTestWorkerDB(t)
	manager := &Manager{
		db:         database,
		downloader: &download.Downloader{YtDlp: &download.YtDlpWrapper{}},
	}
	_, _ = manager.checkChannel(context.Background(), model.Channel{
		ChannelID: "youtube_sample_source",
		Platform:  "youtube",
		URL:       "https://www.youtube.com/channel/UCEXAMPLE000000000000001",
	})
	rawCalls, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(rawCalls) != "call\n" {
		t.Fatalf("yt-dlp calls = %q, want one call", rawCalls)
	}
}

func TestDiscoveryReadyAtSpacesPlatformChecksAcrossCycle(t *testing.T) {
	checked := time.Unix(100, 0)
	lastStart := time.Unix(135, 0)
	channel := model.Channel{LastChecked: &checked}

	got := discoveryReadyAt(channel, 4, 10*time.Second, lastStart)
	want := time.Unix(145, 0)
	if !got.Equal(want) {
		t.Fatalf("ready at = %s, want %s", got, want)
	}

	channel.LastChecked = nil
	got = discoveryReadyAt(channel, 4, 10*time.Second, lastStart)
	if !got.Equal(want) {
		t.Fatalf("never-checked channel ready at = %s, want start spacing %s", got, want)
	}
}

func TestFailedReconcileStillRotatesDiscoveryChannel(t *testing.T) {
	database := newTestWorkerDB(t)
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES
			('youtube_sample_first', 'First', 'youtube', 1, 1),
			('youtube_sample_second', 'Second', 'youtube', 2, 1);
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES
			('youtube_sample_first', 1),
			('youtube_sample_second', 1)
	`); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: "youtube_sample_first", Name: "First", Platform: "youtube"}
	snapshot := download.SourceSnapshot{Windows: []download.SourceWindow{
		{Component: "uploads", Complete: true},
		{Component: "uploads", Complete: true},
	}}

	if _, err := manager.applyDiscoverySnapshot(channel, snapshot); err == nil {
		t.Fatal("duplicate source component unexpectedly reconciled")
	}
	next, count, err := database.NextSubscribedChannel("youtube")
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ChannelID != "youtube_sample_second" || count != 2 {
		t.Fatalf("next channel = %+v, count = %d", next, count)
	}
}

func TestCompleteMultiComponentSnapshotKeepsTimestampFreeNewHeadAtLimit(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.instagram.com/sample_source/', 'instagram', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := database.SetSetting("instagram_max_videos", "1"); err != nil {
		t.Fatalf("set retention limit: %v", err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "instagram"}
	if _, err := manager.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{{
		Component: download.SourceComponentPosts,
		Complete:  true,
		Refs:      []download.VideoRef{{VideoID: "instagram_sample_old", PublishedAtMs: 100}},
	}}}); err != nil {
		t.Fatalf("seed old head: %v", err)
	}
	multi := download.SourceSnapshot{Windows: []download.SourceWindow{
		{Component: download.SourceComponentReels, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_sample_new"}}},
		{Component: download.SourceComponentPosts, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_sample_old", PublishedAtMs: 100}}},
	}}
	for pass := 1; pass <= 2; pass++ {
		if _, err := manager.reconcileSourceSnapshot(channel, multi); err != nil {
			t.Fatalf("reconcile multi-component snapshot pass %d: %v", pass, err)
		}
	}
	if got := desireIDs(desireWindow(t, database, sourceID, download.SourceComponentReels)); fmt.Sprint(got) != "[instagram_sample_new]" {
		t.Fatalf("retained reels = %v", got)
	}
	if got := desireWindow(t, database, sourceID, download.SourceComponentPosts); len(got) != 0 {
		t.Fatalf("older timestamped post survived global limit: %#v", got)
	}
}

func TestInitialMultiComponentRetentionUsesContentTime(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.instagram.com/sample_source/', 'instagram', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("instagram_max_videos", "1"); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "instagram"}
	if _, err := manager.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{
		{Component: download.SourceComponentReels, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_sample_new", PublishedAtMs: 200}}},
		{Component: download.SourceComponentPosts, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_sample_old", PublishedAtMs: 100}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := desireIDs(desireWindow(t, database, sourceID, download.SourceComponentReels)); fmt.Sprint(got) != "[instagram_sample_new]" {
		t.Fatalf("newer component item = %v", got)
	}
	if got := desireWindow(t, database, sourceID, download.SourceComponentPosts); len(got) != 0 {
		t.Fatalf("older later component survived: %#v", got)
	}
}

func TestStoryDesiresStayCurrentOutsideVideoLimit(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "tiktok_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.tiktok.com/@sample_source', 'tiktok', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("shorts_max_videos", "1"); err != nil {
		t.Fatal(err)
	}
	stories := make([]download.VideoRef, nativeStoryFetchLimit+5)
	for index := range stories {
		stories[index] = download.VideoRef{
			VideoID:       fmt.Sprintf("tiktok_story_%d", 1000+index),
			PublishedAtMs: int64(10_000 - index),
		}
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "tiktok"}
	if _, err := manager.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{
		{Component: download.SourceComponentDirect, Complete: true, Refs: []download.VideoRef{
			{VideoID: "sample_new", PublishedAtMs: 200},
			{VideoID: "sample_old", PublishedAtMs: 100},
		}},
		{Component: sourceComponentStories, Complete: true, Refs: stories},
	}}); err != nil {
		t.Fatal(err)
	}

	if got := desireIDs(desireWindow(t, database, sourceID, download.SourceComponentDirect)); fmt.Sprint(got) != "[sample_new]" {
		t.Fatalf("bounded regular window = %v", got)
	}
	storyItems := desireWindow(t, database, sourceID, sourceComponentStories)
	if len(storyItems) != nativeStoryFetchLimit {
		t.Fatalf("story window size = %d, want %d", len(storyItems), nativeStoryFetchLimit)
	}
	for _, item := range storyItems {
		if item.Lane != db.DownloadLaneCurrent {
			t.Fatalf("story %s lane = %q, want current", item.VideoID, item.Lane)
		}
	}
	if err := database.EnforceVideoDesireLimit(sourceID, 1); err != nil {
		t.Fatal(err)
	}
	if got := len(desireWindow(t, database, sourceID, sourceComponentStories)); got != nativeStoryFetchLimit {
		t.Fatalf("normal retention reduced story window to %d", got)
	}
}

func TestIdenticalTimestampFreeComponentsDoNotOscillateAtLimit(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'instagram', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("instagram_max_videos", "1"); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "instagram"}
	snapshot := download.SourceSnapshot{Windows: []download.SourceWindow{
		{Component: download.SourceComponentReels, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_reel_sample"}}},
		{Component: download.SourceComponentPosts, Complete: true, Refs: []download.VideoRef{{VideoID: "instagram_post_sample"}}},
	}}
	for pass := 1; pass <= 3; pass++ {
		if _, err := manager.reconcileSourceSnapshot(channel, snapshot); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if got := desireIDs(desireWindow(t, database, sourceID, download.SourceComponentReels)); fmt.Sprint(got) != "[instagram_reel_sample]" {
			t.Fatalf("pass %d reels = %v", pass, got)
		}
		if got := desireWindow(t, database, sourceID, download.SourceComponentPosts); len(got) != 0 {
			t.Fatalf("pass %d posts = %#v", pass, got)
		}
	}
}

func TestCompleteRecheckPreservesCurrentLane(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "youtube_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'youtube', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "youtube"}
	window := download.SourceWindow{
		Component: download.SourceComponentDirect,
		Complete:  true,
		Refs: []download.VideoRef{
			{VideoID: "sample_head", PublishedAtMs: 200},
			{VideoID: "sample_tail", PublishedAtMs: 100},
		},
	}
	if _, err := manager.reconcileSourceWindow(channel, window); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.reconcileSourceWindow(channel, window); err != nil {
		t.Fatal(err)
	}
	items := desireWindow(t, database, sourceID, download.SourceComponentDirect)
	if head := desireByID(items, "sample_head"); head == nil || head.Lane != db.DownloadLaneCurrent {
		t.Fatalf("rechecked head = %#v", head)
	}
	if tail := desireByID(items, "sample_tail"); tail == nil || tail.Lane != db.DownloadLaneBackfill {
		t.Fatalf("rechecked tail = %#v", tail)
	}
}

func TestPartialExpandedTailKeepsExactSourceOrder(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "youtube_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'youtube', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "youtube"}
	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: download.SourceComponentDirect,
		Complete:  true,
		Refs: []download.VideoRef{
			{VideoID: "sample_a"},
			{VideoID: "sample_b"},
			{VideoID: "sample_c"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: download.SourceComponentDirect,
		Complete:  false,
		Refs: []download.VideoRef{
			{VideoID: "sample_c"},
			{VideoID: "sample_new_tail"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	items := desireWindow(t, database, sourceID, download.SourceComponentDirect)
	if got := fmt.Sprint(desireIDs(items)); got != "[sample_a sample_b sample_c sample_new_tail]" {
		t.Fatalf("partial tail order = %s", got)
	}
	for position, item := range items {
		if item.SourcePosition != position {
			t.Fatalf("%s position = %d, want %d", item.VideoID, item.SourcePosition, position)
		}
	}
	if tail := desireByID(items, "sample_new_tail"); tail == nil || tail.Lane != db.DownloadLaneBackfill {
		t.Fatalf("partial tail lane = %#v", tail)
	}
}

func TestSharedInstagramItemUsesOneOwnerAcrossComponents(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.instagram.com/sample_source/', 'instagram', 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, sourceID, sourceID); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db:          database,
		cfg:         testCfg(t.TempDir()),
		profileKick: make(chan struct{}, 1),
	}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "instagram"}
	snapshot := download.SourceSnapshot{Windows: []download.SourceWindow{
		{
			Component: download.SourceComponentPosts,
			Complete:  true,
			Refs:      []download.VideoRef{{VideoID: "instagram_sample_post"}},
		},
		{
			Component: sourceComponentTagged,
			Complete:  true,
			Refs: []download.VideoRef{{
				VideoID: "instagram_sample_post", ChannelID: "instagram_sample_author",
				AuthorHandle: "sample_author", IsRepost: true,
				ReposterChannelID: sourceID, ReposterHandle: "sample_source",
			}},
		},
	}}
	if _, err := manager.reconcileSourceSnapshot(channel, snapshot); err != nil {
		t.Fatal(err)
	}
	if got := len(desireWindow(t, database, sourceID, download.SourceComponentPosts)); got != 1 {
		t.Fatalf("posts desires = %d", got)
	}
	if got := len(desireWindow(t, database, sourceID, sourceComponentTagged)); got != 1 {
		t.Fatalf("tagged desires = %d", got)
	}
	var owner string
	if err := database.QueryRow(`
		SELECT owner_channel_id FROM download_queue WHERE video_id = 'instagram_sample_post'
	`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "instagram_sample_author" {
		t.Fatalf("canonical owner = %q", owner)
	}
}

func TestClassifySourceWindowLanesSeparatesHeadFromExpandedTail(t *testing.T) {
	refs := []download.VideoRef{
		{VideoID: "new_head"},
		{VideoID: "old_head"},
		{VideoID: "old_tail"},
		{VideoID: "newly_exposed_tail"},
	}
	lanes := classifySourceWindowLanes([]string{"old_head", "old_tail"}, refs)
	if got := lanes["new_head"]; got != db.DownloadLaneCurrent {
		t.Fatalf("new head lane = %q, want current", got)
	}
	if got := lanes["newly_exposed_tail"]; got != db.DownloadLaneBackfill {
		t.Fatalf("expanded tail lane = %q, want backfill", got)
	}
}

func TestClassifySourceWindowLanesStartsNewFollowWithOneCurrentItem(t *testing.T) {
	lanes := classifySourceWindowLanes(nil, []download.VideoRef{
		{VideoID: "newest"},
		{VideoID: "older"},
		{VideoID: "oldest"},
	})
	if lanes["newest"] != db.DownloadLaneCurrent ||
		lanes["older"] != db.DownloadLaneBackfill ||
		lanes["oldest"] != db.DownloadLaneBackfill {
		t.Fatalf("new follow lanes = %#v", lanes)
	}
}

func TestClassifySourceWindowLanesBoundsCurrentWorkWithoutOverlap(t *testing.T) {
	refs := make([]download.VideoRef, discoveryCurrentHeadLimit+3)
	for index := range refs {
		refs[index].VideoID = fmt.Sprintf("replacement_%02d", index)
	}

	lanes := classifySourceWindowLanes([]string{"previous_head", "previous_tail"}, refs)
	for index, ref := range refs {
		want := db.DownloadLaneBackfill
		if index < discoveryCurrentHeadLimit {
			want = db.DownloadLaneCurrent
		}
		if got := lanes[ref.VideoID]; got != want {
			t.Fatalf("replacement %d lane = %q, want %q", index, got, want)
		}
	}
}

func TestPartialSourceSnapshotsCannotGrowPastRetentionLimit(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "youtube_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.youtube.com/@sample_source', 'youtube', 1)
	`, sourceID); err != nil {
		t.Fatalf("insert source channel: %v", err)
	}
	if err := database.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`, sourceID); err != nil {
		t.Fatalf("follow source channel: %v", err)
	}
	if err := database.SetSetting("youtube_max_videos", "2"); err != nil {
		t.Fatalf("set retention limit: %v", err)
	}

	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "youtube"}
	apply := func(complete bool, refs ...download.VideoRef) {
		t.Helper()
		if _, err := manager.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{{
			Component: download.SourceComponentDirect,
			Complete:  complete,
			Refs:      refs,
		}}}); err != nil {
			t.Fatalf("reconcile source snapshot: %v", err)
		}
	}

	apply(true,
		download.VideoRef{VideoID: "sample_old_newer", PublishedAtMs: 200},
		download.VideoRef{VideoID: "sample_old_older", PublishedAtMs: 100},
	)
	apply(false, download.VideoRef{VideoID: "sample_new_first", PublishedAtMs: 300})
	apply(false, download.VideoRef{VideoID: "sample_new_second", PublishedAtMs: 400})

	items := desireWindow(t, database, sourceID, download.SourceComponentDirect)
	if len(items) != 2 || desireByID(items, "sample_new_first") == nil || desireByID(items, "sample_new_second") == nil {
		t.Fatalf("desired window after repeated partial snapshots = %#v", items)
	}

	if err := database.SetSetting("youtube_max_videos", "1"); err != nil {
		t.Fatalf("tighten retention limit: %v", err)
	}
	apply(false, download.VideoRef{VideoID: "sample_new_without_timestamp"})
	items = desireWindow(t, database, sourceID, download.SourceComponentDirect)
	if len(items) != 1 || items[0].VideoID != "sample_new_without_timestamp" {
		t.Fatalf("desired window after timestamp-free head = %#v", items)
	}
}

func TestPartialIntroducedSnapshotsPruneProvenanceWithDesire(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "tiktok_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.tiktok.com/@sample_source', 'tiktok', 1)
	`, sourceID); err != nil {
		t.Fatalf("insert source channel: %v", err)
	}
	if err := database.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`, sourceID); err != nil {
		t.Fatalf("follow source channel: %v", err)
	}
	if err := database.SetSetting("shorts_max_videos", "1"); err != nil {
		t.Fatalf("set retention limit: %v", err)
	}

	manager := &Manager{
		db:          database,
		cfg:         testCfg(t.TempDir()),
		profileKick: make(chan struct{}, 1),
	}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "tiktok"}
	apply := func(videoID, ownerID, handle string, observedAt int64) {
		t.Helper()
		if _, err := manager.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{{
			Component: sourceComponentReposts,
			Complete:  false,
			Refs: []download.VideoRef{{
				VideoID: videoID, ChannelID: ownerID, AuthorHandle: handle,
				IsRepost: true, RepostedAtMs: observedAt,
				ReposterChannelID: sourceID, ReposterHandle: "sample_source",
			}},
		}}}); err != nil {
			t.Fatalf("reconcile introduced snapshot: %v", err)
		}
	}

	apply("tiktok_sample_old", "tiktok_sample_old_owner", "sample_old_owner", 100)
	apply("tiktok_sample_new", "tiktok_sample_new_owner", "sample_new_owner", 200)

	items := desireWindow(t, database, sourceID, sourceComponentReposts)
	if len(items) != 1 || items[0].VideoID != "tiktok_sample_new" {
		t.Fatalf("bounded introduced desires = %#v", items)
	}
	oldRows, err := database.GetVideoRepostSources("tiktok_sample_old")
	if err != nil || len(oldRows) != 0 {
		t.Fatalf("trimmed provenance = %#v, err=%v", oldRows, err)
	}
	newRows, err := database.GetVideoRepostSources("tiktok_sample_new")
	if err != nil || len(newRows) != 1 {
		t.Fatalf("retained provenance = %#v, err=%v", newRows, err)
	}
}

func TestComponentWindowsConvergeIndependently(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.instagram.com/sample_source/', 'instagram', 1)
	`, sourceID); err != nil {
		t.Fatalf("insert source channel: %v", err)
	}
	if err := database.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`, sourceID); err != nil {
		t.Fatalf("follow source channel: %v", err)
	}
	manager := &Manager{
		db:          database,
		cfg:         testCfg(t.TempDir()),
		profileKick: make(chan struct{}, 1),
	}
	channel := model.Channel{ChannelID: sourceID, SourceID: "sample_source", Name: "Sample Source", Platform: "instagram"}

	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: download.SourceComponentPosts,
		Complete:  true,
		Refs: []download.VideoRef{
			{VideoID: "instagram_sample_post_new", PublishedAtMs: 200},
			{VideoID: "instagram_sample_post_old", PublishedAtMs: 100},
		},
	}); err != nil {
		t.Fatalf("seed posts: %v", err)
	}
	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: sourceComponentTagged,
		Complete:  true,
		Refs: []download.VideoRef{{
			VideoID:             "instagram_sample_post_tagged",
			ChannelID:           "instagram_sample_author",
			AuthorHandle:        "sample_author",
			AuthorDisplayName:   "Sample Author",
			IsRepost:            true,
			ReposterChannelID:   sourceID,
			ReposterHandle:      "sample_source",
			ReposterDisplayName: "Sample Source",
		}},
	}); err != nil {
		t.Fatalf("seed tagged: %v", err)
	}

	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: download.SourceComponentPosts,
		Complete:  false,
		Refs:      []download.VideoRef{{VideoID: "instagram_sample_post_old"}},
	}); err != nil {
		t.Fatalf("observe partial posts: %v", err)
	}
	posts := desireWindow(t, database, sourceID, download.SourceComponentPosts)
	if got := desireIDs(posts); fmt.Sprint(got) != "[instagram_sample_post_new instagram_sample_post_old]" {
		t.Fatalf("posts after partial observation = %v", got)
	}
	old := desireByID(posts, "instagram_sample_post_old")
	if old == nil || old.SourcePosition != 1 || old.Lane != db.DownloadLaneBackfill {
		t.Fatalf("partial observation changed old placement: %#v", old)
	}
	if got := desireIDs(desireWindow(t, database, sourceID, sourceComponentTagged)); fmt.Sprint(got) != "[instagram_sample_post_tagged]" {
		t.Fatalf("posts observation changed tagged window: %v", got)
	}

	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: download.SourceComponentPosts,
		Complete:  true,
	}); err != nil {
		t.Fatalf("clear posts: %v", err)
	}
	if got := desireWindow(t, database, sourceID, download.SourceComponentPosts); len(got) != 0 {
		t.Fatalf("complete empty posts retained desires: %#v", got)
	}
	if got := desireIDs(desireWindow(t, database, sourceID, sourceComponentTagged)); fmt.Sprint(got) != "[instagram_sample_post_tagged]" {
		t.Fatalf("clearing posts changed tagged window: %v", got)
	}
	provenance, err := database.GetVideoRepostSources("instagram_sample_post_tagged")
	if err != nil || len(provenance) != 1 {
		t.Fatalf("tagged provenance before clear = %#v, err=%v", provenance, err)
	}

	if _, err := manager.reconcileSourceWindow(channel, download.SourceWindow{
		Component: sourceComponentTagged,
		Complete:  true,
	}); err != nil {
		t.Fatalf("clear tagged: %v", err)
	}
	provenance, err = database.GetVideoRepostSources("instagram_sample_post_tagged")
	if err != nil || len(provenance) != 0 {
		t.Fatalf("complete empty tagged retained provenance: %#v, err=%v", provenance, err)
	}
}

func TestIntroducedOwnerFailureDoesNotReplacePriorWindow(t *testing.T) {
	database := newTestWorkerDB(t)
	const sourceID = "instagram_sample_source"
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, 'sample_source', 'Sample Source', 'https://www.instagram.com/sample_source/', 'instagram', 1)
	`, sourceID); err != nil {
		t.Fatalf("insert source channel: %v", err)
	}
	if err := database.ExecRaw(`INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)`, sourceID); err != nil {
		t.Fatalf("follow source channel: %v", err)
	}
	manager := &Manager{db: database, cfg: testCfg(t.TempDir()), profileKick: make(chan struct{}, 1)}
	channel := model.Channel{ChannelID: sourceID, Name: "Sample Source", Platform: "instagram"}
	seed := download.SourceWindow{Component: sourceComponentTagged, Complete: true, Refs: []download.VideoRef{{
		VideoID: "instagram_sample_post_existing", ChannelID: "instagram_sample_existing", AuthorHandle: "sample_existing", IsRepost: true,
	}}}
	if _, err := manager.reconcileSourceWindow(channel, seed); err != nil {
		t.Fatalf("seed tagged window: %v", err)
	}
	if err := database.ExecRaw(`
		CREATE TRIGGER reject_introduced_owner
		BEFORE INSERT ON channels
		WHEN NEW.channel_id = 'instagram_sample_deleted'
		BEGIN SELECT RAISE(ABORT, 'owner rejected'); END
	`); err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}
	failed := download.SourceWindow{Component: sourceComponentTagged, Complete: true, Refs: []download.VideoRef{{
		VideoID: "instagram_sample_post_deleted", ChannelID: "instagram_sample_deleted", AuthorHandle: "sample_deleted", IsRepost: true,
	}}}
	if _, err := manager.reconcileSourceWindow(channel, failed); err == nil {
		t.Fatal("unpersistable owner unexpectedly replaced tagged window")
	}
	if got := desireIDs(desireWindow(t, database, sourceID, sourceComponentTagged)); fmt.Sprint(got) != "[instagram_sample_post_existing]" {
		t.Fatalf("failed owner replaced prior tagged window: %v", got)
	}
}

func TestEnsureIntroducedInstagramOwnerQueuesProfileRefresh(t *testing.T) {
	database := newTestWorkerDB(t)
	manager := &Manager{db: database, cfg: testCfg(t.TempDir()), profileKick: make(chan struct{}, 1)}

	if err := manager.ensureIntroducedOwner(download.VideoRef{
		ChannelID:         "instagram_sample_author",
		AuthorHandle:      "sample_author",
		AuthorDisplayName: "Sample Owner",
		AuthorAvatarURL:   "https://cdn.example/media-avatar.jpg",
	}); err != nil {
		t.Fatalf("ensure introduced owner: %v", err)
	}

	profile, err := database.GetChannelProfile("instagram_sample_author")
	if err != nil || profile == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, profile)
	}
	if profile.AvatarURL != "" || profile.FetchedAt != nil {
		t.Fatalf("media-derived avatar stored before profile refresh: %+v", profile)
	}
	job, err := database.GetProfileJob("instagram_sample_author")
	if err != nil || job == nil || job.RequestedRevision <= job.CompletedRevision {
		t.Fatalf("durable introduced-owner job = %+v, err=%v", job, err)
	}
}

func TestInstagramUsesOwnGlobalSchedulerSettings(t *testing.T) {
	database := newTestWorkerDB(t)
	if err := database.SetSetting("tiktok_fetch_delay", "3"); err != nil {
		t.Fatalf("SetSetting tiktok_fetch_delay: %v", err)
	}
	if err := database.SetSetting("instagram_fetch_delay", "7"); err != nil {
		t.Fatalf("SetSetting instagram_fetch_delay: %v", err)
	}
	if err := database.SetSetting("shorts_max_videos", "20"); err != nil {
		t.Fatalf("SetSetting shorts_max_videos: %v", err)
	}
	if err := database.SetSetting("instagram_max_videos", "45"); err != nil {
		t.Fatalf("SetSetting instagram_max_videos: %v", err)
	}

	manager := &Manager{db: database, cfg: testCfg(t.TempDir())}
	channel := model.Channel{ChannelID: "instagram_sample", Platform: "instagram"}
	if got := manager.platformFetchDelay(channel.Platform); got != 7*time.Second {
		t.Fatalf("instagram fetch delay = %s, want 7s", got)
	}
	if got := manager.getChannelMaxVideos(channel); got != 45 {
		t.Fatalf("instagram max videos = %d, want 45", got)
	}
}

func sourceWindowIDs(window download.SourceWindow) []string {
	ids := make([]string, 0, len(window.Refs))
	for _, ref := range window.Refs {
		ids = append(ids, ref.VideoID)
	}
	return ids
}

func desireWindow(t *testing.T, database *db.DB, sourceID, component string) []db.VideoDesireWindowItem {
	t.Helper()
	items, err := database.GetVideoDesireWindow(sourceID, component)
	if err != nil {
		t.Fatalf("GetVideoDesireWindow(%s): %v", component, err)
	}
	return items
}

func desireIDs(items []db.VideoDesireWindowItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.VideoID)
	}
	return ids
}

func desireByID(items []db.VideoDesireWindowItem, videoID string) *db.VideoDesireWindowItem {
	for i := range items {
		if items[i].VideoID == videoID {
			return &items[i]
		}
	}
	return nil
}
