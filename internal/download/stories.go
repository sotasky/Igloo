package download

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

// StoryRef is a native platform story discovered by gallery-dl. The VideoID is
// Igloo's local media ID; NativeID keeps the platform story/post ID separate.
type StoryRef struct {
	VideoID           string
	NativeID          string
	Title             string
	URL               string
	ChannelID         string
	AuthorHandle      string
	AuthorDisplayName string
	AuthorAvatarURL   string
	PublishedAtMs     int64
}

// TikTokStories fetches native TikTok stories through gallery-dl's /stories
// extractor without downloading media.
func (g *GalleryDLWrapper) TikTokStories(ctx context.Context, handle string, limit int, cookiesFile string) ([]StoryRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rawURL := "https://www.tiktok.com/@" + handle + "/stories"
	args := storyDumpArgs(limit, cookiesFile, rawURL)
	cmd := exec.CommandContext(ctx, "gallery-dl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gallery-dl TikTok stories: %w: %s", err, output)
	}
	return parseTikTokStoryDump(output, handle), nil
}

// InstagramStories fetches native Instagram stories through gallery-dl without
// downloading media.
func (g *GalleryDLWrapper) InstagramStories(ctx context.Context, handle string, limit int, cookiesFile string) ([]StoryRef, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rawURL := "https://www.instagram.com/stories/" + handle + "/"
	output, err := g.instagramDumpOutput(ctx, rawURL, limit, cookiesFile)
	if err != nil {
		return nil, err
	}
	return parseInstagramStoryDump(output, handle), nil
}

func storyDumpArgs(limit int, cookiesFile, rawURL string) []string {
	if limit <= 0 {
		limit = 20
	}
	args := []string{
		"--dump-json",
		"--simulate",
		"-o", "tiktok-range=1-" + strconv.Itoa(limit),
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, rawURL)
	return args
}

func parseTikTokStoryDump(output []byte, fallbackHandle string) []StoryRef {
	fallbackHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fallbackHandle), "@"))
	seen := map[string]struct{}{}
	var refs []StoryRef
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, item := range flattenJSONObjects(payload) {
			ref := tiktokStoryRefFromGalleryDLObject(item, fallbackHandle)
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

func parseInstagramStoryDump(output []byte, fallbackHandle string) []StoryRef {
	fallbackHandle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fallbackHandle), "@"))
	seen := map[string]struct{}{}
	var refs []StoryRef
	for _, payload := range galleryDLJSONPayloads(output) {
		for _, obj := range instagramSourceObjects(payload) {
			ref := instagramStoryRefFromGalleryDLObject(obj, fallbackHandle)
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

func tiktokStoryRefFromGalleryDLObject(item map[string]any, fallbackHandle string) StoryRef {
	nativeID := firstString(item, "video_id", "aweme_id", "id", "post_id")
	if !validTikTokStoryID(nativeID) {
		return StoryRef{}
	}
	handle := tikTokAuthorHandleFromGalleryDLObject(item, firstExactString(item, "webpage_url", "post_url", "url", "permalink"))
	if handle == "" {
		handle = fallbackHandle
	}
	if !validTikTokHandle(handle) {
		return StoryRef{}
	}
	rawURL := "https://www.tiktok.com/@" + handle + "/video/" + nativeID
	title := firstString(item, "title", "description", "desc", "caption")
	if title == "" {
		title = "TikTok story"
	}
	return StoryRef{
		VideoID:           "tiktok_story_" + nativeID,
		NativeID:          nativeID,
		Title:             title,
		URL:               rawURL,
		ChannelID:         "tiktok_" + handle,
		AuthorHandle:      handle,
		AuthorDisplayName: firstString(item, "author_display_name", "nickname", "uploader"),
		PublishedAtMs:     firstMillis(item, "date", "timestamp", "created_at", "createTime"),
	}
}

func instagramStoryRefFromGalleryDLObject(obj map[string]any, fallbackHandle string) StoryRef {
	kind := strings.ToLower(firstString(obj, "type", "subcategory"))
	if kind != "" && kind != "story" && !strings.Contains(kind, "stories") {
		return StoryRef{}
	}
	nativeID := instagramStoryNativeID(obj)
	if nativeID == "" {
		return StoryRef{}
	}
	handle := strings.ToLower(strings.TrimPrefix(firstString(obj, "username", "owner_username", "uploader_id"), "@"))
	if handle == "" {
		handle = fallbackHandle
	}
	if !validInstagramHandle(handle) {
		return StoryRef{}
	}
	rawURL := "https://www.instagram.com/stories/" + handle + "/" + nativeID + "/"
	title := firstString(obj, "title", "description", "caption")
	if title == "" {
		title = "Instagram story"
	}
	return StoryRef{
		VideoID:           "instagram_story_" + nativeID,
		NativeID:          nativeID,
		Title:             title,
		URL:               rawURL,
		ChannelID:         "instagram_" + handle,
		AuthorHandle:      handle,
		AuthorDisplayName: firstString(obj, "fullname", "full_name", "name"),
		AuthorAvatarURL:   firstExactString(obj, "profile_pic_url_hd", "profile_pic_url", "avatar_url", "profile_image_url"),
		PublishedAtMs:     instagramStoryPublishedAtMs(obj),
	}
}

func validTikTokStoryID(id string) bool {
	return allASCIIDigits(id)
}

func validTikTokHandle(handle string) bool {
	return validSocialHandle(handle, 24)
}

func validInstagramHandle(handle string) bool {
	return validSocialHandle(handle, 30)
}

func validSocialHandle(handle string, maxLen int) bool {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" || len(handle) > maxLen {
		return false
	}
	for _, r := range handle {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func instagramStoryNativeID(obj map[string]any) string {
	for _, key := range []string{"media_id", "story_id", "pk", "id"} {
		if v, ok := obj[key]; ok {
			if s := stringFromAny(v); s != "" {
				return s
			}
		}
	}
	for _, key := range []string{"post_url", "url", "webpage_url"} {
		if id := instagramStoryIDFromURL(firstExactString(obj, key)); id != "" {
			return id
		}
	}
	return ""
}

func instagramStoryIDFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	if !IsInstagramURL(u.String()) {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if part != "stories" || i+2 >= len(parts) {
			continue
		}
		id := strings.TrimSpace(parts[i+2])
		if id != "" && allASCIIDigits(id) {
			return id
		}
	}
	return ""
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func instagramStoryPublishedAtMs(obj map[string]any) int64 {
	if ms := firstMillis(obj, "timestamp", "date", "post_date", "taken_at_timestamp"); ms > 0 {
		return ms
	}
	if t := firstTime(obj, "date", "post_date", "taken_at_timestamp"); t != nil {
		return t.UnixMilli()
	}
	return 0
}
