package feed

import (
	"fmt"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/model"
)

func EnrichFeedItems(database *db.DB, items []model.FeedItem, username string) []model.FeedItem {
	items = enrichFeedItems(database, items, username, true)
	items = attachThreadChains(database, items, username)
	return items
}

// EnrichFeedItemsPreserveRows attaches the same per-row state as EnrichFeedItems
// but keeps every input row. Android delta sync is a mutation stream, not a card
// renderer, so presentation deduplication would make Room miss authoritative
// sibling clears.
func EnrichFeedItemsPreserveRows(database *db.DB, items []model.FeedItem, username string) []model.FeedItem {
	return enrichFeedItems(database, items, username, false)
}

// ThreadContextRow is the server-owned Android mirror row for one ancestor in a
// feed item's inline thread preview.
type ThreadContextRow struct {
	LeafTweetID     string `json:"leaf_tweet_id"`
	RootTweetID     string `json:"root_tweet_id"`
	AncestorTweetID string `json:"ancestor_tweet_id"`
	AncestorOrder   int    `json:"ancestor_order"`
}

// ThreadContextRows returns root-to-parent ancestor rows for a feed item.
func ThreadContextRows(database *db.DB, item model.FeedItem) []ThreadContextRow {
	if !item.IsReply || item.ReplyToStatus == "" {
		return []ThreadContextRow{}
	}
	chain, err := database.GetThreadChain(item.TweetID)
	if err != nil || len(chain) <= 1 {
		return []ThreadContextRow{}
	}
	ancestors := chain[:len(chain)-1]
	rootID := ancestors[0].TweetID
	rows := make([]ThreadContextRow, 0, len(ancestors))
	for i, ancestor := range ancestors {
		rows = append(rows, ThreadContextRow{
			LeafTweetID:     item.TweetID,
			RootTweetID:     rootID,
			AncestorTweetID: ancestor.TweetID,
			AncestorOrder:   i,
		})
	}
	return rows
}

// enrichFeedItems attaches media status, channel flags, and personalization.
func enrichFeedItems(database *db.DB, items []model.FeedItem, username string, deduplicate bool) []model.FeedItem {
	if len(items) == 0 {
		return items
	}

	// Collect all tweet IDs (including quote tweet IDs)
	var allIDs []string
	for _, item := range items {
		allIDs = append(allIDs, item.TweetID)
		if item.QuoteTweetID != "" {
			allIDs = append(allIDs, item.QuoteTweetID)
		}
	}

	// Batch fetch enrichment data
	mediaJobs, _ := database.GetFeedMediaJobs(allIDs)
	videos, _ := database.GetVideosByIDs(allIDs)
	seen, _ := database.GetSeenTweetIDs(username, allIDs)
	liked, _ := database.GetFeedLikesForTweetIDs(username, allIDs)
	bookmarkInfo, _ := database.GetBookmarksForVideoIDsRich(allIDs)
	translateTarget, _ := database.GetSetting("translate_target_lang", "en")
	translateTarget = strings.ToLower(strings.TrimSpace(translateTarget))
	if translateTarget == "" {
		translateTarget = "en"
	}
	translateSkipRaw, _ := database.GetSetting("translate_skip_langs", "")
	translateSkipSet := splitTranslateSkipSet(translateSkipRaw)
	translations, _ := database.GetTranslationsForTweetIDs(allIDs, translateTarget)

	// Build channel lookup
	allChannels, _ := database.GetSubscribedChannels()
	channelMap := make(map[string]model.Channel)
	for _, ch := range allChannels {
		channelMap[ch.ChannelID] = ch
	}

	// When ingest misses a quote-author handle but still carries a raw avatar URL,
	// map that URL back to a known profile row so the server can emit the normal
	// avatar proxy path instead of leaking a raw twimg URL to Android.
	rawQuoteAvatarURLs := make([]string, 0, len(items))
	for _, item := range items {
		if item.QuoteAuthorHandle == "" && model.IsRawTwitterProfileAvatar(item.QuoteAuthorAvatarURL) {
			rawQuoteAvatarURLs = append(rawQuoteAvatarURLs, model.NormalizeTwitterAvatarURL(item.QuoteAuthorAvatarURL))
		}
	}
	quoteAvatarChannelIDs, _ := database.GetChannelIDsByAvatarURLs(rawQuoteAvatarURLs)

	// Prefer unified profile rows for identity repair; if those are still
	// missing, fall back to names previously seen on feed rows.
	var profileLookupHandles []string
	for _, item := range items {
		if shouldRepairDisplayName(item.AuthorDisplayName, item.AuthorHandle) {
			profileLookupHandles = append(profileLookupHandles, item.AuthorHandle)
		}
		if shouldRepairDisplayName(item.RetweetedByDisplayName, item.RetweetedByHandle) {
			profileLookupHandles = append(profileLookupHandles, item.RetweetedByHandle)
		}
		if shouldRepairDisplayName(item.QuoteAuthorDisplayName, item.QuoteAuthorHandle) {
			profileLookupHandles = append(profileLookupHandles, item.QuoteAuthorHandle)
		}
	}
	profilesByHandle, _ := database.GetTwitterChannelProfilesByHandles(profileLookupHandles)
	var missingAuthorNameHandles []string
	for i := range items {
		if shouldRepairDisplayName(items[i].AuthorDisplayName, items[i].AuthorHandle) {
			if profile, ok := profilesByHandle[model.NormalizeTwitterHandle(items[i].AuthorHandle)]; ok && profile.DisplayName != "" {
				items[i].AuthorDisplayName = profile.DisplayName
			}
		}
		if shouldRepairDisplayName(items[i].RetweetedByDisplayName, items[i].RetweetedByHandle) {
			if profile, ok := profilesByHandle[model.NormalizeTwitterHandle(items[i].RetweetedByHandle)]; ok && profile.DisplayName != "" {
				items[i].RetweetedByDisplayName = profile.DisplayName
			}
		}
		if shouldRepairDisplayName(items[i].QuoteAuthorDisplayName, items[i].QuoteAuthorHandle) {
			if profile, ok := profilesByHandle[model.NormalizeTwitterHandle(items[i].QuoteAuthorHandle)]; ok && profile.DisplayName != "" {
				items[i].QuoteAuthorDisplayName = profile.DisplayName
			}
		}
		if shouldRepairDisplayName(items[i].AuthorDisplayName, items[i].AuthorHandle) {
			missingAuthorNameHandles = append(missingAuthorNameHandles, items[i].AuthorHandle)
		}
	}
	displayNames, _ := database.GetDisplayNamesForHandles(missingAuthorNameHandles)
	for i := range items {
		if shouldRepairDisplayName(items[i].AuthorDisplayName, items[i].AuthorHandle) {
			if name, ok := displayNames[items[i].AuthorHandle]; ok {
				items[i].AuthorDisplayName = name
			}
		}
		if shouldRepairDisplayName(items[i].QuoteAuthorDisplayName, items[i].QuoteAuthorHandle) {
			if name, ok := displayNames[items[i].QuoteAuthorHandle]; ok {
				items[i].QuoteAuthorDisplayName = name
			}
		}
	}

	// Collect content hashes for retweet grouping
	var contentHashes []string
	for _, item := range items {
		if item.ContentHash != "" {
			contentHashes = append(contentHashes, item.ContentHash)
		}
	}
	retweetSources, _ := database.GetRetweetSources(contentHashes)

	// Enrich each item
	for i := range items {
		item := &items[i]
		enrichMediaStatus(item, mediaJobs)
		annotateChannelFlags(item, channelMap, quoteAvatarChannelIDs)

		item.IsSeen = seen[item.TweetID]
		item.IsLiked = liked[item.TweetID]
		_, item.IsBookmarked = bookmarkInfo[item.TweetID]

		// Attach cached translations
		if tr, ok := translations[item.TweetID]; ok {
			if body, ok := tr["body"]; ok {
				if shouldAttachTranslation(item.BodyText, body.TranslatedText, body.SourceLang, translateSkipSet) {
					item.BodyTranslation = body.TranslatedText
					item.BodySourceLang = body.SourceLang
				}
			}
			if quote, ok := tr["quote"]; ok {
				if shouldAttachTranslation(item.QuoteBodyText, quote.TranslatedText, quote.SourceLang, translateSkipSet) {
					item.QuoteTranslation = quote.TranslatedText
					item.QuoteSourceLang = quote.SourceLang
				}
			}
		}

		if item.QuoteTweetID != "" {
			item.QuoteIsLiked = liked[item.QuoteTweetID]
			_, item.QuoteIsBookmarked = bookmarkInfo[item.QuoteTweetID]
			if item.QuoteAuthorHandle != "" && item.QuoteTweetID != "" {
				item.QuoteCanonicalURL = fmt.Sprintf("https://x.com/%s/status/%s", item.QuoteAuthorHandle, item.QuoteTweetID)
			}
		}

		// Media stream URLs for local video playback
		if job, ok := mediaJobs[item.TweetID]; ok && job.Status == "completed" {
			// YouTube/TikTok downloads are in the videos table
			if v, ok := videos[item.TweetID]; ok && v.FilePath != "" {
				item.MediaStreamURL = "/api/media/stream/" + item.TweetID
				item.MediaPreviewURL = "/api/media/thumbnail/" + item.TweetID
			} else if job.MediaKind == "video" {
				// Feed media videos (GIFs, tweet videos) are in media_files
				item.MediaStreamURL = "/api/media/stream/" + item.TweetID
				item.MediaPreviewURL = "/api/media/thumbnail/" + item.TweetID
			}
		}
		if item.QuoteTweetID != "" {
			if job, ok := mediaJobs[item.QuoteTweetID]; ok && job.Status == "completed" {
				if v, ok := videos[item.QuoteTweetID]; ok && v.FilePath != "" {
					item.QuoteMediaStreamURL = "/api/media/stream/" + item.QuoteTweetID
				} else if job.MediaKind == "video" {
					item.QuoteMediaStreamURL = "/api/media/stream/" + item.QuoteTweetID
				}
			}
			// Fall back to the parent's job for quote-only media tweets.
			// The download worker stores quote media under the parent tweet's job,
			// so there may be no separate job for the quote tweet ID.
			if item.QuoteMediaStreamURL == "" {
				if parentJob, ok := mediaJobs[item.TweetID]; ok && parentJob.Status == "completed" {
					for _, qm := range item.QuoteMedia {
						if qm.Type == "video" || qm.Type == "gif" {
							item.QuoteMediaStreamURL = "/api/media/stream/" + item.QuoteTweetID
							break
						}
					}
				}
			}
			// Final fallback: use slide endpoint for quote videos even without
			// a completed job. The slide endpoint serves downloaded files or
			// proxies from CDN, so the video is always playable.
			if item.QuoteMediaStreamURL == "" {
				for _, qm := range item.QuoteMedia {
					if qm.Type == "video" || qm.Type == "gif" {
						item.QuoteMediaStreamURL = fmt.Sprintf("/api/media/slide/%s/0", item.QuoteTweetID)
						break
					}
				}
			}
		}

		// Slide URLs from media array
		if len(item.Media) > 1 {
			for idx := range item.Media {
				item.MediaSlideURLs = append(item.MediaSlideURLs,
					fmt.Sprintf("/api/media/slide/%s/%d", item.TweetID, idx))
			}
		}

		// Retweet grouping
		if item.ContentHash != "" {
			if sources, ok := retweetSources[item.ContentHash]; ok && len(sources) > 1 {
				visible := visibleRetweeters(sources, item.AuthorHandle, channelMap)
				if len(visible) > 1 {
					item.Retweeters = visible
				}
			}
		}
	}

	// Bidirectional sibling like/bookmark propagation across content_hash groups
	// and canonical status links. If any sibling (original or retweet) is
	// liked/bookmarked, all siblings inherit it.
	var hashIDs []string
	siblings := make(map[string][]string)
	for _, item := range items {
		if item.ContentHash != "" {
			hashIDs = append(hashIDs, item.TweetID)
		}
		if id := model.TwitterStatusIDFromURL(item.CanonicalURL); id != "" && id != item.TweetID {
			siblings[item.TweetID] = append(siblings[item.TweetID], id)
		}
	}
	if len(hashIDs) > 0 {
		contentSiblings, _ := database.FindSiblingTweetIDsForLikes(hashIDs)
		for tweetID, sibs := range contentSiblings {
			siblings[tweetID] = append(siblings[tweetID], sibs...)
		}
	}
	if len(siblings) > 0 {
		var sibIDs []string
		for _, sibs := range siblings {
			sibIDs = append(sibIDs, sibs...)
		}
		sibLiked, _ := database.GetFeedLikesForTweetIDs(username, sibIDs)
		sibBookmarked, _ := database.GetBookmarksForVideoIDs(sibIDs)
		for i := range items {
			for _, sib := range siblings[items[i].TweetID] {
				if sibLiked[sib] {
					items[i].IsLiked = true
				}
				if sibBookmarked[sib] {
					items[i].IsBookmarked = true
				}
			}
		}
	}

	// Personalization: compute affinity-based interest scores
	PersonalizeItems(database, items, username)

	if deduplicate {
		// Collapse retweets sharing the same content into one card for web/feed
		// presentation. Sync callers opt out because every row is a mutation.
		items = deduplicateRetweets(items)
	}

	return items
}

func shouldRepairDisplayName(displayName, handle string) bool {
	if model.NormalizeTwitterHandle(handle) == "" {
		return false
	}
	display := strings.TrimSpace(displayName)
	if display == "" {
		return true
	}
	return model.NormalizeTwitterHandle(display) == model.NormalizeTwitterHandle(handle)
}

func splitTranslateSkipSet(raw string) map[string]bool {
	out := make(map[string]bool)
	for _, lang := range strings.Split(raw, ",") {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang != "" {
			out[lang] = true
		}
	}
	return out
}

func translationSourceAllowed(sourceLang string, skipSet map[string]bool) bool {
	return strings.TrimSpace(sourceLang) == "" || !language.InSet(sourceLang, skipSet)
}

func shouldAttachTranslation(sourceText, translatedText, sourceLang string, skipSet map[string]bool) bool {
	if strings.TrimSpace(translatedText) == "" {
		return false
	}
	if !translationSourceAllowed(sourceLang, skipSet) {
		return false
	}
	return !translationTextEquivalent(sourceText, translatedText)
}

func translationTextEquivalent(a, b string) bool {
	return normalizeTranslationText(a) == normalizeTranslationText(b)
}

func normalizeTranslationText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// attachThreadChains populates ThreadChain on reply items by walking up via
// reply_to_status, then drops items that appear as ancestors of another reply
// in the same page (so the chain is rendered exactly once, owned by the leaf).
// Sibling reply branches that share the same oldest ancestor collapse to the
// first feed-ranked leaf, so the feed renders one thread capsule per
// conversation root instead of one card per branch.
//
// Chain ancestors are themselves run through the basic enrichment helpers
// (channel flags, media status, like/bookmark/seen state) so they render as
// proper feed cards. We don't recurse into chains for the chain ancestors —
// only one level of threading is materialized per page, which is enough to
// reconstruct the conversation in the UI.
func attachThreadChains(database *db.DB, items []model.FeedItem, username string) []model.FeedItem {
	if len(items) == 0 {
		return items
	}

	// Phase 1: fetch chains for every reply with a known parent.
	chainsByLeaf := make(map[string][]model.FeedItem, len(items))
	rootIDsByLeaf := make(map[string]string, len(items))
	ancestorIDs := make(map[string]bool)
	var ancestorList []model.FeedItem
	for i := range items {
		if !items[i].IsReply || items[i].ReplyToStatus == "" {
			continue
		}
		chain, err := database.GetThreadChain(items[i].TweetID)
		if err != nil || len(chain) <= 1 {
			continue
		}
		// chain is [root, ..., leaf]; strip the leaf (it's `items[i]` itself).
		ancestors := chain[:len(chain)-1]
		chainsByLeaf[items[i].TweetID] = ancestors
		rootIDsByLeaf[items[i].TweetID] = ancestors[0].TweetID
		for _, a := range ancestors {
			if !ancestorIDs[a.TweetID] {
				ancestorIDs[a.TweetID] = true
				ancestorList = append(ancestorList, a)
			}
		}
	}

	if len(ancestorList) == 0 {
		return items
	}

	// Phase 2: enrich the ancestors so cards render properly. Pass deduplicate=false
	// — we don't want retweet collapsing to drop chain entries.
	enrichedAncestors := enrichFeedItems(database, ancestorList, username, false)
	enrichedByID := make(map[string]model.FeedItem, len(enrichedAncestors))
	for _, a := range enrichedAncestors {
		enrichedByID[a.TweetID] = a
	}

	// Phase 3: stitch enriched ancestors back into each leaf's ThreadChain.
	for i := range items {
		raw, ok := chainsByLeaf[items[i].TweetID]
		if !ok {
			continue
		}
		chain := make([]model.FeedItem, 0, len(raw))
		for _, a := range raw {
			if e, ok := enrichedByID[a.TweetID]; ok {
				chain = append(chain, e)
			} else {
				chain = append(chain, a)
			}
		}
		items[i].ThreadChain = chain
	}

	// Phase 4: drop standalone items that are an ancestor of another reply in
	// this same page. The leaf will render them as part of its chain.
	emittedThreadRootIDs := make(map[string]bool)
	out := make([]model.FeedItem, 0, len(items))
	for _, item := range items {
		if ancestorIDs[item.TweetID] {
			continue
		}
		if rootID, ok := rootIDsByLeaf[item.TweetID]; ok {
			if emittedThreadRootIDs[rootID] {
				continue
			}
			emittedThreadRootIDs[rootID] = true
		}
		out = append(out, item)
	}
	return out
}

// visibleRetweeters filters a retweeters list by channel settings, keeping
// self-RTs (handle matches the original author) unconditionally and dropping
// retweeters whose channel has include_reposts explicitly set to false.
func visibleRetweeters(rts []model.RetweeterInfo, authorHandle string, channelMap map[string]model.Channel) []model.RetweeterInfo {
	if len(rts) == 0 {
		return rts
	}
	authorLow := strings.ToLower(authorHandle)
	out := make([]model.RetweeterInfo, 0, len(rts))
	for _, r := range rts {
		if strings.ToLower(r.Handle) == authorLow {
			// Self-RT: always visible.
			out = append(out, r)
			continue
		}
		ch, ok := channelMap[r.ChannelID]
		if ok && ch.IncludeReposts != nil && !*ch.IncludeReposts {
			// Muted retweeter: skip.
			continue
		}
		out = append(out, r)
	}
	return out
}

// enrichMediaStatus sets MediaKind, MediaStatus, MediaSlideCount from job/video data.
func enrichMediaStatus(item *model.FeedItem, jobs map[string]model.FeedMediaJob) {
	job, hasJob := jobs[item.TweetID]

	if hasJob && len(item.Media) > 0 {
		// Parent has its own media — set status from the job.
		item.MediaKind = job.MediaKind
		// Prefer parsed Media count over job's SlideCount (which may be 0 for legacy jobs)
		item.MediaSlideCount = len(item.Media)
		switch job.Status {
		case "completed":
			item.MediaStatus = "ready"
		case "queued", "processing":
			item.MediaStatus = "pending"
		case "failed":
			item.MediaStatus = "failed"
		case "pruned":
			item.MediaStatus = "pruned"
		default:
			item.MediaStatus = job.Status
		}
	} else if hasJob && len(item.Media) == 0 {
		// Job exists but parent has no media (quote-only media tweet).
		// Don't set parent media fields — the job only covers quote media.
		// Keep MediaStatus for quote-media stream URL fallback logic.
	} else if len(item.Media) > 0 {
		item.MediaStatus = "cdn"
		if len(item.Media) > 1 {
			item.MediaKind = "slideshow"
			item.MediaSlideCount = len(item.Media)
		} else if item.Media[0].Type == "video" || item.Media[0].Type == "gif" {
			item.MediaKind = "video"
		} else {
			item.MediaKind = "image"
		}
	}
}

// annotateChannelFlags sets ChannelID, ChannelIsFollowed, ChannelIsStarred,
// and rewrites avatar URLs to local proxy.
// Always sets ChannelID so author names link to in-site profiles (even for non-followed accounts).
func annotateChannelFlags(item *model.FeedItem, channels map[string]model.Channel, quoteAvatarChannelIDs map[string]string) {
	if effectiveAuthor := model.EffectiveTwitterAuthorHandle(item.AuthorHandle, item.SourceHandle, item.IsRetweet); effectiveAuthor != item.AuthorHandle {
		item.AuthorHandle = effectiveAuthor
	}
	authorChID := "twitter_" + strings.ToLower(strings.TrimPrefix(item.AuthorHandle, "@"))
	item.ChannelID = authorChID
	item.AuthorAvatarURL = "/api/media/avatar/" + authorChID

	authorFollowed := false
	if ch, ok := channels[authorChID]; ok {
		authorFollowed = ch.IsSubscribed
		item.ChannelIsFollowed = ch.IsSubscribed
		item.ChannelIsStarred = ch.IsStarred
	}

	sourceChID := ""
	if item.SourceHandle != "" && item.SourceHandle != item.AuthorHandle {
		sourceChID = "twitter_" + strings.ToLower(strings.TrimPrefix(item.SourceHandle, "@"))
		item.ReposterChannelID = sourceChID
		// Retweets: inherit followed/starred from the source (retweeter) when
		// the original author isn't followed. This ensures retweets from
		// followed accounts rank alongside their own tweets.
		if !item.ChannelIsFollowed && !item.ChannelIsStarred {
			if ch, ok := channels[sourceChID]; ok {
				item.ChannelIsFollowed = ch.IsSubscribed
				item.ChannelIsStarred = ch.IsStarred
			}
		}
	}

	// Header actions target the displayed author. ChannelIsFollowed may inherit
	// the source/retweeter for ranking, so keep this as direct author truth.
	item.FollowTargetFollowed = authorFollowed

	if item.QuoteAuthorHandle != "" {
		quoteChID := "twitter_" + model.NormalizeTwitterHandle(item.QuoteAuthorHandle)
		item.QuoteChannelID = quoteChID
		item.QuoteAuthorAvatarURL = "/api/media/avatar/" + quoteChID
		if ch, ok := channels[quoteChID]; ok {
			item.QuoteChannelFollowed = ch.IsSubscribed
		}
	} else if quoteChID, ok := quoteAvatarChannelIDs[model.NormalizeTwitterAvatarURL(item.QuoteAuthorAvatarURL)]; ok {
		item.QuoteChannelID = quoteChID
		item.QuoteAuthorAvatarURL = "/api/media/avatar/" + quoteChID
		if item.QuoteAuthorHandle == "" && strings.HasPrefix(quoteChID, "twitter_") && !model.IsSyntheticTwitterAvatarChannelID(quoteChID) {
			item.QuoteAuthorHandle = strings.TrimPrefix(quoteChID, "twitter_")
		}
		if ch, ok := channels[quoteChID]; ok {
			item.QuoteChannelFollowed = ch.IsSubscribed
		}
	} else if quoteChID := model.SyntheticTwitterAvatarChannelID(item.QuoteAuthorAvatarURL); quoteChID != "" {
		item.QuoteAuthorAvatarURL = "/api/media/avatar/" + quoteChID
	}
}
