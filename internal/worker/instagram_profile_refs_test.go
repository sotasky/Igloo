package worker

import (
	"testing"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

func TestRememberInstagramProfileFromRefsUsesTaggedSourceAvatar(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}

	m.rememberInstagramProfileFromRefs(model.Channel{
		ChannelID: "instagram_sample_followed_source",
		Platform:  "instagram",
		Name:      "Followed Source",
		URL:       "https://instagram.com/sample_followed_source",
	}, []download.VideoRef{{
		ChannelID:           "instagram_sample_author",
		AuthorHandle:        "sample_author",
		AuthorDisplayName:   "Owner Account",
		AuthorAvatarURL:     "https://cdn.example/owner.jpg",
		IsRepost:            true,
		ReposterChannelID:   "instagram_sample_followed_source",
		ReposterHandle:      "sample_followed_source",
		ReposterDisplayName: "Followed Source",
		ReposterAvatarURL:   "https://cdn.example/followed.jpg",
	}})

	got, err := d.GetChannelProfile("instagram_sample_followed_source")
	if err != nil || got == nil {
		t.Fatalf("GetChannelProfile: %v / %+v", err, got)
	}
	if got.Handle != "sample_followed_source" || got.DisplayName != "Followed Source" {
		t.Fatalf("source identity was not applied: %+v", got)
	}
	if got.AvatarURL != "https://cdn.example/followed.jpg" {
		t.Fatalf("AvatarURL = %q, want tagged source avatar", got.AvatarURL)
	}
}
