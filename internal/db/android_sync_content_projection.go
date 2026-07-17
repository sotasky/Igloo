package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

const androidSyncProjectionChunkSize = 400

type AndroidSyncChannelProjection struct {
	ChannelID string
	Channel   *model.Channel
	Profile   *model.ChannelProfile
}

type AndroidSyncVideoProjection struct {
	Video                model.Video
	EffectiveRecencyMs   int64
	Comments             []model.Comment
	SponsorBlockSegments []SponsorBlockSegment
	SponsorBlockChecked  *SBCheckedRow
	RepostSources        []model.VideoRepostSource
}

func (db *DB) ListAndroidSyncFeedRankRows(tweetIDs []string, limit int) (int64, []SnapshotRow, error) {
	snapshotAt, err := db.SnapshotComputedAt()
	if err != nil || snapshotAt == 0 || len(tweetIDs) == 0 {
		return snapshotAt, nil, err
	}
	if limit <= 0 {
		return snapshotAt, nil, nil
	}
	rawIDs, err := json.Marshal(sortedUniqueProjectionIDs(tweetIDs))
	if err != nil {
		return 0, nil, err
	}
	rows, err := db.reader().Query(`
		SELECT s.tweet_id, s.rank_position
		FROM feed_rank_snapshot s
		JOIN feed_items fi ON fi.tweet_id = s.tweet_id
		WHERE s.tweet_id IN (SELECT value FROM json_each(?))
		  AND `+feedPrimaryItemPredicate("fi")+`
		  AND `+feedActiveOwnerPredicate("fi")+`
		  AND `+feedUnseenPredicate("fi")+`
		ORDER BY s.rank_position
		LIMIT ?
	`, string(rawIDs), limit)
	if err != nil {
		return 0, nil, fmt.Errorf("list Android sync feed rank: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]SnapshotRow, 0, min(limit, len(tweetIDs)))
	for rows.Next() {
		var row SnapshotRow
		if err := rows.Scan(&row.TweetID, &row.RankPosition); err != nil {
			return 0, nil, err
		}
		out = append(out, row)
	}
	return snapshotAt, out, rows.Err()
}

func (db *DB) ListAndroidSyncChannelProjections(channelIDs []string) ([]AndroidSyncChannelProjection, error) {
	channelIDs = sortedUniqueProjectionIDs(channelIDs)
	if len(channelIDs) == 0 {
		return nil, nil
	}
	byID := make(map[string]AndroidSyncChannelProjection, len(channelIDs))

	for _, chunk := range stringChunks(channelIDs, androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			WITH desired(channel_id) AS (VALUES `+androidSyncProjectionValues(len(chunk))+`)
			SELECT d.channel_id,
			       c.id, c.channel_id, COALESCE(c.source_id, ''), COALESCE(c.name, ''),
			       COALESCE(c.url, ''), COALESCE(c.platform, ''), COALESCE(c.quality, ''),
			       c.last_checked, c.created_at,
			       cp.channel_id, COALESCE(cp.platform, ''), COALESCE(cp.handle, ''),
			       COALESCE(cp.display_name, ''), COALESCE(cp.bio, ''), COALESCE(cp.website, ''),
			       COALESCE(cp.followers, 0), COALESCE(cp.following, 0),
			       COALESCE(cp.verified, 0), COALESCE(cp.verified_type, ''),
			       COALESCE(cp.protected, 0)
			FROM desired d
			LEFT JOIN channels c ON c.channel_id = d.channel_id
			LEFT JOIN channel_profiles cp ON cp.channel_id = d.channel_id AND cp.tombstone = 0
			ORDER BY d.channel_id
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("list Android sync channel projections: %w", err)
		}
		for rows.Next() {
			projection, err := scanAndroidSyncChannelProjection(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			if projection.Channel != nil || projection.Profile != nil {
				byID[projection.ChannelID] = projection
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	out := make([]AndroidSyncChannelProjection, 0, len(byID))
	for _, channelID := range channelIDs {
		if row, ok := byID[channelID]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func scanAndroidSyncChannelProjection(rows *sql.Rows) (AndroidSyncChannelProjection, error) {
	var projection AndroidSyncChannelProjection
	var channelRowID sql.NullInt64
	var channelID sql.NullString
	var sourceID, name, rawURL, platform, quality string
	var lastChecked, createdAt sql.NullInt64
	var profileID sql.NullString
	var profilePlatform, handle, displayName, bio, website string
	var followers, following, verified, protected int
	var verifiedType string
	if err := rows.Scan(
		&projection.ChannelID,
		&channelRowID, &channelID, &sourceID, &name, &rawURL, &platform, &quality,
		&lastChecked, &createdAt,
		&profileID, &profilePlatform, &handle, &displayName, &bio, &website,
		&followers, &following, &verified, &verifiedType, &protected,
	); err != nil {
		return AndroidSyncChannelProjection{}, err
	}

	if channelRowID.Valid && channelID.Valid {
		channel := &model.Channel{
			ID:        channelRowID.Int64,
			ChannelID: channelID.String,
			SourceID:  sourceID,
			Name:      name,
			URL:       rawURL,
			Platform:  detectPlatform(platform, rawURL),
			Quality:   quality,
		}
		channel.LastChecked = millisToTimePtr(lastChecked)
		if created := millisToTimePtr(createdAt); created != nil {
			channel.CreatedAt = *created
		}
		projection.Channel = channel
	}

	if profileID.Valid {
		profile := &model.ChannelProfile{
			ChannelID:    profileID.String,
			Platform:     profilePlatform,
			Handle:       handle,
			DisplayName:  displayName,
			Bio:          bio,
			Website:      website,
			Followers:    followers,
			Following:    following,
			Verified:     verified != 0,
			VerifiedType: verifiedType,
			Protected:    protected != 0,
		}
		projection.Profile = profile
	}
	return projection, nil
}

func (db *DB) ListAndroidSyncVideoProjections(videoIDs []string, commentLimit int) ([]AndroidSyncVideoProjection, error) {
	videoIDs = sortedUniqueProjectionIDs(videoIDs)
	if len(videoIDs) == 0 {
		return nil, nil
	}
	if commentLimit <= 0 || commentLimit > 50 {
		commentLimit = 50
	}
	byID := make(map[string]*AndroidSyncVideoProjection, len(videoIDs))
	for _, chunk := range stringChunks(videoIDs, androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT v.id, v.video_id, COALESCE(v.channel_id, ''), COALESCE(v.owner_kind, ''),
			       COALESCE(v.title, ''), COALESCE(v.description, ''), COALESCE(v.duration, 0),
			       v.published_at, COALESCE(v.metadata_json, ''), COALESCE(v.media_kind, ''),
				       COALESCE(v.slide_count, 0), COALESCE(v.source_kind, ''), COALESCE(v.is_temp, 0),
			       v.dearrow_title, v.dearrow_title_casual, v.dearrow_checked_at
			FROM videos v
			WHERE v.video_id IN (`+placeholders(len(chunk))+`)
			ORDER BY v.video_id
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("list Android sync video projections: %w", err)
		}
		for rows.Next() {
			projection, err := scanAndroidSyncVideoProjection(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			byID[projection.Video.VideoID] = &projection
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	foundIDs := make([]string, 0, len(byID))
	youtubeIDs := make([]string, 0, len(byID))
	for _, videoID := range videoIDs {
		projection, ok := byID[videoID]
		if !ok {
			continue
		}
		foundIDs = append(foundIDs, videoID)
		if strings.HasPrefix(projection.Video.ChannelID, "youtube_") {
			youtubeIDs = append(youtubeIDs, videoID)
		}
	}
	comments, err := db.listAndroidSyncVideoComments(youtubeIDs, commentLimit)
	if err != nil {
		return nil, err
	}
	segments, checked, err := db.listAndroidSyncSponsorBlock(youtubeIDs)
	if err != nil {
		return nil, err
	}
	reposts, err := db.GetVideoRepostSourcesForVideoIDs(foundIDs)
	if err != nil {
		return nil, err
	}
	repostRecency, err := db.listAndroidSyncApplicableRepostRecency(foundIDs)
	if err != nil {
		return nil, err
	}

	out := make([]AndroidSyncVideoProjection, 0, len(foundIDs))
	for _, videoID := range foundIDs {
		projection := byID[videoID]
		if projection.Video.PublishedAt != nil {
			projection.EffectiveRecencyMs = projection.Video.PublishedAt.UnixMilli()
		}
		if repostRecency[videoID] > projection.EffectiveRecencyMs {
			projection.EffectiveRecencyMs = repostRecency[videoID]
		}
		projection.Comments = comments[videoID]
		projection.SponsorBlockSegments = segments[videoID]
		projection.SponsorBlockChecked = checked[videoID]
		projection.RepostSources = reposts[videoID]
		out = append(out, *projection)
	}
	return out, nil
}

func (db *DB) listAndroidSyncApplicableRepostRecency(videoIDs []string) (map[string]int64, error) {
	out := make(map[string]int64)
	if len(videoIDs) == 0 {
		return out, nil
	}
	includeMomentReposts := db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := db.InstagramIncludeTaggedEnabled()
	if !includeMomentReposts && !includeInstagramTagged {
		return out, nil
	}
	for _, chunk := range stringChunks(videoIDs, androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		rows, err := db.reader().Query(`
			SELECT vrs.video_id,
			       MAX(COALESCE(NULLIF(vrs.reposted_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), 0))
			FROM video_repost_sources vrs
			JOIN videos v ON v.video_id = vrs.video_id
			JOIN channel_follows cf ON cf.channel_id = vrs.reposter_channel_id
			LEFT JOIN channel_settings cs ON cs.channel_id = vrs.reposter_channel_id
			WHERE vrs.video_id IN (`+placeholders(len(chunk))+`)
			  AND `+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(cs.include_reposts, 1) != 0
			GROUP BY vrs.video_id
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var videoID string
			var recency int64
			if err := rows.Scan(&videoID, &recency); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[videoID] = recency
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanAndroidSyncVideoProjection(rows *sql.Rows) (AndroidSyncVideoProjection, error) {
	var projection AndroidSyncVideoProjection
	var publishedAt sql.NullInt64
	if err := rows.Scan(
		&projection.Video.ID, &projection.Video.VideoID, &projection.Video.ChannelID,
		&projection.Video.OwnerKind, &projection.Video.Title, &projection.Video.Description,
		&projection.Video.Duration, &publishedAt,
		&projection.Video.MetadataJSON, &projection.Video.MediaKind, &projection.Video.MediaSlideCount,
		&projection.Video.SourceKind, &projection.Video.IsTemp, &projection.Video.DearrowTitle,
		&projection.Video.DearrowTitleCasual, &projection.Video.DearrowCheckedAtMs,
	); err != nil {
		return AndroidSyncVideoProjection{}, err
	}
	projection.Video.PublishedAt = millisToTimePtr(publishedAt)
	hydrateVideoPlatform(&projection.Video)
	return projection, nil
}

func (db *DB) listAndroidSyncVideoComments(videoIDs []string, limit int) (map[string][]model.Comment, error) {
	out := make(map[string][]model.Comment, len(videoIDs))
	for _, chunk := range stringChunks(videoIDs, androidSyncProjectionChunkSize) {
		args := append(stringsToAny(chunk), limit)
		rows, err := db.reader().Query(`
			SELECT video_id, comment_id, parent_id, author_name, author_id,
			       text, like_count, published_at
			FROM (
				SELECT video_id, comment_id, COALESCE(parent_id, '') AS parent_id,
				       COALESCE(author_name, '') AS author_name, COALESCE(author_id, '') AS author_id,
				       COALESCE(text, '') AS text, COALESCE(like_count, 0) AS like_count, published_at,
				       ROW_NUMBER() OVER (
				           PARTITION BY video_id ORDER BY COALESCE(like_count, 0) DESC, comment_id ASC
				       ) AS row_number
				FROM video_comments
				WHERE video_id IN (`+placeholders(len(chunk))+`)
			)
			WHERE row_number <= ?
			ORDER BY video_id, row_number
		`, args...)
		if err != nil {
			return nil, fmt.Errorf("list Android sync video comments: %w", err)
		}
		for rows.Next() {
			var comment model.Comment
			var publishedAt sql.NullInt64
			if err := rows.Scan(
				&comment.VideoID, &comment.CommentID, &comment.ParentID,
				&comment.AuthorName, &comment.AuthorID,
				&comment.Text, &comment.LikeCount, &publishedAt,
			); err != nil {
				_ = rows.Close()
				return nil, err
			}
			comment.PublishedAt = millisToTimePtr(publishedAt)
			comment.Platform = "youtube"
			comment.SetPublishedAtMs()
			out[comment.VideoID] = append(out[comment.VideoID], comment)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (db *DB) listAndroidSyncSponsorBlock(videoIDs []string) (map[string][]SponsorBlockSegment, map[string]*SBCheckedRow, error) {
	segments := make(map[string][]SponsorBlockSegment, len(videoIDs))
	checked := make(map[string]*SBCheckedRow, len(videoIDs))
	for _, chunk := range stringChunks(videoIDs, androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		rows, err := db.reader().Query(`
			SELECT video_id, start_time, end_time, category
			FROM sponsorblock_segments
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			ORDER BY video_id, start_time, end_time, category
		`, args...)
		if err != nil {
			return nil, nil, fmt.Errorf("list Android sync SponsorBlock segments: %w", err)
		}
		for rows.Next() {
			var videoID string
			var segment SponsorBlockSegment
			if err := rows.Scan(&videoID, &segment.Start, &segment.End, &segment.Category); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			segments[videoID] = append(segments[videoID], segment)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, nil, err
		}

		rows, err = db.reader().Query(`
			SELECT video_id, checked_at, COALESCE(video_age_at_check, '')
			FROM sponsorblock_checked
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			ORDER BY video_id
		`, args...)
		if err != nil {
			return nil, nil, fmt.Errorf("list Android sync SponsorBlock checked rows: %w", err)
		}
		for rows.Next() {
			row := &SBCheckedRow{}
			if err := rows.Scan(&row.VideoID, &row.CheckedAtMs, &row.VideoAgeAtCheck); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			checked[row.VideoID] = row
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, nil, err
		}
	}
	return segments, checked, nil
}

func sortedUniqueProjectionIDs(values []string) []string {
	values = uniqueStrings(values)
	sort.Strings(values)
	return values
}

func androidSyncProjectionValues(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("(?),", count), ",")
}
