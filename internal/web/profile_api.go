package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

// fetchProfileFn matches fetchprofile.Fetch so tests can inject fakes.
type fetchProfileFn func(ctx context.Context, channelID string) (*fetchprofile.Profile, error)

var channelIDRe = regexp.MustCompile(`^(twitter|youtube|tiktok|instagram)_[A-Za-z0-9_@.\-]+$`)

const (
	profileCardStaleAfter = 24 * time.Hour
	profileCardFetchTO    = 1500 * time.Millisecond
	tiktokBannerSentinel  = "synth:latest-video:"
)

// profileFlight coalesces concurrent fetches for the same channel_id.
type profileFlight struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

func newProfileFlight() *profileFlight {
	return &profileFlight{m: map[string]chan struct{}{}}
}

func (p *profileFlight) acquire(ctx context.Context, channelID string) (chan struct{}, bool) {
	p.mu.Lock()
	if ch, ok := p.m[channelID]; ok {
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		return nil, false
	}
	ch := make(chan struct{})
	p.m[channelID] = ch
	p.mu.Unlock()
	return ch, true
}

func (p *profileFlight) tryAcquire(channelID string) (chan struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.m[channelID]; ok {
		return nil, false
	}
	ch := make(chan struct{})
	p.m[channelID] = ch
	return ch, true
}

func (p *profileFlight) release(channelID string, done chan struct{}) {
	p.mu.Lock()
	delete(p.m, channelID)
	p.mu.Unlock()
	close(done)
}

func (s *Server) registerProfileAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/profile-card/{channelID}", s.handleProfileCard)
}

func (s *Server) handleProfileCard(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimSpace(r.PathValue("channelID"))
	if !channelIDRe.MatchString(channelID) {
		http.Error(w, "invalid channel_id", http.StatusBadRequest)
		return
	}

	p, _ := s.db.GetChannelProfile(channelID)
	if p != nil && p.Tombstone {
		http.NotFound(w, r)
		return
	}
	if profileCardRenderable(p) {
		refreshing := profileNeedsRenderRefresh(p)
		if refreshing {
			s.refreshProfileCardAsync(channelID, p)
			w.Header().Set("X-Igloo-Profile-Refreshing", "1")
		}
		s.writeProfileCard(w, r, p, s.isChannelFollowed(channelID))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), profileCardFetchTO)
	defer cancel()
	done, leader := s.profileFlight.acquire(ctx, channelID)
	if leader {
		s.refreshOneProfile(ctx, channelID, p)
		s.profileFlight.release(channelID, done)
	}
	p, _ = s.db.GetChannelProfile(channelID)
	if !profileCardRenderable(p) {
		http.NotFound(w, r)
		return
	}
	s.writeProfileCard(w, r, p, s.isChannelFollowed(channelID))
}

func (s *Server) refreshProfileCardAsync(channelID string, existing *model.ChannelProfile) {
	done, ok := s.profileFlight.tryAcquire(channelID)
	if !ok {
		return
	}
	var snapshot *model.ChannelProfile
	if existing != nil {
		cp := *existing
		snapshot = &cp
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), profileCardFetchTO)
		defer cancel()
		defer s.profileFlight.release(channelID, done)
		s.refreshOneProfile(ctx, channelID, snapshot)
	}()
}

func (s *Server) isChannelFollowed(channelID string) bool {
	return s.db.IsChannelFollowed(channelID)
}

func (s *Server) isChannelStarred(channelID string) bool {
	return s.db.IsChannelStarred(channelID)
}

func profileCardRenderable(p *model.ChannelProfile) bool {
	if p == nil || p.Tombstone {
		return false
	}
	return p.FetchedAt != nil || strings.TrimSpace(p.DisplayName) != "" || strings.TrimSpace(p.Handle) != ""
}

func profileFresh(p *model.ChannelProfile) bool {
	return p != nil && p.FetchedAt != nil && time.Since(*p.FetchedAt) < profileCardStaleAfter
}

func profileNeedsRenderRefresh(p *model.ChannelProfile) bool {
	if p == nil {
		return true
	}
	if !profileFresh(p) {
		return true
	}
	if profileTriggersMissingBannerRefresh(p.Platform) && strings.TrimSpace(p.BannerURL) == "" {
		return true
	}
	return false
}

func (s *Server) refreshOneProfile(ctx context.Context, channelID string, existing *model.ChannelProfile) {
	fetch := s.profileFetch
	if fetch == nil {
		fetch = fetchprofile.Fetch
	}
	p, err := fetch(ctx, channelID)
	now := time.Now().UTC()
	if err != nil {
		if errors.Is(err, fetchprofile.ErrNotFound) {
			_ = s.db.UpsertChannelProfile(model.ChannelProfile{
				ChannelID: channelID,
				Platform:  platformOfChannelID(channelID),
				Tombstone: true,
			})
		}
		return
	}
	if err := fetchprofile.ValidateChannelIdentity(channelID, p); err != nil {
		return
	}
	isInstagramStub := instagramFetchProfileIsStub(p)
	if isInstagramStub && existing == nil {
		existing, _ = s.db.GetChannelProfile(channelID)
	}
	if isInstagramStub && existing != nil {
		mergeInstagramStubProfile(p, existing)
	}
	if profileUsesLatestVideoBanner(p.Platform) && p.BannerURL == "" {
		if sentinel, ok := s.synthesizeLatestVideoBanner(channelID, existing); ok {
			p.BannerURL = sentinel
		}
	}
	row := model.ChannelProfile{
		ChannelID:    p.ChannelID,
		Platform:     p.Platform,
		Handle:       p.Handle,
		DisplayName:  p.DisplayName,
		Bio:          p.Bio,
		Website:      p.Website,
		Followers:    p.Followers,
		Following:    p.Following,
		Verified:     p.Verified,
		VerifiedType: p.VerifiedType,
		Protected:    p.Protected,
		AvatarURL:    p.AvatarURL,
		BannerURL:    p.BannerURL,
	}
	if isInstagramStub {
		if existing != nil {
			row.FetchedAt = existing.FetchedAt
			row.FailCount = existing.FailCount
			row.NextRetryAt = existing.NextRetryAt
			row.Tombstone = existing.Tombstone
		}
	} else {
		row.FetchedAt = &now
	}
	if existing != nil && !isInstagramStub {
		row.FailCount = 0
	}
	if err := s.db.UpsertChannelProfile(row); err == nil {
		if s.requestAvatar != nil {
			s.requestAvatar(channelID)
		}
		if channelIDs, err := s.db.SeedProfileBioMentionProfileRows(row); err == nil && s.requestAvatar != nil {
			for _, mentionedChannelID := range channelIDs {
				if mentionedChannelID == "" || mentionedChannelID == channelID {
					continue
				}
				s.requestAvatar(mentionedChannelID)
			}
		}
	}
}

func profileTriggersMissingBannerRefresh(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "tiktok":
		return true
	default:
		return false
	}
}

func profileUsesLatestVideoBanner(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "tiktok", "instagram":
		return true
	default:
		return false
	}
}

func instagramFetchProfileIsStub(p *fetchprofile.Profile) bool {
	if p == nil || strings.ToLower(strings.TrimSpace(p.Platform)) != "instagram" {
		return false
	}
	handle := strings.TrimSpace(p.Handle)
	return strings.TrimSpace(p.Bio) == "" &&
		strings.TrimSpace(p.Website) == "" &&
		strings.TrimSpace(p.AvatarURL) == "" &&
		strings.TrimSpace(p.BannerURL) == "" &&
		p.Followers == 0 &&
		p.Following == 0 &&
		!p.Verified &&
		strings.TrimSpace(p.VerifiedType) == "" &&
		strings.TrimSpace(p.DisplayName) == handle
}

func mergeInstagramStubProfile(p *fetchprofile.Profile, existing *model.ChannelProfile) {
	if p.Handle == "" {
		p.Handle = existing.Handle
	}
	if existing.DisplayName != "" {
		p.DisplayName = existing.DisplayName
	}
	p.Bio = existing.Bio
	p.Website = existing.Website
	p.Followers = existing.Followers
	p.Following = existing.Following
	p.Verified = existing.Verified
	p.VerifiedType = existing.VerifiedType
	p.Protected = existing.Protected
	p.AvatarURL = existing.AvatarURL
	p.BannerURL = existing.BannerURL
}

func (s *Server) synthesizeLatestVideoBanner(channelID string, existing *model.ChannelProfile) (string, bool) {
	videoID, filePath, err := s.db.LatestVideoFileForChannel(channelID)
	if err != nil || videoID == "" {
		return "", false
	}
	srcPath := resolveLatestVideoBannerSource(s.cfg.DataDir, filePath)
	if srcPath == "" {
		return "", false
	}
	sentinel := tiktokBannerSentinel + videoID
	if existing != nil && existing.BannerURL == sentinel && s.resolveBannerPath(channelID) != "" {
		return sentinel, true
	}
	bnDir := filepath.Join(s.cfg.DataDir, "thumbnails", "banners")
	if err := os.MkdirAll(bnDir, 0o755); err != nil {
		return "", false
	}
	if err := copyTikTokBannerFile(srcPath, bnDir, channelID); err != nil {
		return "", false
	}
	return sentinel, true
}

func resolveLatestVideoBannerSource(dataDir, filePath string) string {
	abs := resolveDataPath(dataDir, filePath)
	if abs == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".image":
		if fileExists(abs) {
			return abs
		}
	}
	base := strings.TrimSuffix(abs, filepath.Ext(abs))
	for _, candidateExt := range []string{".image", ".webp", ".jpg", ".jpeg", ".png"} {
		candidate := base + candidateExt
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func copyTikTokBannerFile(srcPath, dir, channelID string) error {
	tmpPath := filepath.Join(dir, channelID+".download")
	if err := copyFile(srcPath, tmpPath); err != nil {
		return err
	}
	if _, err := normalizeProfileMedia(tmpPath, dir, channelID); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() {
		_ = in.Close()
	}()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return out.Close()
}

func normalizeProfileMedia(path, dir, key string) (string, error) {
	contentType := detectImageContentType(path)
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("unexpected content type %q for %s", contentType, path)
	}
	ext := profileMediaExtForContentType(contentType)
	finalPath := filepath.Join(dir, key+ext)
	for _, knownExt := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		candidate := filepath.Join(dir, key+knownExt)
		if candidate == finalPath {
			continue
		}
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove stale media %s: %w", candidate, err)
		}
	}
	if err := os.Rename(path, finalPath); err != nil {
		return "", fmt.Errorf("rename media %s -> %s: %w", path, finalPath, err)
	}
	return finalPath, nil
}

func profileMediaExtForContentType(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}

func platformOfChannelID(channelID string) string {
	switch {
	case strings.HasPrefix(channelID, "twitter_"):
		return "twitter"
	case strings.HasPrefix(channelID, "youtube_"):
		return "youtube"
	case strings.HasPrefix(channelID, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(channelID, "instagram_"):
		return "instagram"
	}
	return ""
}

func (s *Server) writeProfileCard(w http.ResponseWriter, r *http.Request, p *model.ChannelProfile, isFollowing bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = components.ProfileCard(s.pageProps(w, r), p, "hover", isFollowing, s.isChannelStarred(p.ChannelID)).Render(r.Context(), w)
}
