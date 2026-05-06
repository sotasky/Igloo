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
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}

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
}

func TestInstagramUsesOwnGlobalSchedulerSettings(t *testing.T) {
	d := newTestWorkerDB(t)
	if err := d.SetSetting("", "shorts_check_interval", "3"); err != nil {
		t.Fatalf("SetSetting shorts_check_interval: %v", err)
	}
	if err := d.SetSetting("", "instagram_check_interval", "7"); err != nil {
		t.Fatalf("SetSetting instagram_check_interval: %v", err)
	}
	if err := d.SetSetting("", "shorts_max_videos", "20"); err != nil {
		t.Fatalf("SetSetting shorts_max_videos: %v", err)
	}
	if err := d.SetSetting("", "instagram_max_videos", "45"); err != nil {
		t.Fatalf("SetSetting instagram_max_videos: %v", err)
	}

	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	ch := model.Channel{ChannelID: "instagram_cinema", Platform: "instagram"}

	if got := m.getCheckInterval(ch); got != 7*time.Hour {
		t.Fatalf("instagram check interval = %s, want 7h", got)
	}
	if got := m.getChannelMaxVideos(ch); got != 45 {
		t.Fatalf("instagram max videos = %d, want 45", got)
	}
}
