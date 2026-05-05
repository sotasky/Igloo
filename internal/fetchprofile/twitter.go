package fetchprofile

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/screwys/igloo/internal/fxtwitter"
)

// FetchTwitter calls fxtwitter for the given handle.
func FetchTwitter(ctx context.Context, handle string) (*Profile, error) {
	return fetchTwitterWithClient(ctx, handle, fxtwitter.NewClient())
}

func fetchTwitterWithClient(ctx context.Context, handle string, fx *fxtwitter.Client) (*Profile, error) {
	h := strings.ToLower(strings.TrimPrefix(handle, "@"))
	u, err := fx.FetchUser(ctx, h)
	if err != nil {
		if errors.Is(err, fxtwitter.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("fxtwitter fetch: %w", err)
	}

	// Twitter banner URLs need the size suffix for the card-sized crop.
	bannerURL := fxtwitter.UpgradeBannerURL(u.BannerURL)

	return &Profile{
		ChannelID:    "twitter_" + h,
		Platform:     "twitter",
		Handle:       h,
		DisplayName:  u.Name,
		Bio:          u.Description,
		Website:      normalizeURL(u.Website),
		Followers:    u.Followers,
		Following:    u.Following,
		Verified:     u.Verified,
		VerifiedType: u.VerifiedType,
		Protected:    u.Protected,
		AvatarURL:    u.AvatarURL,
		BannerURL:    bannerURL,
	}, nil
}
