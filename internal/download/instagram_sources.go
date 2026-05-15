package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const instagramGalleryDLTimeout = 90 * time.Second

var instagramSourceSuffixes = []string{"reels", "posts"}
var instagramHandleRe = regexp.MustCompile(`^[a-z0-9._]{1,64}$`)

func normalizeInstagramHandle(raw string) string {
	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if !instagramHandleRe.MatchString(handle) {
		return ""
	}
	return handle
}

type InstagramProfile struct {
	Handle      string
	DisplayName string
	Bio         string
	Website     string
	Followers   int
	Following   int
	Verified    bool
	AvatarURL   string
}

// InstagramChannel fetches recent Instagram posts and reels through gallery-dl
// without downloading media.
func (g *GalleryDLWrapper) InstagramChannel(ctx context.Context, handle string, limit int, cookiesFile string, cookiesBrowser ...string) ([]VideoRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	authAttempts := instagramCookieAuthAttempts(cookiesFile, optionalCookieBrowser(cookiesBrowser))
	var all []VideoRef
	var firstErr error
	anySuccess := false
	for _, suffix := range instagramSourceSuffixes {
		rawURL := "https://www.instagram.com/" + handle + "/" + suffix + "/"
		var refs []VideoRef
		var err error
		authSucceeded := false
		for _, auth := range authAttempts {
			cookieRefs, cookieErr := g.instagramDump(ctx, rawURL, limit, auth, handle)
			if cookieErr == nil {
				authSucceeded = true
				refs, err = cookieRefs, nil
				if len(cookieRefs) > 0 {
					break
				}
				continue
			}
			if err == nil {
				err = cookieErr
			}
		}
		if authSucceeded && len(refs) == 0 {
			err = nil
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		anySuccess = true
		all = append(all, refs...)
	}
	if len(all) == 0 && firstErr != nil && !anySuccess {
		return nil, firstErr
	}
	return mergeInstagramRefs(all, limit), nil
}

// InstagramTagged fetches recent posts where handle was tagged. The returned
// refs keep the original post owner in ChannelID and use repost-source fields
// to record the followed tagged account that introduced the post.
func (g *GalleryDLWrapper) InstagramTagged(ctx context.Context, handle string, limit int, cookiesFile string, cookiesBrowser ...string) ([]VideoRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	authAttempts := instagramCookieAuthAttempts(cookiesFile, optionalCookieBrowser(cookiesBrowser))
	rawURL := "https://www.instagram.com/" + handle + "/tagged/"
	var refs []VideoRef
	var err error
	authSucceeded := false
	for _, auth := range authAttempts {
		cookieOutput, cookieErr := g.instagramTaggedDumpOutput(ctx, rawURL, limit, auth.File, auth.Browser)
		cookieRefs := ParseInstagramTaggedDumpForHandle(cookieOutput, handle)
		if cookieErr == nil {
			authSucceeded = true
			err, refs = nil, cookieRefs
			if len(cookieRefs) > 0 {
				break
			}
			continue
		}
		if err == nil {
			err = cookieErr
		}
	}
	if authSucceeded && len(refs) == 0 {
		err = nil
	}
	if err != nil {
		return refs, err
	}
	return mergeInstagramRefs(refs, limit), nil
}

func (g *GalleryDLWrapper) InstagramProfile(ctx context.Context, handle string, cookiesFile string, cookiesBrowser ...string) (*InstagramProfile, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	var firstErr error
	anySuccess := false
	cookieAttempts := instagramProfileCookieAttempts(cookiesFile, optionalCookieBrowser(cookiesBrowser))
	for _, suffix := range instagramSourceSuffixes {
		rawURL := "https://www.instagram.com/" + handle + "/" + suffix + "/"
		for _, cookies := range cookieAttempts {
			output, err := g.instagramDumpOutput(ctx, rawURL, 1, cookies.File, cookies.Browser)
			profile := ParseInstagramProfileDump(output, handle)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			anySuccess = true
			if profile != nil {
				return profile, nil
			}
		}
	}
	if firstErr != nil && !anySuccess {
		return nil, firstErr
	}
	return &InstagramProfile{Handle: handle, DisplayName: handle}, nil
}

func instagramProfileCookieAttempts(cookiesFile, cookiesBrowser string) []CookieSet {
	if strings.TrimSpace(cookiesFile) == "" {
		if strings.TrimSpace(cookiesBrowser) == "" {
			return []CookieSet{{}}
		}
		return []CookieSet{{Browser: strings.TrimSpace(cookiesBrowser)}}
	}
	out := []CookieSet{{File: strings.TrimSpace(cookiesFile)}}
	if strings.TrimSpace(cookiesBrowser) != "" {
		out = append(out, CookieSet{Browser: strings.TrimSpace(cookiesBrowser)})
	}
	return out
}

func instagramCookieAuthAttempts(cookiesFile, cookiesBrowser string) []CookieSet {
	var out []CookieSet
	if strings.TrimSpace(cookiesFile) != "" {
		out = append(out, CookieSet{File: strings.TrimSpace(cookiesFile)})
	}
	if strings.TrimSpace(cookiesBrowser) != "" {
		out = append(out, CookieSet{Browser: strings.TrimSpace(cookiesBrowser)})
	}
	if len(out) == 0 {
		return []CookieSet{{}}
	}
	return out
}

func optionalCookieBrowser(cookiesBrowser []string) string {
	if len(cookiesBrowser) == 0 {
		return ""
	}
	return strings.TrimSpace(cookiesBrowser[0])
}

func (g *GalleryDLWrapper) instagramDump(ctx context.Context, rawURL string, limit int, cookies CookieSet, sourceHandle string) ([]VideoRef, error) {
	output, err := g.instagramDumpOutput(ctx, rawURL, limit, cookies.File, cookies.Browser)
	if err != nil {
		return nil, err
	}
	return ParseInstagramChannelDumpForHandle(output, sourceHandle), nil
}

func (g *GalleryDLWrapper) instagramDumpOutput(ctx context.Context, rawURL string, limit int, cookiesFile string, cookiesBrowser ...string) ([]byte, error) {
	browser := optionalCookieBrowser(cookiesBrowser)
	args := instagramDumpArgs(limit, cookiesFile, rawURL, browser)
	result := g.Run(ctx, "instagram.dump", "instagram", rawURL, args, cookiesFile, CommandOptions{Timeout: instagramGalleryDLTimeout}, browser)
	output := result.CombinedOutput()
	err := result.Err
	if err != nil {
		if errors.Is(result.Err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gallery-dl Instagram timed out after %s for %s", instagramGalleryDLTimeout, rawURL)
		}
		return nil, fmt.Errorf("gallery-dl Instagram: %w: %s", err, RedactText(string(output)))
	}
	return output, nil
}

func (g *GalleryDLWrapper) instagramTaggedDumpOutput(ctx context.Context, rawURL string, limit int, cookiesFile string, cookiesBrowser ...string) ([]byte, error) {
	browser := optionalCookieBrowser(cookiesBrowser)
	args := instagramTaggedArgs(limit, cookiesFile, rawURL, browser)
	result := g.Run(ctx, "instagram.tagged", "instagram", rawURL, args, cookiesFile, CommandOptions{Timeout: instagramGalleryDLTimeout}, browser)
	output := result.CombinedOutput()
	err := result.Err
	if err != nil {
		if errors.Is(result.Err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gallery-dl Instagram tagged timed out after %s for %s", instagramGalleryDLTimeout, rawURL)
		}
		return nil, fmt.Errorf("gallery-dl Instagram tagged: %w: %s", err, RedactText(string(output)))
	}
	return output, nil
}

func instagramDumpArgs(limit int, cookiesFile, rawURL string, cookiesBrowser ...string) []string {
	if limit <= 0 {
		limit = 20
	}
	args := []string{
		"--dump-json",
		"--simulate",
		"--range", "1-" + strconv.Itoa(limit),
	}
	args = appendCookieAuthArgs(args, cookiesFile, optionalCookieBrowser(cookiesBrowser))
	args = append(args, rawURL)
	return args
}

func instagramTaggedArgs(limit int, cookiesFile, rawURL string, cookiesBrowser ...string) []string {
	return instagramDumpArgs(limit, cookiesFile, rawURL, cookiesBrowser...)
}

func ParseInstagramChannelDump(output []byte) []VideoRef {
	return ParseInstagramChannelDumpForHandle(output, "")
}

func ParseInstagramChannelDumpForHandle(output []byte, sourceHandle string) []VideoRef {
	sourceHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(sourceHandle), "@"))
	seen := map[string]struct{}{}
	var refs []VideoRef
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, obj := range instagramSourceObjects(payload) {
			ref := instagramVideoRefFromGalleryDLObject(obj, sourceHandle)
			if ref.VideoID == "" {
				continue
			}
			if _, ok := seen[ref.VideoID]; ok {
				continue
			}
			seen[ref.VideoID] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func ParseInstagramTaggedDumpForHandle(output []byte, taggedHandle string) []VideoRef {
	taggedHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(taggedHandle), "@"))
	if taggedHandle == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var refs []VideoRef
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, obj := range instagramSourceObjects(payload) {
			ref := instagramVideoRefFromGalleryDLObject(obj, "")
			if ref.VideoID == "" || ref.ChannelID == "" {
				continue
			}
			reposterHandle := instagramTaggedHandleFromObject(obj, taggedHandle)
			if reposterHandle == "" {
				continue
			}
			if _, ok := seen[ref.VideoID]; ok {
				continue
			}
			ref.IsRepost = true
			ref.ReposterHandle = reposterHandle
			ref.ReposterChannelID = "instagram_" + reposterHandle
			ref.ReposterDisplayName = instagramTaggedDisplayNameFromObject(obj, reposterHandle)
			ref.RepostedAtMs = firstMillis(obj, "tagged_at", "reposted_at")
			seen[ref.VideoID] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs
}

func ParseInstagramProfileDump(output []byte, fallbackHandle string) *InstagramProfile {
	fallbackHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fallbackHandle), "@"))
	sawObject := false
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, obj := range instagramSourceObjects(payload) {
			sawObject = true
			profile := instagramProfileFromGalleryDLObject(obj, fallbackHandle)
			if profile.Handle != "" || profile.DisplayName != "" || profile.AvatarURL != "" {
				if profile.Handle == "" {
					profile.Handle = fallbackHandle
				}
				if profile.DisplayName == "" {
					profile.DisplayName = profile.Handle
				}
				return &profile
			}
		}
	}
	if fallbackHandle == "" || !sawObject {
		return nil
	}
	return &InstagramProfile{Handle: fallbackHandle, DisplayName: fallbackHandle}
}

func instagramSourceObjects(value any) []map[string]any {
	switch v := value.(type) {
	case []any:
		if len(v) >= 2 {
			if obj, ok := v[1].(map[string]any); ok {
				return []map[string]any{obj}
			}
		}
		if len(v) >= 3 {
			if obj, ok := v[2].(map[string]any); ok {
				return []map[string]any{obj}
			}
		}
		var out []map[string]any
		for _, item := range v {
			out = append(out, instagramSourceObjects(item)...)
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

func instagramProfileFromGalleryDLObject(obj map[string]any, fallbackHandle string) InstagramProfile {
	fallbackHandle = normalizeInstagramHandle(fallbackHandle)
	if fallbackHandle != "" {
		nested := instagramNestedProfileForHandle(obj, fallbackHandle)
		if instagramProfileHasData(nested) {
			if nested.DisplayName == "" {
				if displayName, ok := instagramCoauthorDisplayName(obj, fallbackHandle); ok {
					nested.DisplayName = displayName
				}
			}
			if nested.DisplayName == "" {
				nested.DisplayName = fallbackHandle
			}
			nested.Handle = fallbackHandle
			return nested
		}
		if displayName, ok := instagramCoauthorDisplayName(obj, fallbackHandle); ok {
			return InstagramProfile{
				Handle:      fallbackHandle,
				DisplayName: displayName,
			}
		}
		topHandle := normalizeInstagramHandle(firstDirectExactString(obj, "username", "owner_username", "uploader_id"))
		if topHandle != "" && topHandle != fallbackHandle {
			return InstagramProfile{}
		}
		if topHandle == "" {
			return InstagramProfile{}
		}
		mediaObject := instagramObjectLooksLikeMedia(obj)
		avatarURL := firstDirectProfileString(obj, mediaObject, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url")
		if mediaObject {
			avatarURL = firstDirectExactString(obj, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url")
		}
		return InstagramProfile{
			Handle:      fallbackHandle,
			DisplayName: firstDirectExactString(obj, "fullname", "full_name", "name"),
			Bio:         firstDirectProfileString(obj, mediaObject, "biography", "bio"),
			Website:     firstDirectProfileString(obj, mediaObject, "external_url", "website"),
			Followers:   firstDirectProfileInt(obj, mediaObject, "edge_followed_by", "followers", "follower_count"),
			Following:   firstDirectProfileInt(obj, mediaObject, "edge_follow", "following", "following_count"),
			Verified:    firstDirectProfileBool(obj, mediaObject, "is_verified", "verified"),
			AvatarURL:   avatarURL,
		}
	}
	handle := normalizeInstagramHandle(firstExactString(obj, "username", "owner_username", "uploader_id"))
	return InstagramProfile{
		Handle:      handle,
		DisplayName: firstExactString(obj, "fullname", "full_name", "name"),
		Bio:         firstExactString(obj, "biography", "bio"),
		Website:     firstExactString(obj, "external_url", "website"),
		Followers:   firstInt(obj, "edge_followed_by", "followers", "follower_count"),
		Following:   firstInt(obj, "edge_follow", "following", "following_count"),
		Verified:    firstBool(obj, "is_verified", "verified"),
		AvatarURL:   firstExactString(obj, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url"),
	}
}

func instagramObjectLooksLikeMedia(obj map[string]any) bool {
	for _, key := range []string{"post_shortcode", "shortcode", "post_url", "webpage_url", "display_url", "media_id"} {
		if stringFromAny(obj[key]) != "" {
			return true
		}
	}
	kind := strings.ToLower(firstDirectExactString(obj, "type", "subcategory"))
	return kind == "post" || kind == "reel" || kind == "story" ||
		strings.Contains(kind, "posts") || strings.Contains(kind, "reels") || strings.Contains(kind, "stories")
}

func firstDirectProfileString(item map[string]any, mediaObject bool, keys ...string) string {
	if mediaObject {
		return ""
	}
	return firstDirectExactString(item, keys...)
}

func firstDirectProfileInt(item map[string]any, mediaObject bool, keys ...string) int {
	if mediaObject {
		return 0
	}
	return firstDirectInt(item, keys...)
}

func firstDirectProfileBool(item map[string]any, mediaObject bool, keys ...string) bool {
	if mediaObject {
		return false
	}
	return firstDirectBool(item, keys...)
}

func instagramProfileHasData(profile InstagramProfile) bool {
	return profile.DisplayName != "" ||
		profile.Bio != "" ||
		profile.Website != "" ||
		profile.Followers > 0 ||
		profile.Following > 0 ||
		profile.Verified ||
		profile.AvatarURL != ""
}

func instagramVideoRefFromGalleryDLObject(obj map[string]any, sourceHandle string) VideoRef {
	kind := strings.ToLower(firstString(obj, "type", "subcategory"))
	if kind == "story" || strings.Contains(kind, "stories") {
		return VideoRef{}
	}
	shortcode := firstString(obj, "post_shortcode", "shortcode")
	if shortcode == "" {
		shortcode = instagramShortcodeFromURL(firstString(obj, "post_url", "url", "webpage_url"))
	}
	if shortcode == "" {
		return VideoRef{}
	}
	prefix := "post"
	if kind == "reel" || strings.Contains(kind, "reel") {
		prefix = "reel"
	}
	handle := normalizeInstagramHandle(firstString(obj, "username", "owner_username", "uploader_id"))
	displayName := firstString(obj, "fullname", "full_name", "name")
	avatarURL := firstExactString(obj, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url")
	if sourceHandle != "" {
		if coauthorDisplayName, ok := instagramCoauthorDisplayName(obj, sourceHandle); ok || handle == "" {
			nested := instagramNestedProfileForHandle(obj, sourceHandle)
			handle = sourceHandle
			if nested.DisplayName != "" {
				displayName = nested.DisplayName
			} else if coauthorDisplayName != "" {
				displayName = coauthorDisplayName
			} else if displayName == "" {
				displayName = sourceHandle
			}
			avatarURL = nested.AvatarURL
		}
	}
	title := firstString(obj, "title", "description", "caption")
	if title == "" {
		title = "Instagram " + prefix
	}
	ref := VideoRef{
		VideoID:           "instagram_" + prefix + "_" + shortcode,
		Title:             title,
		URL:               firstString(obj, "post_url", "url", "webpage_url"),
		ChannelID:         "instagram_" + handle,
		AuthorHandle:      handle,
		AuthorDisplayName: displayName,
		AuthorAvatarURL:   avatarURL,
		PublishedAtMs:     firstMillis(obj, "timestamp", "date", "post_date"),
	}
	if ref.PublishedAtMs == 0 {
		if t := firstTime(obj, "date", "post_date"); t != nil {
			ref.PublishedAtMs = t.UnixMilli()
		}
	}
	if ref.URL == "" {
		if prefix == "reel" {
			ref.URL = "https://www.instagram.com/reel/" + shortcode + "/"
		} else {
			ref.URL = "https://www.instagram.com/p/" + shortcode + "/"
		}
	}
	if ref.ChannelID == "instagram_" {
		ref.ChannelID = ""
	}
	return ref
}

func instagramNestedProfileForHandle(obj map[string]any, handle string) InstagramProfile {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return InstagramProfile{}
	}
	for _, nestedKey := range []string{"user", "owner", "author"} {
		nested, ok := obj[nestedKey].(map[string]any)
		if !ok {
			continue
		}
		username := strings.ToLower(strings.TrimPrefix(firstExactString(nested, "username", "owner_username", "uploader_id"), "@"))
		if username != handle {
			continue
		}
		return InstagramProfile{
			Handle:      username,
			DisplayName: firstExactString(nested, "full_name", "fullname", "name"),
			Bio:         firstExactString(nested, "biography", "bio"),
			Website:     firstExactString(nested, "external_url", "website"),
			AvatarURL:   firstExactString(nested, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url"),
			Followers:   firstInt(nested, "edge_followed_by", "followers", "follower_count", "count_followed"),
			Following:   firstInt(nested, "edge_follow", "following", "following_count", "count_follow"),
			Verified:    firstBool(nested, "is_verified", "verified"),
		}
	}
	return InstagramProfile{}
}

func instagramCoauthorDisplayName(obj map[string]any, handle string) (string, bool) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return "", false
	}
	raw, ok := obj["coauthors"].([]any)
	if !ok {
		return "", false
	}
	for _, item := range raw {
		coauthor, ok := item.(map[string]any)
		if !ok {
			continue
		}
		username := strings.ToLower(strings.TrimPrefix(firstExactString(coauthor, "username"), "@"))
		if username != handle {
			continue
		}
		return firstExactString(coauthor, "full_name", "fullname", "name"), true
	}
	return "", false
}

func instagramTaggedHandleFromObject(obj map[string]any, fallbackHandle string) string {
	fallbackHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fallbackHandle), "@"))
	candidates := []string{
		firstExactString(obj, "tagged_username", "tagged_user", "tagged_handle"),
		fallbackHandle,
	}
	for _, candidate := range candidates {
		handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(candidate), "@"))
		if handle != "" {
			return handle
		}
	}
	return ""
}

func instagramTaggedDisplayNameFromObject(obj map[string]any, handle string) string {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return ""
	}
	raw, ok := obj["tagged_users"].([]any)
	if !ok {
		return ""
	}
	for _, item := range raw {
		tagged, ok := item.(map[string]any)
		if !ok {
			continue
		}
		username := strings.ToLower(strings.TrimPrefix(firstExactString(tagged, "username"), "@"))
		if username != handle {
			continue
		}
		return firstExactString(tagged, "full_name", "fullname", "name")
	}
	return ""
}

func firstExactString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	for _, nestedKey := range []string{"author", "user", "owner"} {
		if nested, ok := item[nestedKey].(map[string]any); ok {
			for _, key := range keys {
				if v, ok := nested[key]; ok {
					if s := stringFromAny(v); s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func firstDirectExactString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := item[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstDirectInt(item map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := intFromAny(item[key]); n > 0 {
			return n
		}
		if nested, ok := item[key].(map[string]any); ok {
			if n := intFromAny(nested["count"]); n > 0 {
				return n
			}
		}
	}
	return 0
}

func firstInt(item map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := intFromAny(item[key]); n > 0 {
			return n
		}
		if nested, ok := item[key].(map[string]any); ok {
			if n := intFromAny(nested["count"]); n > 0 {
				return n
			}
		}
	}
	for _, nestedKey := range []string{"author", "user", "owner"} {
		if nested, ok := item[nestedKey].(map[string]any); ok {
			for _, key := range keys {
				if n := intFromAny(nested[key]); n > 0 {
					return n
				}
				if nestedCount, ok := nested[key].(map[string]any); ok {
					if n := intFromAny(nestedCount["count"]); n > 0 {
						return n
					}
				}
			}
		}
	}
	return 0
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(v.String())
		return n
	default:
		return 0
	}
}

func firstBool(item map[string]any, keys ...string) bool {
	for _, key := range keys {
		if b, ok := item[key].(bool); ok && b {
			return true
		}
	}
	for _, nestedKey := range []string{"author", "user", "owner"} {
		if nested, ok := item[nestedKey].(map[string]any); ok {
			for _, key := range keys {
				if b, ok := nested[key].(bool); ok && b {
					return true
				}
			}
		}
	}
	return false
}

func firstDirectBool(item map[string]any, keys ...string) bool {
	for _, key := range keys {
		if b, ok := item[key].(bool); ok && b {
			return true
		}
	}
	return false
}

func instagramShortcodeFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if (part == "p" || part == "reel" || part == "tv") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func mergeInstagramRefs(refs []VideoRef, limit int) []VideoRef {
	seen := map[string]struct{}{}
	out := make([]VideoRef, 0, len(refs))
	for _, ref := range refs {
		if ref.VideoID == "" {
			continue
		}
		if _, ok := seen[ref.VideoID]; ok {
			continue
		}
		seen[ref.VideoID] = struct{}{}
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i].PublishedAtMs, out[j].PublishedAtMs
		if left == right {
			return false
		}
		if left == 0 {
			return false
		}
		if right == 0 {
			return true
		}
		return left > right
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
