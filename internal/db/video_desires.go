package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type DownloadLane string

const (
	DownloadLaneCurrent  DownloadLane = "current"
	DownloadLaneBackfill DownloadLane = "backfill"
)

var ErrDownloadNotReady = errors.New("download has no ready canonical media")

// VideoDesire is one observed source item. Ownership and display metadata are
// inputs used to create one canonical job; the durable desire row stores only
// the source root and its scheduling lane.
type VideoDesire struct {
	VideoID            string
	OwnerChannelID     string
	Title              string
	PublishedAtMs      int64
	FreshnessAtMs      int64
	SourcePosition     int
	Lane               DownloadLane
	OwnerAuthoritative bool
}

type VideoDesireSnapshot struct {
	SourceChannelID string
	Component       string
	Items           []VideoDesire
}

type VideoDesireSourceSnapshot struct {
	SourceChannelID string
	Components      []VideoDesireSnapshot
}

type VideoDesireWindowItem struct {
	VideoID        string
	OwnerChannelID string
	Title          string
	PublishedAtMs  int64
	FreshnessAtMs  int64
	SourcePosition int
	Lane           DownloadLane
}

type DownloadWork struct {
	VideoID         string
	SourceChannelID string
	SourceComponent string
	OwnerChannelID  string
	Title           string
	Platform        string
	PublishedAtMs   int64
	Lane            DownloadLane
	RetryCount      int
	LeaseOwner      string
}

func (lane DownloadLane) valid() bool {
	return lane == DownloadLaneCurrent || lane == DownloadLaneBackfill
}

func (db *DB) GetVideoDesireWindow(sourceChannelID, component string) ([]VideoDesireWindowItem, error) {
	sourceChannelID = strings.TrimSpace(sourceChannelID)
	component = strings.TrimSpace(component)
	if sourceChannelID == "" || component == "" {
		return nil, nil
	}
	rows, err := db.reader().Query(`
		SELECT desired.video_id,
		       COALESCE(NULLIF(queued.owner_channel_id, ''), stored.channel_id, ''),
		       COALESCE(NULLIF(queued.title, ''), stored.title, ''),
		       COALESCE(NULLIF(queued.published_at_ms, 0), stored.published_at, 0),
		       COALESCE(NULLIF(provenance.reposted_at_ms, 0),
		                NULLIF(queued.published_at_ms, 0), stored.published_at, 0),
		       desired.source_position, desired.lane
		FROM video_desires desired
		LEFT JOIN download_queue queued ON queued.video_id = desired.video_id
		LEFT JOIN videos stored ON stored.video_id = desired.video_id
		LEFT JOIN video_repost_sources provenance
		  ON provenance.video_id = desired.video_id
		 AND provenance.reposter_channel_id = desired.source_channel_id
		WHERE desired.source_channel_id = ? AND desired.source_component = ?
		ORDER BY desired.source_position, desired.video_id
	`, sourceChannelID, component)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []VideoDesireWindowItem
	for rows.Next() {
		var item VideoDesireWindowItem
		if err := rows.Scan(
			&item.VideoID, &item.OwnerChannelID, &item.Title, &item.PublishedAtMs,
			&item.FreshnessAtMs,
			&item.SourcePosition, &item.Lane,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) ReconcileVideoDesires(snapshot VideoDesireSnapshot) (int, error) {
	return db.ReconcileVideoDesireSource(VideoDesireSourceSnapshot{
		SourceChannelID: snapshot.SourceChannelID,
		Components:      []VideoDesireSnapshot{snapshot},
	})
}

// ReconcileVideoDesireSource publishes every observed component in one
// transaction, so work is never claimable from an over-limit intermediate
// state and component transfers have one deterministic root transition.
func (db *DB) ReconcileVideoDesireSource(snapshot VideoDesireSourceSnapshot) (int, error) {
	sourceChannelID := strings.TrimSpace(snapshot.SourceChannelID)
	if sourceChannelID == "" || len(snapshot.Components) == 0 {
		return 0, fmt.Errorf("video desire source and components are required")
	}
	ownerSet := make(map[string]struct{})
	var ownerIDs []string
	componentSet := make(map[string]struct{}, len(snapshot.Components))
	finalComponents := make(map[string]map[string]struct{})
	for componentIndex := range snapshot.Components {
		component := &snapshot.Components[componentIndex]
		component.SourceChannelID = sourceChannelID
		component.Component = strings.TrimSpace(component.Component)
		if component.Component == "" {
			return 0, fmt.Errorf("video desire component is required")
		}
		if _, duplicate := componentSet[component.Component]; duplicate {
			return 0, fmt.Errorf("duplicate video desire component %s", component.Component)
		}
		componentSet[component.Component] = struct{}{}
		incoming := make(map[string]struct{}, len(component.Items))
		for itemIndex := range component.Items {
			item := &component.Items[itemIndex]
			item.VideoID = strings.TrimSpace(item.VideoID)
			item.OwnerChannelID = strings.TrimSpace(item.OwnerChannelID)
			item.Title = strings.TrimSpace(item.Title)
			if item.VideoID == "" || item.OwnerChannelID == "" || item.SourcePosition < 0 || !item.Lane.valid() {
				return 0, fmt.Errorf("invalid video desire in %s at position %d", component.Component, itemIndex)
			}
			if _, duplicate := incoming[item.VideoID]; duplicate {
				return 0, fmt.Errorf("duplicate video desire %s in %s", item.VideoID, component.Component)
			}
			incoming[item.VideoID] = struct{}{}
			if finalComponents[item.VideoID] == nil {
				finalComponents[item.VideoID] = make(map[string]struct{})
			}
			finalComponents[item.VideoID][component.Component] = struct{}{}
			if _, exists := ownerSet[item.OwnerChannelID]; !exists {
				ownerSet[item.OwnerChannelID] = struct{}{}
				ownerIDs = append(ownerIDs, item.OwnerChannelID)
			}
		}
	}

	nowMs := time.Now().UnixMilli()
	added := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		var followed int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM channel_follows WHERE channel_id = ?)`, sourceChannelID).Scan(&followed); err != nil || followed == 0 {
			return err
		}
		for _, ownerID := range ownerIDs {
			var exists int
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels WHERE channel_id = ?)`, ownerID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return fmt.Errorf("video desire owner channel does not exist: %s", ownerID)
			}
		}

		oldGlobal := make(map[string]bool, len(finalComponents))
		oldOtherSource := make(map[string]bool, len(finalComponents))
		oldSourceComponents := make(map[string]map[string]struct{}, len(finalComponents))
		if len(finalComponents) > 0 {
			videoIDs := make([]string, 0, len(finalComponents))
			for videoID := range finalComponents {
				videoIDs = append(videoIDs, videoID)
			}
			args := stringsToAny(videoIDs)
			rows, err := tx.Query(`
				SELECT video_id, source_channel_id, source_component
				FROM video_desires
				WHERE video_id IN (`+placeholders(len(videoIDs))+`)
			`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var videoID, oldSourceID, component string
				if err := rows.Scan(&videoID, &oldSourceID, &component); err != nil {
					_ = rows.Close()
					return err
				}
				oldGlobal[videoID] = true
				if oldSourceID != sourceChannelID {
					oldOtherSource[videoID] = true
					continue
				}
				if oldSourceComponents[videoID] == nil {
					oldSourceComponents[videoID] = make(map[string]struct{})
				}
				oldSourceComponents[videoID][component] = struct{}{}
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}

		for _, component := range snapshot.Components {
			if _, err := tx.Exec(`
				DELETE FROM video_desires
				WHERE source_channel_id = ? AND source_component = ?
			`, sourceChannelID, component.Component); err != nil {
				return err
			}
		}

		processed := make(map[string]struct{}, len(finalComponents))
		for _, component := range snapshot.Components {
			for _, item := range component.Items {
				ready, queueStatus, canonicalOwner, err := reconcileVideoOwnerTx(tx, item, component.Component)
				if err != nil {
					return err
				}
				item.OwnerChannelID = canonicalOwner
				if _, err := tx.Exec(`
					INSERT INTO video_desires (
						source_channel_id, source_component, video_id, source_position, lane
					) VALUES (?, ?, ?, ?, ?)
				`, sourceChannelID, component.Component, item.VideoID, item.SourcePosition, item.Lane); err != nil {
					return err
				}
				if _, duplicate := processed[item.VideoID]; duplicate {
					continue
				}
				processed[item.VideoID] = struct{}{}
				if ready {
					if _, err := tx.Exec(`
						DELETE FROM download_queue
						WHERE video_id = ?
						  AND (status != 'processing' OR lease_until_ms <= ?)
					`, item.VideoID, nowMs); err != nil {
						return err
					}
					continue
				}
				reactivated := queueStatus == "blocked" && (!oldGlobal[item.VideoID] ||
					(!oldOtherSource[item.VideoID] && disjointComponents(oldSourceComponents[item.VideoID], finalComponents[item.VideoID])))
				if reactivated {
					if _, err := tx.Exec(`
						UPDATE download_queue
						SET status = 'pending', retry_count = 0, next_attempt_at_ms = 0,
						    last_error_kind = '', last_error = '',
						    lease_owner = '', lease_until_ms = 0
						WHERE video_id = ? AND status = 'blocked'
					`, item.VideoID); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(`
					INSERT INTO download_queue (
						video_id, owner_channel_id, title, published_at_ms, status, added_at_ms
					) VALUES (?, ?, ?, ?, 'pending', ?)
					ON CONFLICT(video_id) DO UPDATE SET
						owner_channel_id = excluded.owner_channel_id,
						title = CASE WHEN excluded.title != '' THEN excluded.title ELSE download_queue.title END,
						published_at_ms = CASE WHEN excluded.published_at_ms > 0
							THEN excluded.published_at_ms ELSE download_queue.published_at_ms END
				`, item.VideoID, item.OwnerChannelID, item.Title, item.PublishedAtMs, nowMs); err != nil {
					return err
				}
				if queueStatus == "" || !oldGlobal[item.VideoID] || reactivated {
					added++
				}
			}
		}
		return nil
	})
	return added, err
}

func disjointComponents(left, right map[string]struct{}) bool {
	for component := range left {
		if _, exists := right[component]; exists {
			return false
		}
	}
	return len(left) > 0 && len(right) > 0
}

// EnforceVideoDesireLimit keeps the newest desired videos for one followed
// source across its retained video components. Ephemeral stories expire by
// time and do not consume the normal video limit.
func (db *DB) EnforceVideoDesireLimit(sourceChannelID string, limit int) error {
	sourceChannelID = strings.TrimSpace(sourceChannelID)
	if sourceChannelID == "" || limit <= 0 {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			WITH introduced AS (
				SELECT video_id,
				       MAX(COALESCE(NULLIF(reposted_at_ms, 0), 0)) AS freshness_at_ms
				FROM video_repost_sources
				WHERE reposter_channel_id = ?
				GROUP BY video_id
			), kept AS (
				SELECT desired.video_id
				FROM video_desires desired
				LEFT JOIN download_queue queued ON queued.video_id = desired.video_id
				LEFT JOIN videos stored ON stored.video_id = desired.video_id
				LEFT JOIN introduced ON introduced.video_id = desired.video_id
				WHERE desired.source_channel_id = ?
				  AND desired.source_component != 'stories'
				GROUP BY desired.video_id
				ORDER BY
					MAX(CASE WHEN COALESCE(
						NULLIF(introduced.freshness_at_ms, 0),
						NULLIF(queued.published_at_ms, 0),
						NULLIF(stored.published_at, 0),
						0
					) = 0 THEN 1 ELSE 0 END) DESC,
					MAX(COALESCE(
						NULLIF(introduced.freshness_at_ms, 0),
						NULLIF(queued.published_at_ms, 0),
						stored.published_at,
						0
					)) DESC,
					MIN(desired.source_position),
					desired.video_id
				LIMIT ?
			)
			DELETE FROM video_desires
			WHERE source_channel_id = ?
			  AND source_component != 'stories'
			  AND video_id NOT IN (SELECT video_id FROM kept)
		`, sourceChannelID, sourceChannelID, limit, sourceChannelID)
		if err != nil {
			return err
		}
		return pruneVideoRepostSourcesForDesiresTx(tx, sourceChannelID)
	})
}

func (db *DB) PruneVideoRepostSourcesForDesires(sourceChannelID string) error {
	sourceChannelID = strings.TrimSpace(sourceChannelID)
	if sourceChannelID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		return pruneVideoRepostSourcesForDesiresTx(tx, sourceChannelID)
	})
}

func pruneVideoRepostSourcesForDesiresTx(tx *sql.Tx, sourceChannelID string) error {
	_, err := tx.Exec(`
		DELETE FROM video_repost_sources
		WHERE reposter_channel_id = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM video_desires desired
			WHERE desired.source_channel_id = ?
			  AND desired.video_id = video_repost_sources.video_id
		  )
	`, sourceChannelID, sourceChannelID)
	return err
}

// reconcileVideoOwnerTx preserves canonical ownership unless an introduced
// source provides authoritative original-author evidence.
func reconcileVideoOwnerTx(tx *sql.Tx, item VideoDesire, sourceComponent string) (ready bool, queueStatus, canonicalOwner string, err error) {
	canonicalOwner = item.OwnerChannelID
	storyDesire := sourceComponent == "stories"
	var existingOwner, sourceKind string
	var isTemp bool
	videoFound := false
	err = tx.QueryRow(`
		SELECT channel_id, `+readyVideoMediaExistsSQL("v")+`,
		       COALESCE(is_temp, 0), COALESCE(source_kind, '')
		FROM videos v WHERE video_id = ?
	`, item.VideoID).Scan(&existingOwner, &ready, &isTemp, &sourceKind)
	if err != nil && err != sql.ErrNoRows {
		return false, "", "", err
	}
	if err == nil {
		videoFound = true
		canonicalOwner = existingOwner
		if item.OwnerAuthoritative || storyDesire || strings.HasPrefix(existingOwner, "playlist_") || isTemp || sourceKind == "story" {
			canonicalOwner = item.OwnerChannelID
		}
		_, err = tx.Exec(`
			UPDATE videos
			SET channel_id = ?, is_temp = 0,
			    source_kind = CASE
			      WHEN ? THEN 'story'
			      WHEN channel_id LIKE 'playlist_%' THEN 'playlist'
			      WHEN COALESCE(source_kind, '') = 'story' THEN ''
			      ELSE source_kind END
			WHERE video_id = ?
			  AND (channel_id != ? OR COALESCE(is_temp, 0) != 0
			       OR (? AND COALESCE(source_kind, '') != 'story')
			       OR (NOT ? AND COALESCE(source_kind, '') = 'story'))
		`, canonicalOwner, storyDesire, item.VideoID, canonicalOwner, storyDesire, storyDesire)
		if err != nil || ready {
			return ready, "", canonicalOwner, err
		}
	}

	queueErr := tx.QueryRow(`
		SELECT owner_channel_id, status
		FROM download_queue WHERE video_id = ?
	`, item.VideoID).Scan(&existingOwner, &queueStatus)
	if queueErr == sql.ErrNoRows {
		return false, "", canonicalOwner, nil
	}
	if queueErr != nil {
		return false, "", "", queueErr
	}
	if !videoFound && !item.OwnerAuthoritative {
		canonicalOwner = existingOwner
	}
	if existingOwner != canonicalOwner {
		if _, err := tx.Exec(`
			UPDATE download_queue SET owner_channel_id = ? WHERE video_id = ?
		`, canonicalOwner, item.VideoID); err != nil {
			return false, "", "", err
		}
	}
	return false, queueStatus, canonicalOwner, nil
}

func queryVideoIDsTx(tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func queryAssetFileKeysTx(tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	return keys, rows.Err()
}

func isReadyVideoTx(tx *sql.Tx, videoID string) (bool, error) {
	var ready bool
	err := tx.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM videos v
			WHERE v.video_id = ? AND `+readyVideoMediaExistsSQL("v")+`
		)
	`, videoID).Scan(&ready)
	return ready, err
}

func videoDownloadPlatformSQL(queueAlias, channelAlias string) string {
	return `COALESCE(NULLIF(` + channelAlias + `.platform, ''), CASE
		WHEN ` + queueAlias + `.owner_channel_id LIKE 'youtube_%' THEN 'youtube'
		WHEN ` + queueAlias + `.owner_channel_id LIKE 'tiktok_%' THEN 'tiktok'
		WHEN ` + queueAlias + `.owner_channel_id LIKE 'instagram_%' THEN 'instagram'
		ELSE '' END)`
}

func (db *DB) ClaimDownloadWork(owner string, lane DownloadLane, platform string, nowMs int64, lease time.Duration) (DownloadWork, bool, error) {
	owner = strings.TrimSpace(owner)
	platform = strings.ToLower(strings.TrimSpace(platform))
	if owner == "" || platform == "" || !lane.valid() {
		return DownloadWork{}, false, fmt.Errorf("download claim owner, lane, and platform are required")
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	if lease <= 0 {
		lease = defaultQueueLease
	}
	opts := LeaseOptions{
		Owner: owner, NowMs: nowMs, LeaseMs: lease.Milliseconds(), Limit: 1,
		StatusFrom: "pending", StatusTo: "processing",
	}
	var work DownloadWork
	var found bool
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := `
			SELECT dq.video_id
			FROM download_queue dq
			LEFT JOIN channels owner_channel ON owner_channel.channel_id = dq.owner_channel_id
			WHERE CASE WHEN EXISTS (
				SELECT 1
				FROM video_desires current_desire
				JOIN channel_follows current_follow
				  ON current_follow.channel_id = current_desire.source_channel_id
				WHERE current_desire.video_id = dq.video_id AND current_desire.lane = 'current'
			) THEN 'current' ELSE 'backfill' END = ?
			  AND ` + leaseEligibleSQL() + `
			  AND ` + videoDownloadPlatformSQL("dq", "owner_channel") + ` = ?
			  AND EXISTS (
				SELECT 1
				FROM video_desires desired
				JOIN channel_follows followed ON followed.channel_id = desired.source_channel_id
				WHERE desired.video_id = dq.video_id
			  )
			ORDER BY (
				SELECT MIN(position_desire.source_position)
				FROM video_desires position_desire
				JOIN channel_follows position_follow
				  ON position_follow.channel_id = position_desire.source_channel_id
				WHERE position_desire.video_id = dq.video_id
			) ASC,
			dq.published_at_ms DESC, dq.added_at_ms DESC, dq.video_id ASC
			LIMIT 1`
		ids, err := claimLeasedIDs(tx, "download_queue", "video_id", query, []any{
			lane, nowMs, "pending", nowMs, "processing", nowMs, platform,
		}, opts)
		if err != nil || len(ids) == 0 {
			return err
		}
		if _, err := tx.Exec(`UPDATE download_queue SET started_at_ms = ? WHERE video_id = ?`, nowMs, ids[0]); err != nil {
			return err
		}
		work, err = readDownloadWorkTx(tx, ids[0], lane)
		found = err == nil
		return err
	})
	return work, found, err
}

func readDownloadWorkTx(tx *sql.Tx, videoID string, lane DownloadLane) (DownloadWork, error) {
	work := DownloadWork{Lane: lane}
	err := tx.QueryRow(`
		WITH chosen AS (
			SELECT desired.source_channel_id, desired.source_component
			FROM video_desires desired
			JOIN channel_follows followed ON followed.channel_id = desired.source_channel_id
			WHERE desired.video_id = ?
			ORDER BY CASE WHEN lane = 'current' THEN 0 ELSE 1 END,
			         source_position, desired.source_channel_id, desired.source_component
			LIMIT 1
		)
		SELECT dq.video_id,
		       chosen.source_channel_id, chosen.source_component,
		       dq.owner_channel_id, dq.title,
		       `+videoDownloadPlatformSQL("dq", "owner_channel")+`,
		       dq.published_at_ms, dq.retry_count, dq.lease_owner
		FROM download_queue dq
		JOIN chosen
		LEFT JOIN channels owner_channel ON owner_channel.channel_id = dq.owner_channel_id
		WHERE dq.video_id = ?
	`, videoID, videoID).Scan(
		&work.VideoID, &work.SourceChannelID, &work.SourceComponent, &work.OwnerChannelID, &work.Title,
		&work.Platform, &work.PublishedAtMs, &work.RetryCount, &work.LeaseOwner,
	)
	return work, err
}

func (db *DB) RetryDownloadWork(videoID, owner, errorKind, message string, delay time.Duration, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		var retries int
		if err := tx.QueryRow(`
			SELECT retry_count FROM download_queue
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, strings.TrimSpace(videoID), strings.TrimSpace(owner)).Scan(&retries); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: download_queue %s owner %q", ErrQueueLeaseNotHeld, videoID, owner)
			}
			return err
		}
		if delay <= 0 {
			delay = jobRetryDelay(retries)
		}
		res, err := tx.Exec(`
			UPDATE download_queue
			SET status = 'pending', retry_count = retry_count + 1,
			    next_attempt_at_ms = ?, last_error_kind = ?, last_error = ?,
			    lease_owner = '', lease_until_ms = 0
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, nowMs+delay.Milliseconds(), trimJobError(errorKind), trimJobError(message), videoID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) ReleaseDownloadWork(videoID, owner string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue
			SET status = 'pending', lease_owner = '', lease_until_ms = 0
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, strings.TrimSpace(videoID), strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) BlockDownloadWork(videoID, owner, reason string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue
			SET status = 'blocked', next_attempt_at_ms = 0,
			    last_error_kind = 'not_found', last_error = ?,
			    lease_owner = '', lease_until_ms = 0
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, trimJobError(reason), strings.TrimSpace(videoID), strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) WakeDownloadAuthRetriesForPlatform(platform string) (int, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return 0, nil
	}
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue AS dq
			SET retry_count = 0, next_attempt_at_ms = 0,
			    last_error_kind = '', last_error = ''
			WHERE dq.status = 'pending' AND dq.last_error_kind = 'auth'
			  AND EXISTS (
				SELECT 1 FROM channels owner_channel
				WHERE owner_channel.channel_id = dq.owner_channel_id
				  AND `+videoDownloadPlatformSQL("dq", "owner_channel")+` = ?
			  )
		`, platform)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		affected = int(n)
		return err
	})
	return affected, err
}

func (db *DB) CompleteDownloadWork(videoID, owner string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		ready, err := isReadyVideoTx(tx, strings.TrimSpace(videoID))
		if err != nil {
			return err
		}
		if !ready {
			return ErrDownloadNotReady
		}
		res, err := tx.Exec(`
			DELETE FROM download_queue
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, videoID, strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) RenewDownloadWorkLease(videoID, owner string, nowMs int64, lease time.Duration) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	if lease <= 0 {
		lease = defaultQueueLease
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue SET lease_until_ms = ?
			WHERE video_id = ? AND status = 'processing' AND lease_owner = ?
		`, nowMs+lease.Milliseconds(), strings.TrimSpace(videoID), strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) videoRetentionCutoffs(nowMs int64) (int64, int64) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return nowMs - (24 * time.Hour).Milliseconds(), db.StoryCutoffMs(nowMs)
}

const collectibleVideoWhereSQL = `
	COALESCE(v.owner_kind, '') != 'tweet'
	AND COALESCE(v.source_kind, '') = ''
	AND COALESCE(v.is_temp, 0) = 0
	AND COALESCE(v.is_pinned, 0) = 0
	AND NOT EXISTS (SELECT 1 FROM video_desires d WHERE d.video_id = v.video_id)
	AND NOT EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = v.video_id)
	AND NOT EXISTS (SELECT 1 FROM feed_likes l WHERE l.tweet_id = v.video_id)
	AND NOT EXISTS (
		SELECT 1 FROM download_queue active
		WHERE active.video_id = v.video_id
		  AND active.status = 'processing' AND active.lease_until_ms > ?
	)`

// MaintainVideoRetention is the only automatic owner for expiring local roots,
// retiring orphan work, and collecting canonical video bytes with no root.
func (db *DB) MaintainVideoRetention(nowMs int64) (int, error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	tempCutoffMs, storyCutoffMs := db.videoRetentionCutoffs(nowMs)
	var retiredKeys []string
	collected := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		if storyCutoffMs > 0 {
			if _, err := tx.Exec(`
				DELETE FROM video_desires
				WHERE source_component = 'stories'
				  AND COALESCE(
				    (SELECT NULLIF(queued.published_at_ms, 0)
				     FROM download_queue queued WHERE queued.video_id = video_desires.video_id),
				    (SELECT NULLIF(stored.published_at, 0)
				     FROM videos stored WHERE stored.video_id = video_desires.video_id),
				    (SELECT NULLIF(queued.added_at_ms, 0)
				     FROM download_queue queued WHERE queued.video_id = video_desires.video_id),
				    0
				  ) < ?
			`, storyCutoffMs); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			UPDATE videos
			SET is_temp = CASE
			      WHEN COALESCE(is_temp, 0) = 1 AND downloaded_at > 0 AND downloaded_at < ? THEN 0
			      ELSE is_temp END,
			    source_kind = CASE
			      WHEN ? > 0 AND COALESCE(source_kind, '') = 'story' AND published_at < ? THEN ''
			      ELSE source_kind END
			WHERE COALESCE(is_pinned, 0) = 0
			  AND (
			    (COALESCE(is_temp, 0) = 1 AND downloaded_at > 0 AND downloaded_at < ?)
			    OR (? > 0 AND COALESCE(source_kind, '') = 'story' AND published_at < ?)
			  )
		`, tempCutoffMs, storyCutoffMs, storyCutoffMs,
			tempCutoffMs, storyCutoffMs, storyCutoffMs); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			DELETE FROM download_queue
			WHERE (status != 'processing' OR lease_until_ms <= ?)
			  AND (
			    NOT EXISTS (
			      SELECT 1
			      FROM video_desires desired
			      JOIN channel_follows followed ON followed.channel_id = desired.source_channel_id
			      WHERE desired.video_id = download_queue.video_id
			    )
			    OR EXISTS (
			      SELECT 1 FROM videos v
			      WHERE v.video_id = download_queue.video_id AND `+readyVideoMediaExistsSQL("v")+`
			    )
			  )
		`, nowMs); err != nil {
			return err
		}
		rows, err := tx.Query(`
			SELECT v.video_id, v.owner_kind
			FROM videos v
			WHERE `+collectibleVideoWhereSQL, nowMs)
		if err != nil {
			return err
		}
		type collectibleVideo struct {
			videoID   string
			ownerKind string
		}
		var collectible []collectibleVideo
		for rows.Next() {
			var item collectibleVideo
			if err := rows.Scan(&item.videoID, &item.ownerKind); err != nil {
				_ = rows.Close()
				return err
			}
			collectible = append(collectible, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range collectible {
			keys, err := queryAssetFileKeysTx(tx, `
				SELECT DISTINCT current.file_path
				FROM assets a INDEXED BY idx_assets_owner
				JOIN media_objects current ON current.object_id = a.object_id
				WHERE a.owner_kind = ? AND a.owner_id = ?
				  AND current.published_revision > 0 AND current.file_path != ''
			`, item.ownerKind, item.videoID)
			if err != nil {
				return err
			}
			retiredKeys = append(retiredKeys, keys...)
			if _, err := tx.Exec(`
				DELETE FROM assets
				WHERE owner_kind = ? AND owner_id = ?
			`, item.ownerKind, item.videoID); err != nil {
				return err
			}
			res, err := tx.Exec(`
				DELETE FROM videos
				WHERE video_id = ? AND owner_kind = ?
			`, item.videoID, item.ownerKind)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			collected += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	db.removeRetiredCanonicalFiles(retiredKeys, nil)
	return collected, nil
}
