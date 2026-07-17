package web

import (
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

type androidSyncRetweetSourcesPayload struct {
	Rows []androidSyncRetweetSource `json:"rows"`
}

type androidSyncMaterializationPlan struct {
	Desired                    db.AndroidSyncDesiredSets
	RequestedMediaVideos       map[string]struct{}
	AllRequestedVideosAreMedia bool
	RetweetHashes              map[string]struct{}
}

func newAndroidSyncMaterializationPlan(desired *db.AndroidSyncDesiredSets) *androidSyncMaterializationPlan {
	plan := &androidSyncMaterializationPlan{
		Desired:       emptyAndroidSyncDesiredSets(),
		RetweetHashes: make(map[string]struct{}),
	}
	if desired == nil {
		plan.AllRequestedVideosAreMedia = true
	} else {
		plan.RequestedMediaVideos = desired.MediaVideos
	}
	return plan
}

func (s *Server) buildAndroidSyncBootstrapSelection(
	database *db.DB,
	retention db.AndroidRetentionSettings,
	nowMs int64,
	fullYoutubeMetadata bool,
) ([]model.AndroidSyncHead, db.AndroidSyncDesiredSets, error) {
	sets, err := database.ListAndroidSyncDesiredSetsForMode(retention, nowMs, fullYoutubeMetadata)
	if err != nil {
		return nil, db.AndroidSyncDesiredSets{}, err
	}
	heads := make([]model.AndroidSyncHead, 0, len(sets.Tweets)+len(sets.Videos)+len(sets.Channels))
	appendOwners := func(kind string, ids []string) {
		for _, id := range ids {
			heads = append(heads, model.AndroidSyncHead{OwnerKind: kind, OwnerID: id})
		}
	}
	appendOwners("feed", sets.SortedTweets())
	appendOwners("video", sets.SortedVideos())
	appendOwners("channel", sets.SortedChannels())

	_, ranks, err := database.ListAndroidSyncFeedRankRows(sets.SortedTweets(), androidSyncFeedRankMaxRows)
	if err != nil {
		return nil, db.AndroidSyncDesiredSets{}, err
	}
	for _, rank := range ranks {
		heads = append(heads, model.AndroidSyncHead{OwnerKind: "feed_rank", OwnerID: rank.TweetID})
	}
	stateKeys, err := database.ListAndroidSyncStateKeys()
	if err != nil {
		return nil, db.AndroidSyncDesiredSets{}, err
	}
	for _, key := range stateKeys {
		heads = append(heads, model.AndroidSyncHead{OwnerKind: key.OwnerKind, OwnerID: key.OwnerID})
	}
	for _, key := range androidSyncRelevantSettingKeys() {
		row, err := database.GetAndroidSyncSetting(key)
		if err != nil {
			return nil, db.AndroidSyncDesiredSets{}, err
		}
		if row != nil {
			heads = append(heads, model.AndroidSyncHead{OwnerKind: "setting", OwnerID: key})
		}
	}
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].OwnerKind != heads[j].OwnerKind {
			return heads[i].OwnerKind < heads[j].OwnerKind
		}
		return heads[i].OwnerID < heads[j].OwnerID
	})
	return heads, sets, nil
}

func (s *Server) buildAndroidSyncBootstrapHeads(database *db.DB, retention db.AndroidRetentionSettings, nowMs int64) ([]model.AndroidSyncHead, error) {
	heads, _, err := s.buildAndroidSyncBootstrapSelection(database, retention, nowMs, true)
	return heads, err
}

func (s *Server) buildAndroidSyncChangeSelection(
	database *db.DB,
	retention db.AndroidRetentionSettings,
	nowMs int64,
	heads []model.AndroidSyncHead,
	fullYoutubeMetadata bool,
) (db.AndroidSyncDesiredSets, error) {
	byKind := make(map[string][]string)
	for _, head := range heads {
		byKind[head.OwnerKind] = append(byKind[head.OwnerKind], head.OwnerID)
	}
	if err := s.addAndroidSyncDependentVideoOwners(database, byKind); err != nil {
		return db.AndroidSyncDesiredSets{}, err
	}
	feedIDs := sortedNonEmpty(byKind["feed"])
	if hashes := sortedNonEmpty(byKind["retweet_sources"]); len(hashes) > 0 {
		peers, err := database.ListAndroidSyncFeedIDsByContentHashes(hashes)
		if err != nil {
			return db.AndroidSyncDesiredSets{}, err
		}
		feedIDs = append(feedIDs, peers...)
	}
	if len(feedIDs) > 0 {
		hydrated, err := database.ListAndroidSyncFeedHydrationIDs(feedIDs)
		if err != nil {
			return db.AndroidSyncDesiredSets{}, err
		}
		feedIDs = hydrated
	}
	selection, err := database.ListAndroidSyncDesiredContentAmongForMode(
		retention, nowMs, feedIDs, byKind["video"], fullYoutubeMetadata,
	)
	if err != nil {
		return db.AndroidSyncDesiredSets{}, err
	}
	selection.FeedRanks, err = database.ListAndroidSyncDesiredFeedRanksAmong(
		retention.FeedDays, nowMs, byKind["feed_rank"], androidSyncFeedRankMaxRows,
	)
	if err != nil {
		return db.AndroidSyncDesiredSets{}, err
	}
	return selection, nil
}

func (s *Server) materializeAndroidSyncHeads(database *db.DB, heads []model.AndroidSyncHead, desired *db.AndroidSyncDesiredSets) ([]model.AndroidSyncChange, error) {
	byKind := make(map[string][]string)
	for _, head := range heads {
		byKind[head.OwnerKind] = append(byKind[head.OwnerKind], head.OwnerID)
	}
	if err := s.addAndroidSyncDependentVideoOwners(database, byKind); err != nil {
		return nil, err
	}
	plan := newAndroidSyncMaterializationPlan(desired)

	// A changed retweet group can add or remove every same-hash feed owner.
	retweetHeadHashes := sortedNonEmpty(byKind["retweet_sources"])
	if len(retweetHeadHashes) > 0 {
		feedIDs, err := database.ListAndroidSyncFeedIDsByContentHashes(retweetHeadHashes)
		if err != nil {
			return nil, err
		}
		byKind["feed"] = append(byKind["feed"], feedIDs...)
	}

	changes := make([]model.AndroidSyncChange, 0)
	if desired != nil {
		feedRoots := sortedNonEmpty(byKind["feed"])
		if len(feedRoots) > 0 {
			expanded, err := database.ListAndroidSyncFeedHydrationIDs(feedRoots)
			if err != nil {
				return nil, err
			}
			byKind["feed"] = append(feedRoots, expanded...)
		}
		filterContent := func(kind string, selected map[string]struct{}) {
			kept := make([]string, 0, len(byKind[kind]))
			for _, id := range sortedNonEmpty(byKind[kind]) {
				if _, ok := selected[id]; ok {
					kept = append(kept, id)
				} else {
					changes = append(changes, androidSyncDeleteChange(kind, id))
				}
			}
			byKind[kind] = kept
		}
		filterContent("feed", desired.Tweets)
		filterContent("video", desired.Videos)
		if len(byKind["feed_rank"]) > 0 {
			selectedRanks := desired.FeedRanks
			if selectedRanks == nil {
				_, ranks, err := database.ListAndroidSyncFeedRankRows(
					desired.SortedTweets(), androidSyncFeedRankMaxRows,
				)
				if err != nil {
					return nil, err
				}
				selectedRanks = make(map[string]struct{}, len(ranks))
				for _, rank := range ranks {
					selectedRanks[rank.TweetID] = struct{}{}
				}
			}
			filterContent("feed_rank", selectedRanks)
		}
	}
	appendChanges := func(rows []model.AndroidSyncChange, err error) error {
		if err != nil {
			return err
		}
		changes = append(changes, rows...)
		return nil
	}
	if err := appendChanges(s.androidSyncFeedChanges(database, plan, byKind["feed"])); err != nil {
		return nil, err
	}
	if err := appendChanges(s.androidSyncVideoChanges(database, plan, byKind["video"])); err != nil {
		return nil, err
	}

	stateWanted := make(map[string]map[string]struct{})
	for _, kind := range androidSyncStateOwnerKinds() {
		if len(byKind[kind]) > 0 {
			stateWanted[kind] = stringSet(byKind[kind])
		}
	}
	if err := appendChanges(s.androidSyncStateChanges(database, plan, stateWanted)); err != nil {
		return nil, err
	}
	for _, hash := range retweetHeadHashes {
		plan.RetweetHashes[hash] = struct{}{}
	}
	if err := appendChanges(s.androidSyncRetweetSourceChanges(database, plan)); err != nil {
		return nil, err
	}
	if err := appendChanges(s.androidSyncFeedRankChanges(database, byKind["feed_rank"])); err != nil {
		return nil, err
	}
	if err := appendChanges(s.androidSyncSettingChanges(database, byKind["setting"])); err != nil {
		return nil, err
	}
	if err := appendChanges(s.androidSyncChannelChanges(database, plan, byKind["channel"])); err != nil {
		return nil, err
	}
	if err := appendChanges(s.androidSyncAssetChanges(database, byKind["asset"])); err != nil {
		return nil, err
	}
	assets, _, err := s.buildAndroidSyncAssets(database, plan.Desired)
	if err != nil {
		return nil, err
	}
	if err := appendChanges(marshalAndroidSyncAssetPayloads(assets)); err != nil {
		return nil, err
	}
	return dedupeAndroidSyncChanges(changes), nil
}

func (s *Server) addAndroidSyncDependentVideoOwners(database *db.DB, byKind map[string][]string) error {
	assetVideoIDs, err := database.ListAndroidSyncVideoIDsForAssetIDs(byKind["asset"])
	if err != nil {
		return err
	}
	byKind["video"] = append(byKind["video"], assetVideoIDs...)
	return nil
}

func (s *Server) androidSyncFeedChanges(database *db.DB, plan *androidSyncMaterializationPlan, requested []string) ([]model.AndroidSyncChange, error) {
	requested = sortedNonEmpty(requested)
	if len(requested) == 0 {
		return nil, nil
	}
	ids := requested
	projection, err := database.ListAndroidSyncFeedProjection(ids)
	if err != nil {
		return nil, err
	}
	recency, err := database.ListAndroidSyncFeedEffectiveRecency(ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		plan.Desired.Tweets[id] = struct{}{}
		plan.Desired.TweetAssetOwners[id] = struct{}{}
		row, ok := projection.Rows[id]
		if !ok {
			continue
		}
		if quoteID := strings.TrimSpace(row.Item.QuoteTweetID); quoteID != "" {
			plan.Desired.TweetAssetOwners[quoteID] = struct{}{}
		}
		retweets := projection.RetweetSources[row.Item.ContentHash]
		addAndroidSyncFeedChannelRefs(plan.Desired.Channels, row.Item, retweets)
		if row.Item.ContentHash != "" {
			plan.RetweetHashes[row.Item.ContentHash] = struct{}{}
		}
	}

	emitted := make(map[string]struct{}, len(ids))
	changes := make([]model.AndroidSyncChange, 0, len(ids))
	for _, id := range ids {
		row, ok := projection.Rows[id]
		if !ok {
			continue
		}
		change, err := marshalAndroidSyncChange(
			"feed", id, "feed", recency[id],
			androidSyncFeedPayload{Item: androidSyncFeedItemFromModel(row.Item)},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
		emitted[id] = struct{}{}
	}
	for _, id := range requested {
		if _, ok := emitted[id]; !ok {
			changes = append(changes, androidSyncDeleteChange("feed", id))
		}
	}
	return changes, nil
}

func (s *Server) androidSyncVideoChanges(database *db.DB, plan *androidSyncMaterializationPlan, requested []string) ([]model.AndroidSyncChange, error) {
	requested = sortedNonEmpty(requested)
	if len(requested) == 0 {
		return nil, nil
	}
	projections, err := database.ListAndroidSyncVideoProjections(requested, youtubeCommentsCap)
	if err != nil {
		return nil, err
	}
	emitted := make(map[string]struct{}, len(projections))
	changes := make([]model.AndroidSyncChange, 0, len(projections))
	for _, projection := range projections {
		video := projection.Video
		plan.Desired.Videos[video.VideoID] = struct{}{}
		if plan.AllRequestedVideosAreMedia {
			plan.Desired.MediaVideos[video.VideoID] = struct{}{}
		} else if _, ok := plan.RequestedMediaVideos[video.VideoID]; ok {
			plan.Desired.MediaVideos[video.VideoID] = struct{}{}
		}
		if video.ChannelID != "" {
			plan.Desired.Channels[video.ChannelID] = struct{}{}
		}
		comments := make([]androidSyncComment, 0, len(projection.Comments))
		for _, comment := range projection.Comments {
			comments = append(comments, androidSyncCommentFromModel(comment))
		}
		segments := projection.SponsorBlockSegments
		if segments == nil {
			segments = []db.SponsorBlockSegment{}
		}
		reposts := make([]androidSyncVideoRepostSource, 0, len(projection.RepostSources))
		for _, source := range projection.RepostSources {
			reposts = append(reposts, androidSyncVideoRepostSource{
				ReposterChannelID: source.ReposterChannelID,
				RepostedAtMs:      source.RepostedAtMs,
				FirstSeenAtMs:     source.FirstSeenAtMs,
				UpdatedAtMs:       source.UpdatedAtMs,
			})
			if source.ReposterChannelID != "" {
				plan.Desired.Channels[source.ReposterChannelID] = struct{}{}
			}
		}
		var checked *androidSyncSponsorBlockCheck
		if projection.SponsorBlockChecked != nil {
			checked = &androidSyncSponsorBlockCheck{
				CheckedAtMs:     projection.SponsorBlockChecked.CheckedAtMs,
				VideoAgeAtCheck: projection.SponsorBlockChecked.VideoAgeAtCheck,
			}
		}
		payload := androidSyncVideoPayload{
			Item:                 androidSyncVideoItemFromProjection(projection),
			Comments:             comments,
			SponsorBlockSegments: segments,
			SponsorBlockChecked:  checked,
			RepostSources:        reposts,
		}
		change, err := marshalAndroidSyncChange(
			"video", video.VideoID, androidSyncVideoRetentionBucket(video),
			androidSyncVideoEffectiveRecency(projection), payload,
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
		emitted[video.VideoID] = struct{}{}
	}
	for _, id := range requested {
		if _, ok := emitted[id]; !ok {
			changes = append(changes, androidSyncDeleteChange("video", id))
		}
	}
	return changes, nil
}

func (s *Server) androidSyncStateChanges(database *db.DB, plan *androidSyncMaterializationPlan, wanted map[string]map[string]struct{}) ([]model.AndroidSyncChange, error) {
	keys := make([]db.AndroidSyncStateKey, 0)
	for _, kind := range androidSyncStateOwnerKinds() {
		for _, id := range sortedMapKeys(wanted[kind]) {
			keys = append(keys, db.AndroidSyncStateKey{OwnerKind: kind, OwnerID: id})
		}
	}
	rows, err := database.ListAndroidSyncStateProjections(keys)
	if err != nil {
		return nil, err
	}
	rowByKey := make(map[string]db.AndroidSyncStateProjection, len(rows))
	for _, row := range rows {
		rowByKey[row.OwnerKind+"\x00"+row.OwnerID] = row
		if row.ChannelID != "" {
			plan.Desired.Channels[row.ChannelID] = struct{}{}
		}
	}
	var changes []model.AndroidSyncChange
	for _, kind := range androidSyncStateOwnerKinds() {
		for _, id := range sortedMapKeys(wanted[kind]) {
			row, ok := rowByKey[kind+"\x00"+id]
			if !ok {
				changes = append(changes, androidSyncDeleteChange(kind, id))
				continue
			}
			change, err := marshalAndroidSyncChange(row.OwnerKind, row.OwnerID, "", 0, row.Payload)
			if err != nil {
				return nil, err
			}
			changes = append(changes, change)
		}
	}
	return changes, nil
}

func (s *Server) androidSyncRetweetSourceChanges(database *db.DB, plan *androidSyncMaterializationPlan) ([]model.AndroidSyncChange, error) {
	hashes := sortedMapKeys(plan.RetweetHashes)
	if len(hashes) == 0 {
		return nil, nil
	}
	rows, err := database.GetRetweetSourceRows(hashes)
	if err != nil {
		return nil, err
	}
	changes := make([]model.AndroidSyncChange, 0, len(hashes))
	for _, hash := range hashes {
		group := androidSyncRetweetSources(rows[hash])
		if len(group) == 0 {
			changes = append(changes, androidSyncDeleteChange("retweet_sources", hash))
			continue
		}
		change, err := marshalAndroidSyncChange(
			"retweet_sources", hash, "", 0,
			androidSyncRetweetSourcesPayload{Rows: group},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
		for _, row := range rows[hash] {
			if row.RetweeterChannelID != "" {
				plan.Desired.Channels[row.RetweeterChannelID] = struct{}{}
			}
		}
	}
	return changes, nil
}

func (s *Server) androidSyncChannelChanges(database *db.DB, plan *androidSyncMaterializationPlan, requested []string) ([]model.AndroidSyncChange, error) {
	for _, id := range sortedNonEmpty(requested) {
		plan.Desired.Channels[id] = struct{}{}
	}
	ids := plan.Desired.SortedChannels()
	if len(ids) == 0 {
		return nil, nil
	}
	projections, err := database.ListAndroidSyncChannelProjections(ids)
	if err != nil {
		return nil, err
	}
	payloads := make(map[string]androidSyncChannelPayload, len(projections))
	for _, projection := range projections {
		payloads[projection.ChannelID] = androidSyncChannelPayloadFromProjection(projection)
	}
	changes := make([]model.AndroidSyncChange, 0, len(ids))
	for _, id := range ids {
		payload, ok := payloads[id]
		if !ok {
			changes = append(changes, androidSyncDeleteChange("channel", id))
			continue
		}
		change, err := marshalAndroidSyncChange("channel", id, "", 0, payload)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (s *Server) androidSyncFeedRankChanges(database *db.DB, ids []string) ([]model.AndroidSyncChange, error) {
	ids = sortedNonEmpty(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := database.ListAndroidSyncFeedRankProjections(ids)
	if err != nil {
		return nil, err
	}
	changes := make([]model.AndroidSyncChange, 0, len(ids))
	for _, id := range ids {
		row, ok := rows[id]
		if !ok {
			changes = append(changes, androidSyncDeleteChange("feed_rank", id))
			continue
		}
		change, err := marshalAndroidSyncChange("feed_rank", id, "", 0, row)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (s *Server) androidSyncAssetChanges(database *db.DB, ids []string) ([]model.AndroidSyncChange, error) {
	ids = sortedNonEmpty(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := database.ListAndroidSyncAssetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	changes := make([]model.AndroidSyncChange, 0, len(ids))
	for _, id := range ids {
		row, ok := rows[id]
		if !ok || row.State == db.AssetStatePruned {
			changes = append(changes, androidSyncDeleteChange("asset", id))
			continue
		}
		change, err := marshalAndroidSyncChange("asset", id, "", 0, s.androidSyncAssetFromInventory(row))
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (s *Server) androidSyncSettingChanges(database *db.DB, keys []string) ([]model.AndroidSyncChange, error) {
	keys = sortedNonEmpty(keys)
	changes := make([]model.AndroidSyncChange, 0, len(keys))
	for _, key := range keys {
		row, err := database.GetAndroidSyncSetting(key)
		if err != nil {
			return nil, err
		}
		if row == nil {
			changes = append(changes, androidSyncDeleteChange("setting", key))
			continue
		}
		change, err := marshalAndroidSyncChange("setting", key, "", 0, row)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func emptyAndroidSyncDesiredSets() db.AndroidSyncDesiredSets {
	return db.AndroidSyncDesiredSets{
		Tweets: map[string]struct{}{}, TweetAssetOwners: map[string]struct{}{},
		Videos: map[string]struct{}{}, MediaVideos: map[string]struct{}{}, Channels: map[string]struct{}{},
	}
}

func addAndroidSyncFeedChannelRefs(into map[string]struct{}, item model.FeedItem, retweets []db.RetweetSourceRow) {
	refs := []string{item.SourceChannelID, item.ChannelID, item.QuoteChannelID, item.ReplyChannelID, item.ReposterChannelID}
	for _, row := range retweets {
		refs = append(refs, row.RetweeterChannelID)
	}
	for _, id := range sortedNonEmpty(refs) {
		into[id] = struct{}{}
	}
}

func androidSyncRetweetSources(rows []db.RetweetSourceRow) []androidSyncRetweetSource {
	out := make([]androidSyncRetweetSource, 0, len(rows))
	for _, row := range rows {
		out = append(out, androidSyncRetweetSource{
			ContentHash:        row.ContentHash,
			RetweeterChannelID: row.RetweeterChannelID,
			TweetID:            row.TweetID,
			PublishedAt:        row.PublishedAt,
		})
	}
	return out
}

func marshalAndroidSyncAssetPayloads(assets []model.AndroidSyncAsset) ([]model.AndroidSyncChange, error) {
	seen := make(map[string]struct{}, len(assets))
	changes := make([]model.AndroidSyncChange, 0, len(assets))
	for _, asset := range assets {
		if _, ok := seen[asset.AssetID]; ok {
			continue
		}
		seen[asset.AssetID] = struct{}{}
		change, err := marshalAndroidSyncChange("asset", asset.AssetID, "", 0, asset)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func androidSyncVideoRetentionBucket(video model.Video) string {
	if video.SourceKind == "story" {
		return "story"
	}
	if androidSyncPlatformForOwnerKind(video.OwnerKind) == "youtube" {
		// The Android client retains this metadata and independently prunes its
		// automatic primary binary.
		return "youtube"
	}
	return "moments"
}

func androidSyncVideoEffectiveRecency(projection db.AndroidSyncVideoProjection) int64 {
	if androidSyncVideoRetentionBucket(projection.Video) == "moments" {
		return projection.EffectiveRecencyMs
	}
	if projection.Video.PublishedAt != nil {
		return projection.Video.PublishedAt.UnixMilli()
	}
	return 0
}

func androidSyncStateOwnerKinds() []string {
	return []string{
		"feed_like", "bookmark", "bookmark_category", "feed_seen", "moment_view",
		"watch_history", "muted_channel", "channel_follow", "channel_star",
		"channel_setting", "moments_cursor",
	}
}

func androidSyncRelevantSettingKeys() []string {
	return []string{
		"moments_include_reposts_default", "instagram_include_tagged_default",
		"include_reposts_default", "translate_target_lang", "translate_skip_langs",
	}
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func sortedMapKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedNonEmpty(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedMapKeys(set)
}

func dedupeAndroidSyncChanges(changes []model.AndroidSyncChange) []model.AndroidSyncChange {
	seen := make(map[string]struct{}, len(changes))
	out := changes[:0]
	for _, change := range changes {
		key := change.OwnerKind + "\x00" + change.OwnerID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, change)
	}
	return out
}
