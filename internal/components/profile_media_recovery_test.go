package components

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestProfileMediaRecoveryRetriesAvatarsAndBanners(t *testing.T) {
	srcBytes, err := os.ReadFile("../../static/js/site_base.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	checks := []string{
		"function avatarBaseSrc(src)",
		"url.searchParams.delete('avatar_retry')",
		"url.searchParams.delete('avatar_refresh')",
		"function recoverableProfileMediaSrc(src)",
		"src.indexOf('/api/media/banner/') !== -1",
		"function refreshMatchingAvatars(loadedImg)",
		"retryAvatarNow(img, baseSrc)",
		"refreshMatchingAvatars(img)",
		"var delays = [1200, 3500, 8000, 16000]",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("site_base.js profile media recovery missing %q", check)
		}
	}

	var buf bytes.Buffer
	profile := &model.ChannelProfile{
		ChannelID:   "twitter_lazy_profile",
		Platform:    "twitter",
		Handle:      "lazy_profile",
		DisplayName: "Lazy Profile",
		BannerURL:   "https://pbs.twimg.com/profile_banners/123/456",
	}
	if err := ProfileCard(newTestPageProps(), profile, "", true, false).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `/api/media/banner/twitter_lazy_profile`) {
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
			t.Fatalf("profile card banner recovery missing %q: %s", check, bannerHTML)
		}
	}
}
