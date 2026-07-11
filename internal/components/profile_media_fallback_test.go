package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestProfileMediaFailureOnlyShowsPresentationFallback(t *testing.T) {
	var buf bytes.Buffer
	profile := &model.ChannelProfile{
		ChannelID:   "twitter_sample_profile",
		Platform:    "twitter",
		Handle:      "sample_profile",
		DisplayName: "Lazy Profile",
		BannerURL:   "https://pbs.twimg.com/profile_banners/123/456",
	}
	if err := ProfileCard(newTestPageProps(), profile, "", true, false).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `/api/media/banner/twitter_sample_profile`) {
		t.Fatalf("profile card missing banner image: %s", html)
	}
	bannerStart := strings.Index(html, `<div class="profile-card-banner">`)
	if bannerStart < 0 {
		t.Fatalf("profile card missing banner container: %s", html)
	}
	bannerEnd := strings.Index(html[bannerStart:], `</div>`)
	if bannerEnd < 0 {
		t.Fatalf("profile card banner container was not closed: %s", html)
	}
	bannerHTML := html[bannerStart : bannerStart+bannerEnd]
	for _, check := range []string{
		`onload="window.MpaSiteBase&&window.MpaSiteBase.avatarLoad(this)"`,
		`onerror="if(window.MpaSiteBase&&window.MpaSiteBase.avatarError(this))return; this.style.display='none'"`,
	} {
		if !strings.Contains(bannerHTML, check) {
			t.Fatalf("profile card banner fallback missing %q: %s", check, bannerHTML)
		}
	}
}
