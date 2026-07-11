package db

import (
	"strings"

	"github.com/screwys/igloo/internal/model"
)

func storyCutoffMs(nowMs int64, hours int) int64 {
	hours = NormalizeStoriesWindowHours(hours)
	if nowMs <= 0 {
		return 0
	}
	return nowMs - int64(hours)*3_600_000
}

func storyState(unseenCount, count int) string {
	if count <= 0 {
		return model.StoryStateNone
	}
	if unseenCount > 0 {
		return model.StoryStateNew
	}
	return model.StoryStateSeen
}

func validStoryVideoSQL(videoAlias, channelAlias string) string {
	videoID := videoAlias + ".video_id"
	platform := channelAlias + ".platform"
	suffix := "substr(" + videoID + ", 17)"
	return "(COALESCE(" + platform + ", '') != 'instagram' OR " +
		videoID + " NOT GLOB 'instagram_story_*' OR (" +
		suffix + " != '' AND " + suffix + " NOT GLOB '*[^0-9]*'))"
}

func (db *DB) StoryCutoffMs(nowMs int64) int64 {
	return storyCutoffMs(nowMs, db.StoriesWindowHours())
}

func (db *DB) ListStoryChannels(nowMs int64, limit int) ([]model.StoryChannel, bool, error) {
	if limit <= 0 {
		limit = 200
	}
	cutoff := db.StoryCutoffMs(nowMs)
	query := `
		WITH active AS (
			SELECT v.video_id,
			       v.channel_id,
			       COALESCE(v.published_at, 0) AS published_at,
			       CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
			FROM videos v
			INNER JOIN channels c ON c.channel_id = v.channel_id
			INNER JOIN channel_follows cf ON cf.channel_id = v.channel_id
			LEFT JOIN moment_views mv ON mv.video_id = v.video_id
			WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
			  AND COALESCE(v.source_kind, '') = 'story'
			  AND COALESCE(v.is_temp, 0) = 0
			  AND (? = 0 OR COALESCE(v.published_at, 0) >= ?)
			  AND ` + validStoryVideoSQL("v", "c") + `
		)
		SELECT a.channel_id,
		       COALESCE(c.platform, ''),
		       COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), NULLIF(cp.handle, ''), a.channel_id) AS display_name,
		       COALESCE(NULLIF(cp.handle, ''), NULLIF(c.source_id, ''), '') AS handle,
		       COUNT(*) AS story_count,
		       COALESCE(SUM(a.unseen), 0) AS unseen_count,
		       COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
		       COALESCE((
		           SELECT ax.video_id
		           FROM active ax
		           WHERE ax.channel_id = a.channel_id
		           ORDER BY ax.published_at ASC, ax.video_id ASC
		           LIMIT 1
		       ), '') AS first_video_id,
		       COALESCE((
		           SELECT ax.video_id
		           FROM active ax
		           WHERE ax.channel_id = a.channel_id AND ax.unseen = 1
		           ORDER BY ax.published_at ASC, ax.video_id ASC
		           LIMIT 1
		       ), '') AS first_unseen_video_id
		FROM active a
		INNER JOIN channels c ON c.channel_id = a.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = a.channel_id AND COALESCE(cp.tombstone, 0) = 0
		LEFT JOIN channel_stars cs ON cs.channel_id = a.channel_id
		GROUP BY a.channel_id, c.platform, c.name, c.source_id, cp.display_name, cp.handle, cs.channel_id
		ORDER BY CASE WHEN COALESCE(SUM(a.unseen), 0) > 0 THEN 0 ELSE 1 END,
		         CASE WHEN cs.channel_id IS NOT NULL THEN 0 ELSE 1 END,
		         latest_at_ms DESC,
		         display_name COLLATE NOCASE ASC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, cutoff, cutoff, limit)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := []model.StoryChannel{}
	hasUnseen := false
	for rows.Next() {
		var row model.StoryChannel
		if err := rows.Scan(
			&row.ChannelID,
			&row.Platform,
			&row.DisplayName,
			&row.Handle,
			&row.Count,
			&row.UnseenCount,
			&row.LatestAtMs,
			&row.FirstVideoID,
			&row.FirstUnseenVideoID,
		); err != nil {
			return nil, false, err
		}
		row.State = storyState(row.UnseenCount, row.Count)
		row.AvatarURL = "/api/media/avatar/" + row.ChannelID
		hasUnseen = hasUnseen || row.State == model.StoryStateNew
		out = append(out, row)
	}
	return out, hasUnseen, rows.Err()
}

func (db *DB) GetStoryStatusForChannelIDs(channelIDs []string, nowMs int64) (map[string]model.StoryStatus, error) {
	out := make(map[string]model.StoryStatus, len(channelIDs))
	ids := normalizeStoryChannelIDs(channelIDs)
	if len(ids) == 0 {
		return out, nil
	}
	cutoff := db.StoryCutoffMs(nowMs)
	var args []any
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, cutoff, cutoff)
	query := `
		WITH active AS (
			SELECT v.video_id,
			       v.channel_id,
			       COALESCE(v.published_at, 0) AS published_at,
			       CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
			FROM videos v
			INNER JOIN channels c ON c.channel_id = v.channel_id
			INNER JOIN channel_follows cf ON cf.channel_id = v.channel_id
			LEFT JOIN moment_views mv ON mv.video_id = v.video_id
			WHERE v.channel_id IN (` + placeholders(len(ids)) + `)
			  AND COALESCE(c.platform, '') IN ('tiktok','instagram')
			  AND COALESCE(v.source_kind, '') = 'story'
			  AND COALESCE(v.is_temp, 0) = 0
			  AND (? = 0 OR COALESCE(v.published_at, 0) >= ?)
			  AND ` + validStoryVideoSQL("v", "c") + `
		)
		SELECT a.channel_id,
		       COUNT(*) AS story_count,
		       COALESCE(SUM(a.unseen), 0) AS unseen_count,
		       COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
		       COALESCE((
		           SELECT ax.video_id
		           FROM active ax
		           WHERE ax.channel_id = a.channel_id
		           ORDER BY ax.published_at ASC, ax.video_id ASC
		           LIMIT 1
		       ), '') AS first_video_id,
		       COALESCE((
		           SELECT ax.video_id
		           FROM active ax
		           WHERE ax.channel_id = a.channel_id AND ax.unseen = 1
		           ORDER BY ax.published_at ASC, ax.video_id ASC
		           LIMIT 1
		       ), '') AS first_unseen_video_id
		FROM active a
		GROUP BY a.channel_id
	`
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var row model.StoryStatus
		if err := rows.Scan(
			&row.ChannelID,
			&row.Count,
			&row.UnseenCount,
			&row.LatestAtMs,
			&row.FirstVideoID,
			&row.FirstUnseenVideoID,
		); err != nil {
			return nil, err
		}
		row.State = storyState(row.UnseenCount, row.Count)
		out[row.ChannelID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *DB) AttachStoryStatusToVideos(videos []model.Video, nowMs int64) error {
	if len(videos) == 0 {
		return nil
	}
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.ChannelID)
	}
	statuses, err := db.GetStoryStatusForChannelIDs(ids, nowMs)
	if err != nil {
		return err
	}
	for i := range videos {
		status, ok := statuses[videos[i].ChannelID]
		if !ok {
			videos[i].StoryState = model.StoryStateNone
			continue
		}
		videos[i].StoryState = status.State
		videos[i].StoryCount = status.Count
		videos[i].StoryUnseenCount = status.UnseenCount
		if status.FirstUnseenVideoID != "" {
			videos[i].StoryFirstVideoID = status.FirstUnseenVideoID
		} else {
			videos[i].StoryFirstVideoID = status.FirstVideoID
		}
	}
	return nil
}

func (db *DB) GetStoryVideos(channelID string, nowMs int64) ([]model.Video, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil
	}
	cutoff := db.StoryCutoffMs(nowMs)
	videos, err := db.GetVideos(GetVideosOpts{
		ChannelID:        channelID,
		Platform:         "shorts",
		SourceKind:       "story",
		Limit:            500,
		OrderAsc:         true,
		PublishedAfterMs: cutoff,
	})
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return videos, nil
	}
	viewed, err := db.momentViewedSet(videos)
	if err != nil {
		return nil, err
	}
	status := model.StoryStatus{
		ChannelID:    channelID,
		Count:        len(videos),
		LatestAtMs:   maxVideoPublishedAt(videos),
		FirstVideoID: videos[0].VideoID,
	}
	for i := range videos {
		videos[i].StoryState = model.StoryStateSeen
		videos[i].StoryCount = len(videos)
		videos[i].StoryUnseen = !viewed[videos[i].VideoID]
		if videos[i].StoryUnseen {
			status.UnseenCount++
			if status.FirstUnseenVideoID == "" {
				status.FirstUnseenVideoID = videos[i].VideoID
			}
		}
	}
	status.State = storyState(status.UnseenCount, status.Count)
	first := status.FirstVideoID
	if status.FirstUnseenVideoID != "" {
		first = status.FirstUnseenVideoID
	}
	for i := range videos {
		videos[i].StoryState = status.State
		videos[i].StoryUnseenCount = status.UnseenCount
		videos[i].StoryFirstVideoID = first
	}
	return videos, nil
}

func (db *DB) DeleteExpiredStoryVideos(nowMs int64) (int, error) {
	cutoff := db.StoryCutoffMs(nowMs)
	if cutoff <= 0 {
		return 0, nil
	}
	rows, err := db.conn.Query(`
		SELECT v.video_id
		FROM videos v
		WHERE COALESCE(v.source_kind, '') = 'story'
		  AND COALESCE(v.published_at, 0) < ?
		  AND COALESCE(v.is_pinned, 0) = 0
		  AND NOT EXISTS (
		      SELECT 1
		      FROM bookmarks b
		      WHERE b.video_id = v.video_id
		  )
	`, cutoff)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := db.DeleteVideoWithFile(id); err != nil {
			return len(ids), err
		}
	}
	return len(ids), nil
}

func (db *DB) momentViewedSet(videos []model.Video) (map[string]bool, error) {
	out := map[string]bool{}
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		if strings.TrimSpace(v.VideoID) != "" {
			ids = append(ids, v.VideoID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.conn.Query(`
		SELECT video_id
		FROM moment_views
		WHERE video_id IN (`+placeholders(len(ids))+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func normalizeStoryChannelIDs(channelIDs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(channelIDs))
	for _, raw := range channelIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func maxVideoPublishedAt(videos []model.Video) int64 {
	var maxMs int64
	for _, v := range videos {
		if v.PublishedAt != nil && v.PublishedAt.UnixMilli() > maxMs {
			maxMs = v.PublishedAt.UnixMilli()
		}
	}
	return maxMs
}
