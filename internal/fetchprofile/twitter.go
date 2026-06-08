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
	returnedHandle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(u.ScreenName, "@")))
	if returnedHandle == "" {
		return nil, fmt.Errorf("%w: requested @%s returned empty screen_name", ErrIdentityMismatch, h)
	}
	if returnedHandle != h {
		return nil, fmt.Errorf("%w: requested @%s returned @%s", ErrIdentityMismatch, h, returnedHandle)
	}

	// Twitter banner URLs need the size suffix for the card-sized crop.
	bannerURL := fxtwitter.UpgradeBannerURL(u.BannerURL)

	return &Profile{
		ChannelID:    "twitter_" + returnedHandle,
		Platform:     "twitter",
		Handle:       returnedHandle,
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
