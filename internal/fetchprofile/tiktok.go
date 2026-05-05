package fetchprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const ttTimeout = 30 * time.Second

// FetchTikTok invokes gallery-dl against the user's /avatar route to extract
// the rich user-detail JSON (nickname, signature, verified, avatar URLs,
// bioLink). Stats are not returned by this route, so Followers/Following
// stay zero.
func FetchTikTok(ctx context.Context, handle string) (*Profile, error) {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if h == "" {
		return nil, ErrNotFound
	}
	cmdCtx, cancel := context.WithTimeout(ctx, ttTimeout)
	defer cancel()
	url := "https://www.tiktok.com/@" + h + "/avatar"
	cmd := exec.CommandContext(cmdCtx, "gallery-dl", "--dump-json", url)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && isTikTokNotFound(ee.Stderr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("gallery-dl: %w", err)
	}
	return parseTikTokAvatar(h, out)
}

func isTikTokNotFound(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	return strings.Contains(s, "not found") || strings.Contains(s, "no such user")
}

// parseTikTokAvatar walks the gallery-dl --dump-json output looking for the
// user-detail dict. The array contains tuples shaped
// [code, url-or-dict, metadata]; the user-detail we want is the dict entry
// where "type" == "avatar".
func parseTikTokAvatar(handle string, out []byte) (*Profile, error) {
	var raw []any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var user map[string]any
	for _, item := range raw {
		arr, ok := item.([]any)
		if !ok || len(arr) < 2 {
			continue
		}
		d, ok := arr[1].(map[string]any)
		if !ok {
			continue
		}
		if t, _ := d["type"].(string); t == "avatar" {
			user = d
			break
		}
	}
	if user == nil {
		return nil, ErrNotFound
	}
	p := &Profile{
		ChannelID:   "tiktok_" + handle,
		Platform:    "tiktok",
		Handle:      strOf(user, "uniqueId"),
		DisplayName: strOf(user, "nickname"),
		Bio:         strOf(user, "signature"),
		AvatarURL:   strOf(user, "avatarLarger"),
		Verified:    boolOf(user, "verified"),
	}
	if p.Handle == "" {
		p.Handle = handle
	}
	if bl, ok := user["bioLink"].(map[string]any); ok {
		p.Website = normalizeURL(strOf(bl, "link"))
	}
	if p.AvatarURL == "" {
		p.AvatarURL = strOf(user, "avatarMedium")
	}
	return p, nil
}

func strOf(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func boolOf(m map[string]any, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}
