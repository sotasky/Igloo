package worker

import (
	"fmt"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestEnforceVideoRetentionForPlatformUsesEffectiveChannelLimits(t *testing.T) {
	database := newTestWorkerDB(t)
	if err := database.SetSetting("youtube_max_videos", "1"); err != nil {
		t.Fatal(err)
	}
	seedRetentionDesires(t, database, "youtube_default_source", "youtube")
	seedRetentionDesires(t, database, "youtube_override_source", "youtube")
	seedRetentionDesires(t, database, "tiktok_other_source", "tiktok")
	if err := database.UpdateChannelSettings("youtube_override_source", map[string]any{"max_videos": 2}); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{db: database}
	if err := manager.EnforceVideoRetentionForPlatform("youtube"); err != nil {
		t.Fatal(err)
	}

	assertRetentionDesireCount(t, database, "youtube_default_source", 1)
	assertRetentionDesireCount(t, database, "youtube_override_source", 2)
	assertRetentionDesireCount(t, database, "tiktok_other_source", 3)
	var queueCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM download_queue`).Scan(&queueCount); err != nil {
		t.Fatal(err)
	}
	if queueCount != 6 {
		t.Fatalf("download queue count = %d, want 6 retained roots", queueCount)
	}
}

func seedRetentionDesires(t *testing.T, database *db.DB, channelID, platform string) {
	t.Helper()
	if err := database.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, ?, 'Sample Source', '', ?, 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, channelID, channelID, platform, channelID); err != nil {
		t.Fatal(err)
	}
	items := make([]db.VideoDesire, 0, 3)
	for i := 1; i <= 3; i++ {
		items = append(items, db.VideoDesire{
			VideoID:        fmt.Sprintf("%s_video_%d", channelID, i),
			OwnerChannelID: channelID,
			PublishedAtMs:  int64(i * 100),
			SourcePosition: 3 - i,
			Lane:           db.DownloadLaneBackfill,
		})
	}
	if _, err := database.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: channelID,
		Component:       "uploads",
		Items:           items,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertRetentionDesireCount(t *testing.T, database *db.DB, channelID string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(`
		SELECT COUNT(DISTINCT video_id) FROM video_desires WHERE source_channel_id = ?
	`, channelID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s desired count = %d, want %d", channelID, got, want)
	}
}
