package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const (
	profileObservationRefreshInterval = 24 * time.Hour
	defaultProfileJobLease            = 15 * time.Minute
)

type profileObservation struct {
	channelID   string
	platform    string
	handle      string
	displayName string
	avatarURL   string
	observedAt  int64
}

type profileObservationState struct {
	exists      bool
	handle      string
	displayName string
	avatarURL   string
	identityAt  int64
	fetchedAt   int64
	tombstone   bool
	requested   int64
	completed   int64
	hasJob      bool
}

func observeChannelProfileTx(tx *sql.Tx, channel model.Channel, observedAt int64) error {
	applyChannelIDDefaults(&channel)
	channel.Platform = detectPlatform(channel.Platform, channel.URL)
	handle := strings.TrimSpace(channel.Handle)
	if handle == "" {
		switch channel.Platform {
		case "twitter":
			handle = strings.TrimPrefix(strings.TrimPrefix(channel.ChannelID, "twitter_"), "x_")
		case "tiktok":
			handle = model.TikTokHandleFromChannelID(channel.ChannelID)
		case "instagram":
			handle = model.InstagramHandleFromChannelID(channel.ChannelID)
		case "youtube":
			if strings.HasPrefix(strings.TrimSpace(channel.SourceID), "@") {
				handle = channel.SourceID
			}
		}
	}
	return observeProfileTx(tx, profileObservation{
		channelID:   channel.ChannelID,
		platform:    channel.Platform,
		handle:      handle,
		displayName: strings.TrimSpace(channel.DisplayName),
		observedAt:  observedAt,
	})
}

func observeProfileTx(tx *sql.Tx, observation profileObservation) error {
	var ok bool
	observation, ok = normalizeProfileObservation(observation)
	if !ok {
		return nil
	}

	state, err := readProfileObservationStateTx(tx, observation.channelID)
	if err != nil {
		return err
	}
	_, shouldRequest := profileObservationDecision(state, observation)

	if _, err := tx.Exec(`
		INSERT INTO channel_profiles (
			channel_id, platform, handle, display_name, observed_at_ms, tombstone
		) VALUES (?, ?, ?, ?, ?, 0)
		ON CONFLICT(channel_id) DO UPDATE SET
			platform = excluded.platform,
			handle = CASE
				WHEN COALESCE(channel_profiles.handle, '') = '' THEN excluded.handle
				ELSE channel_profiles.handle
			END,
			display_name = CASE
				WHEN COALESCE(excluded.display_name, '') = '' THEN channel_profiles.display_name
				WHEN COALESCE(channel_profiles.display_name, '') = '' THEN excluded.display_name
				WHEN channel_profiles.fetched_at = 0
				 AND excluded.observed_at_ms >= channel_profiles.observed_at_ms THEN excluded.display_name
				ELSE channel_profiles.display_name
			END,
			observed_at_ms = MAX(channel_profiles.observed_at_ms, excluded.observed_at_ms),
			tombstone = CASE
				WHEN excluded.observed_at_ms >= channel_profiles.fetched_at THEN 0
				ELSE channel_profiles.tombstone
			END
	`, observation.channelID, observation.platform, nilIfEmpty(observation.handle), nilIfEmpty(observation.displayName), observation.observedAt); err != nil {
		return err
	}

	if !state.hasJob {
		_, err := tx.Exec(`
			INSERT INTO profile_jobs (
				channel_id, requested_revision, completed_revision,
				requested_at_ms, updated_at_ms
			) VALUES (?, 1, 0, ?, ?)
		`, observation.channelID, observation.observedAt, observation.observedAt)
		return err
	}

	if !shouldRequest {
		return nil
	}
	_, err = tx.Exec(`
		UPDATE profile_jobs
		SET requested_revision = requested_revision + 1,
			requested_at_ms = ?,
			next_attempt_at_ms = 0,
			last_error = '',
			updated_at_ms = ?
		WHERE channel_id = ?
	`, observation.observedAt, observation.observedAt, observation.channelID)
	return err
}

func normalizeProfileObservation(observation profileObservation) (profileObservation, bool) {
	observation.channelID = strings.TrimSpace(observation.channelID)
	observation.platform = strings.ToLower(strings.TrimSpace(observation.platform))
	switch observation.platform {
	case "twitter", "x":
		observation.platform = "twitter"
		observation.handle = model.NormalizeTwitterHandle(observation.handle)
	case "tiktok":
		observation.handle = model.NormalizeTikTokHandle(observation.handle)
	case "instagram":
		observation.handle = model.NormalizeInstagramHandle(observation.handle)
	case "youtube":
		observation.handle = strings.TrimSpace(observation.handle)
	default:
		return observation, false
	}
	observation.displayName = strings.TrimSpace(observation.displayName)
	observation.avatarURL = strings.TrimSpace(model.CleanFeedAvatarURL(observation.avatarURL))
	if observation.platform == "twitter" && !model.IsRawTwitterProfileAvatar(observation.avatarURL) {
		observation.avatarURL = ""
	}
	if observation.channelID == "" || observation.platform == "" {
		return observation, false
	}
	if observation.observedAt <= 0 {
		observation.observedAt = time.Now().UnixMilli()
	}
	return observation, true
}

func profileObservationDecision(state profileObservationState, observation profileObservation) (currentObservation, shouldRequest bool) {
	currentObservation = !state.exists || observation.observedAt >= state.identityAt
	visibleChanged := !state.exists || (currentObservation && (state.tombstone ||
		(state.handle == "" && observation.handle != "") ||
		(observation.displayName != "" && observation.displayName != strings.TrimSpace(state.displayName)) ||
		(observation.avatarURL != "" && observation.avatarURL != strings.TrimSpace(state.avatarURL))))
	if !state.hasJob {
		return currentObservation, true
	}

	pending := state.requested > state.completed
	stale := state.fetchedAt == 0 || observation.observedAt-state.fetchedAt >= profileObservationRefreshInterval.Milliseconds()
	return currentObservation, !pending && (visibleChanged || stale)
}

func readProfileObservationStateTx(tx *sql.Tx, channelID string) (profileObservationState, error) {
	var state profileObservationState
	var tombstone int
	err := tx.QueryRow(`
		SELECT COALESCE(cp.handle, ''), COALESCE(cp.display_name, ''),
		       COALESCE(mo.source_url, ''),
		       MAX(COALESCE(cp.observed_at_ms, 0), COALESCE(cp.fetched_at, 0)),
		       COALESCE(cp.fetched_at, 0), COALESCE(cp.tombstone, 0),
		       COALESCE(pj.requested_revision, 0), COALESCE(pj.completed_revision, 0),
		       CASE WHEN pj.channel_id IS NULL THEN 0 ELSE 1 END
		FROM channel_profiles cp
		LEFT JOIN profile_jobs pj ON pj.channel_id = cp.channel_id
		LEFT JOIN assets a
		  ON a.asset_kind = 'avatar'
		 AND a.owner_kind = 'channel'
		 AND a.owner_id = cp.channel_id
		 AND a.media_index = 0
		LEFT JOIN media_objects mo ON mo.object_id = a.desired_object_id
		WHERE cp.channel_id = ?
	`, channelID).Scan(
		&state.handle, &state.displayName, &state.avatarURL,
		&state.identityAt, &state.fetchedAt, &tombstone,
		&state.requested, &state.completed, &state.hasJob,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.exists = true
	state.tombstone = tombstone != 0
	return state, nil
}

// GetProfileJob returns the durable request row for one channel.
func (db *DB) GetProfileJob(channelID string) (*model.ProfileJob, error) {
	job, err := scanProfileJob(db.conn.QueryRow(`
		SELECT channel_id, requested_revision, completed_revision,
		       requested_at_ms, lease_owner, lease_until_ms, attempts,
		       next_attempt_at_ms, last_error, updated_at_ms
		FROM profile_jobs
		WHERE channel_id = ?
	`, strings.TrimSpace(channelID)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// RequestProfileJob advances the durable request revision for an existing
// fetchable identity. Ingest owners use this instead of clearing profile
// freshness fields or creating an in-memory request.
func (db *DB) RequestProfileJob(channelID string, nowMs int64) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			INSERT INTO profile_jobs (
				channel_id, requested_revision, completed_revision,
				requested_at_ms, updated_at_ms
			)
			SELECT channel_id, 1, 0, ?, ?
			FROM channel_profiles
			WHERE channel_id = ?
			ON CONFLICT(channel_id) DO UPDATE SET
				requested_revision = profile_jobs.requested_revision + 1,
				requested_at_ms = excluded.requested_at_ms,
				next_attempt_at_ms = 0,
				last_error = '',
				updated_at_ms = excluded.updated_at_ms
		`, nowMs, nowMs, channelID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("RequestProfileJob: unknown channel_id %s", channelID)
		}
		return nil
	})
}

// ClaimProfileJobs leases pending revisions. A revision remains pending when
// new ingest arrives while an older revision is being fetched.
func (db *DB) ClaimProfileJobs(opts LeaseOptions) ([]model.ProfileJob, error) {
	if opts.Owner == "" {
		opts.Owner = "unknown"
	}
	if opts.NowMs <= 0 {
		opts.NowMs = time.Now().UnixMilli()
	}
	if opts.LeaseMs <= 0 {
		opts.LeaseMs = defaultProfileJobLease.Milliseconds()
	}
	if opts.Limit <= 0 {
		opts.Limit = 1
	}

	var claimed []model.ProfileJob
	err := db.WithWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT channel_id
			FROM profile_jobs
			WHERE requested_revision > completed_revision
			  AND next_attempt_at_ms <= ?
			  AND (lease_owner = '' OR lease_until_ms <= ?)
			ORDER BY requested_at_ms DESC, channel_id
			LIMIT ?
		`, opts.NowMs, opts.NowMs, opts.Limit)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, id := range ids {
			res, err := tx.Exec(`
				UPDATE profile_jobs
				SET lease_owner = ?, lease_until_ms = ?, updated_at_ms = ?
				WHERE channel_id = ?
				  AND requested_revision > completed_revision
				  AND next_attempt_at_ms <= ?
				  AND (lease_owner = '' OR lease_until_ms <= ?)
			`, opts.Owner, opts.NowMs+opts.LeaseMs, opts.NowMs, id, opts.NowMs, opts.NowMs)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			job, err := scanProfileJob(tx.QueryRow(`
				SELECT channel_id, requested_revision, completed_revision,
				       requested_at_ms, lease_owner, lease_until_ms, attempts,
				       next_attempt_at_ms, last_error, updated_at_ms
				FROM profile_jobs WHERE channel_id = ?
			`, id))
			if err != nil {
				return err
			}
			claimed = append(claimed, job)
		}
		return nil
	})
	return claimed, err
}

// ReleaseProfileJob makes a canceled claim immediately available without
// recording a fetch failure. A newer request revision also discards retry
// state inherited from the superseded work.
func (db *DB) ReleaseProfileJob(job model.ProfileJob, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE profile_jobs
			SET attempts = CASE
					WHEN requested_revision = ? THEN attempts
					ELSE 0
				END,
				next_attempt_at_ms = 0,
				last_error = CASE
					WHEN requested_revision = ? THEN last_error
					ELSE ''
				END,
				lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE channel_id = ? AND lease_owner = ? AND lease_until_ms = ?
		`, job.RequestedRevision, job.RequestedRevision, nowMs,
			job.ChannelID, job.LeaseOwner, profileJobLeaseUntilMs(job))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "profile_jobs", job.ChannelID, job.LeaseOwner)
	})
}

// CompleteProfileJob publishes fetched metadata and every ready replacement in
// one transaction. The revision completes only when every declared identity
// asset is ready. A failed optional sibling therefore stays on the same durable
// job without delaying metadata or another successful asset. Empty
// authoritative sources remove the old asset row. Superseded results publish
// nothing.
func (db *DB) CompleteProfileJob(job model.ProfileJob, profile model.ChannelProfile, replacements []Asset, nowMs int64) (stored, complete bool, err error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	if strings.TrimSpace(job.ChannelID) == "" || strings.TrimSpace(job.LeaseOwner) == "" {
		return false, false, fmt.Errorf("CompleteProfileJob: missing channel or lease owner")
	}
	if strings.TrimSpace(profile.ChannelID) == "" {
		profile.ChannelID = job.ChannelID
	}
	if profile.ChannelID != job.ChannelID {
		return false, false, fmt.Errorf("CompleteProfileJob: profile channel %s does not match job %s", profile.ChannelID, job.ChannelID)
	}
	if profile.Platform == "" {
		return false, false, fmt.Errorf("CompleteProfileJob: empty platform for %s", job.ChannelID)
	}
	if profile.FetchedAt == nil || profile.FetchedAt.IsZero() {
		fetchedAt := time.UnixMilli(nowMs)
		profile.FetchedAt = &fetchedAt
	}
	prepared, err := db.prepareProfileJobReplacements(profile, replacements, nowMs)
	if err != nil {
		return false, false, err
	}

	complete = true
	err = db.WithWrite(func(tx *sql.Tx) error {
		var requested, leaseUntilMs int64
		var owner string
		err := tx.QueryRow(`
			SELECT requested_revision, lease_owner, lease_until_ms
			FROM profile_jobs
			WHERE channel_id = ?
		`, job.ChannelID).Scan(&requested, &owner, &leaseUntilMs)
		if err != nil {
			return err
		}
		if owner != job.LeaseOwner || leaseUntilMs != profileJobLeaseUntilMs(job) {
			return fmt.Errorf("%w: profile_jobs %s owner %q", ErrQueueLeaseNotHeld, job.ChannelID, job.LeaseOwner)
		}
		if requested != job.RequestedRevision {
			_, err := tx.Exec(`
				UPDATE profile_jobs
				SET lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
				WHERE channel_id = ? AND lease_owner = ? AND lease_until_ms = ?
			`, nowMs, job.ChannelID, job.LeaseOwner, leaseUntilMs)
			return err
		}

		for _, kind := range []string{"avatar", "banner"} {
			sourceURL := profileAssetSource(profile, kind)
			if sourceURL == "" {
				continue
			}
			if _, ok := prepared[kind]; ok {
				continue
			}
			reusable, err := reusableProfileAssetTx(tx, profile.ChannelID, kind, sourceURL)
			if err != nil {
				return err
			}
			if !reusable {
				complete = false
			}
		}

		if err := upsertChannelProfileTx(tx, profile); err != nil {
			return err
		}
		for _, kind := range []string{"avatar", "banner"} {
			sourceURL := profileAssetSource(profile, kind)
			if sourceURL == "" {
				if _, err := tx.Exec(`
					DELETE FROM assets
					WHERE asset_kind = ? AND owner_kind = 'channel'
					  AND owner_id = ? AND media_index = 0
				`, kind, profile.ChannelID); err != nil {
					return err
				}
				continue
			}
			if replacement, ok := prepared[kind]; ok {
				if err := upsertAssetTx(tx, replacement); err != nil {
					return err
				}
			}
		}
		stored = true
		if !complete {
			return nil
		}
		res, err := tx.Exec(`
			UPDATE profile_jobs
			SET completed_revision = ?,
				lease_owner = '', lease_until_ms = 0,
				attempts = 0, next_attempt_at_ms = 0, last_error = '',
				updated_at_ms = ?
			WHERE channel_id = ?
			  AND requested_revision = ?
			  AND lease_owner = ?
			  AND lease_until_ms = ?
		`, job.RequestedRevision, nowMs, job.ChannelID, job.RequestedRevision,
			job.LeaseOwner, leaseUntilMs)
		if err != nil {
			return err
		}
		if err := requireQueueLeaseUpdate(res, "profile_jobs", job.ChannelID, job.LeaseOwner); err != nil {
			return err
		}
		return nil
	})
	if err != nil || !stored {
		complete = false
		stored = false
	}
	return stored, complete, err
}

func (db *DB) prepareProfileJobReplacements(profile model.ChannelProfile, replacements []Asset, nowMs int64) (map[string]Asset, error) {
	if len(replacements) > 2 {
		return nil, fmt.Errorf("CompleteProfileJob: too many replacement assets")
	}
	prepared := make(map[string]Asset, len(replacements))
	for _, replacement := range replacements {
		kind := strings.TrimSpace(replacement.AssetKind)
		if kind != "avatar" && kind != "banner" {
			return nil, fmt.Errorf("CompleteProfileJob: unsupported profile asset kind %q", kind)
		}
		if _, exists := prepared[kind]; exists {
			return nil, fmt.Errorf("CompleteProfileJob: duplicate %s replacement", kind)
		}
		sourceURL := profileAssetSource(profile, kind)
		if sourceURL == "" {
			return nil, fmt.Errorf("CompleteProfileJob: %s replacement has no fetched source", kind)
		}
		if strings.TrimSpace(replacement.FilePath) == "" {
			return nil, fmt.Errorf("CompleteProfileJob: %s replacement has no file path", kind)
		}
		replacement.AssetID = BuildAssetID(profile.Platform, "channel", profile.ChannelID, kind, 0)
		replacement.AssetKind = kind
		replacement.OwnerKind = "channel"
		replacement.OwnerID = profile.ChannelID
		replacement.MediaIndex = 0
		replacement.SourceURL = sourceURL
		replacement.State = AssetStateReady
		replacement.RequiredReason = "identity"
		replacement.LastErrorKind = ""
		replacement.LastError = ""
		replacement.Attempts = 0
		replacement.NextAttemptAtMs = 0
		replacement.LeaseOwner = ""
		replacement.LeaseUntilMs = 0
		replacement.UpdatedAtMs = nowMs
		var err error
		replacement, err = db.prepareReadyAssetMetadata(replacement, nowMs)
		if err != nil {
			return nil, fmt.Errorf("CompleteProfileJob: prepare %s: %w", kind, err)
		}
		prepared[kind] = normalizeAsset(replacement, nowMs)
	}
	return prepared, nil
}

func profileAssetSource(profile model.ChannelProfile, kind string) string {
	if kind == "banner" {
		return strings.TrimSpace(profile.BannerURL)
	}
	return strings.TrimSpace(profile.AvatarURL)
}

func reusableProfileAssetTx(tx *sql.Tx, channelID, kind, sourceURL string) (bool, error) {
	var storedSource, filePath, contentType, state string
	var sizeBytes, fileMtimeNs int64
	err := tx.QueryRow(`
		SELECT COALESCE(current.published_source_url, ''), COALESCE(current.file_path, ''), COALESCE(current.content_type, ''),
		       COALESCE(current.size_bytes, 0), COALESCE(current.file_mtime_ns, 0),
		       CASE WHEN current.published_revision > 0 AND current.file_path != '' THEN 'ready' ELSE desired.job_state END
		FROM assets a
		JOIN media_objects current ON current.object_id = a.object_id
		JOIN media_objects desired ON desired.object_id = a.desired_object_id
		WHERE a.asset_kind = ? AND a.owner_kind = 'channel'
		  AND a.owner_id = ? AND a.media_index = 0
	`, kind, channelID).Scan(&storedSource, &filePath, &contentType, &sizeBytes, &fileMtimeNs, &state)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ReadyAssetMatchesSource(&Asset{
		SourceURL: storedSource, FilePath: filePath, ContentType: contentType, State: state,
		SizeBytes: sizeBytes, FileMtimeNs: fileMtimeNs,
	}, sourceURL), nil
}

// RetryProfileJob releases a failed claim with bounded durable retry state.
// A newer revision supersedes the old failure and becomes immediately due.
func (db *DB) RetryProfileJob(job model.ProfileJob, message string, delay time.Duration, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	if delay < 0 {
		delay = 0
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE profile_jobs
			SET attempts = CASE
					WHEN requested_revision = ? THEN attempts + 1
					ELSE 0
				END,
				next_attempt_at_ms = CASE
					WHEN requested_revision = ? THEN ?
					ELSE 0
				END,
				last_error = CASE
					WHEN requested_revision = ? THEN ?
					ELSE ''
				END,
				lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE channel_id = ? AND lease_owner = ? AND lease_until_ms = ?
		`, job.RequestedRevision,
			job.RequestedRevision, nowMs+delay.Milliseconds(),
			job.RequestedRevision, strings.TrimSpace(message),
			nowMs, job.ChannelID, job.LeaseOwner, profileJobLeaseUntilMs(job))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "profile_jobs", job.ChannelID, job.LeaseOwner)
	})
}

func (db *DB) FailProfileJob(job model.ProfileJob, message string, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE profile_jobs
			SET completed_revision = CASE WHEN requested_revision = ? THEN requested_revision ELSE completed_revision END,
			    attempts = CASE WHEN requested_revision = ? THEN attempts + 1 ELSE 0 END,
			    next_attempt_at_ms = 0, last_error = ?,
			    lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE channel_id = ? AND lease_owner = ? AND lease_until_ms = ?
		`, job.RequestedRevision, job.RequestedRevision, strings.TrimSpace(message), nowMs,
			job.ChannelID, job.LeaseOwner, profileJobLeaseUntilMs(job))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "profile_jobs", job.ChannelID, job.LeaseOwner)
	})
}

func (db *DB) NextProfileJobDelay(nowMs int64) (time.Duration, error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	var due sql.NullInt64
	err := db.reader().QueryRow(`
		SELECT MIN(CASE
			WHEN lease_owner != '' THEN lease_until_ms
			WHEN next_attempt_at_ms > 0 THEN next_attempt_at_ms
			ELSE ? END)
		FROM profile_jobs
		WHERE completed_revision < requested_revision
	`, nowMs).Scan(&due)
	if err != nil {
		return 0, err
	}
	if !due.Valid {
		return 5 * time.Minute, nil
	}
	delay := time.Duration(due.Int64-nowMs) * time.Millisecond
	if delay < 0 {
		return 0, nil
	}
	return delay, nil
}

func profileJobLeaseUntilMs(job model.ProfileJob) int64 {
	if job.LeaseUntil == nil {
		return 0
	}
	return job.LeaseUntil.UnixMilli()
}

type profileJobScanner interface {
	Scan(dest ...any) error
}

func scanProfileJob(row profileJobScanner) (model.ProfileJob, error) {
	var job model.ProfileJob
	var requestedAt, leaseUntil, nextAttemptAt, updatedAt int64
	err := row.Scan(
		&job.ChannelID, &job.RequestedRevision, &job.CompletedRevision,
		&requestedAt, &job.LeaseOwner, &leaseUntil, &job.Attempts,
		&nextAttemptAt, &job.LastError, &updatedAt,
	)
	if err != nil {
		return job, err
	}
	job.RequestedAt = time.UnixMilli(requestedAt)
	job.LeaseUntil = millisToTimePtr(sql.NullInt64{Int64: leaseUntil, Valid: leaseUntil > 0})
	job.NextAttemptAt = millisToTimePtr(sql.NullInt64{Int64: nextAttemptAt, Valid: nextAttemptAt > 0})
	job.UpdatedAt = time.UnixMilli(updatedAt)
	return job, nil
}
