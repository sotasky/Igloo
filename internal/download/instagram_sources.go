package download

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
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
func (g *GalleryDLWrapper) InstagramChannel(ctx context.Context, handle string, limit int, cookiesFile string) ([]VideoRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	var all []VideoRef
	var firstErr error
	anySuccess := false
	for _, suffix := range instagramSourceSuffixes {
		rawURL := "https://www.instagram.com/" + handle + "/" + suffix + "/"
		refs, err := g.instagramDump(ctx, rawURL, limit, "", handle)
		if (err != nil || len(refs) == 0) && cookiesFile != "" {
			cookieRefs, cookieErr := g.instagramDump(ctx, rawURL, limit, cookiesFile, handle)
			if cookieErr == nil && len(cookieRefs) > 0 {
				refs, err = cookieRefs, nil
			} else if err == nil {
				err = cookieErr
			}
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
func (g *GalleryDLWrapper) InstagramTagged(ctx context.Context, handle string, limit int, cookiesFile string) ([]VideoRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rawURL := "https://www.instagram.com/" + handle + "/tagged/"
	output, err := g.instagramTaggedDumpOutput(ctx, rawURL, limit, "")
	refs := ParseInstagramTaggedDumpForHandle(output, handle)
	if (err != nil || len(refs) == 0) && cookiesFile != "" {
		cookieOutput, cookieErr := g.instagramTaggedDumpOutput(ctx, rawURL, limit, cookiesFile)
		cookieRefs := ParseInstagramTaggedDumpForHandle(cookieOutput, handle)
		if cookieErr == nil && len(cookieRefs) > 0 {
			output, err, refs = cookieOutput, nil, cookieRefs
		} else if err == nil {
			err = cookieErr
		}
	}
	if err != nil {
		return refs, err
	}
	return mergeInstagramRefs(refs, limit), nil
}

func (g *GalleryDLWrapper) InstagramProfile(ctx context.Context, handle string, cookiesFile string) (*InstagramProfile, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	var firstErr error
	anySuccess := false
	for _, suffix := range instagramSourceSuffixes {
		rawURL := "https://www.instagram.com/" + handle + "/" + suffix + "/"
		output, err := g.instagramDumpOutput(ctx, rawURL, 1, "")
		profile := ParseInstagramProfileDump(output, handle)
		if (err != nil || profile == nil || profile.AvatarURL == "") && cookiesFile != "" {
			cookieOutput, cookieErr := g.instagramDumpOutput(ctx, rawURL, 1, cookiesFile)
			cookieProfile := ParseInstagramProfileDump(cookieOutput, handle)
			if cookieErr == nil && cookieProfile != nil && (profile == nil || (profile.AvatarURL == "" && cookieProfile.AvatarURL != "")) {
				output, err, profile = cookieOutput, nil, cookieProfile
			} else if err == nil {
				err = cookieErr
			}
		}
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
	if firstErr != nil && !anySuccess {
		return nil, firstErr
	}
	return &InstagramProfile{Handle: handle, DisplayName: handle}, nil
}

func (g *GalleryDLWrapper) instagramDump(ctx context.Context, rawURL string, limit int, cookiesFile string, sourceHandle string) ([]VideoRef, error) {
	output, err := g.instagramDumpOutput(ctx, rawURL, limit, cookiesFile)
	if err != nil {
		return nil, err
	}
	return ParseInstagramChannelDumpForHandle(output, sourceHandle), nil
}

func (g *GalleryDLWrapper) instagramDumpOutput(ctx context.Context, rawURL string, limit int, cookiesFile string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, instagramGalleryDLTimeout)
	defer cancel()
	args := instagramDumpArgs(limit, cookiesFile, rawURL)
	cmd := exec.CommandContext(ctx, "gallery-dl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gallery-dl Instagram timed out after %s for %s", instagramGalleryDLTimeout, rawURL)
		}
		return nil, fmt.Errorf("gallery-dl Instagram: %w: %s", err, output)
	}
	return output, nil
}

func (g *GalleryDLWrapper) instagramTaggedDumpOutput(ctx context.Context, rawURL string, limit int, cookiesFile string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, instagramGalleryDLTimeout)
	defer cancel()
	args := instagramTaggedArgs(limit, cookiesFile, rawURL)
	cmd := exec.CommandContext(ctx, "gallery-dl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gallery-dl Instagram tagged timed out after %s for %s", instagramGalleryDLTimeout, rawURL)
		}
		return nil, fmt.Errorf("gallery-dl Instagram tagged: %w: %s", err, output)
	}
	return output, nil
}

func instagramDumpArgs(limit int, cookiesFile, rawURL string) []string {
	if limit <= 0 {
		limit = 20
	}
	args := []string{
		"--dump-json",
		"--simulate",
		"--range", "1-" + strconv.Itoa(limit),
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, rawURL)
	return args
}

func instagramTaggedArgs(limit int, cookiesFile, rawURL string) []string {
	return instagramDumpArgs(limit, cookiesFile, rawURL)
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
	if displayName, ok := instagramCoauthorDisplayName(obj, fallbackHandle); ok {
		nested := instagramNestedProfileForHandle(obj, fallbackHandle)
		if nested.DisplayName != "" {
			displayName = nested.DisplayName
		}
		return InstagramProfile{
			Handle:      fallbackHandle,
			DisplayName: displayName,
			Bio:         firstExactString(obj, "biography", "bio", "description"),
			Website:     firstExactString(obj, "external_url", "website", "url"),
			Followers:   firstInt(obj, "edge_followed_by", "followers", "follower_count"),
			Following:   firstInt(obj, "edge_follow", "following", "following_count"),
			Verified:    firstBool(obj, "is_verified", "verified"),
			AvatarURL:   nested.AvatarURL,
		}
	}
	handle := normalizeInstagramHandle(firstExactString(obj, "username", "owner_username", "uploader_id"))
	if handle == "" {
		handle = fallbackHandle
	}
	return InstagramProfile{
		Handle:      handle,
		DisplayName: firstExactString(obj, "fullname", "full_name", "name"),
		Bio:         firstExactString(obj, "biography", "bio", "description"),
		Website:     firstExactString(obj, "external_url", "website", "url"),
		Followers:   firstInt(obj, "edge_followed_by", "followers", "follower_count"),
		Following:   firstInt(obj, "edge_follow", "following", "following_count"),
		Verified:    firstBool(obj, "is_verified", "verified"),
		AvatarURL:   firstExactString(obj, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url"),
	}
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
