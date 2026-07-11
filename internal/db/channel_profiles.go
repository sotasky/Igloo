package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// UpsertChannelProfile stores profile metadata only. Normal fetched identity
// completion, including avatar/banner publication, is owned by profile_jobs.
func (db *DB) UpsertChannelProfile(profile model.ChannelProfile) error {
	if strings.TrimSpace(profile.ChannelID) == "" {
		return fmt.Errorf("UpsertChannelProfile: empty channel_id")
	}
	if strings.TrimSpace(profile.Platform) == "" {
		return fmt.Errorf("UpsertChannelProfile: empty platform for %s", profile.ChannelID)
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		return upsertChannelProfileTx(tx, profile)
	})
}

func upsertChannelProfileTx(tx *sql.Tx, profile model.ChannelProfile) error {
	channelID := strings.TrimSpace(profile.ChannelID)
	platform := strings.TrimSpace(profile.Platform)
	if channelID == "" || platform == "" {
		return fmt.Errorf("upsert channel profile: missing identity")
	}
	_, err := tx.Exec(`
		INSERT INTO channel_profiles (
			channel_id, platform, handle, display_name, bio, website,
			followers, following, verified, verified_type, protected,
			observed_at_ms, fetched_at, tombstone
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			platform = excluded.platform,
			handle = COALESCE(excluded.handle, channel_profiles.handle),
			display_name = COALESCE(excluded.display_name, channel_profiles.display_name),
			bio = excluded.bio,
			website = excluded.website,
			followers = excluded.followers,
			following = excluded.following,
			verified = excluded.verified,
			verified_type = excluded.verified_type,
			protected = excluded.protected,
			observed_at_ms = MAX(channel_profiles.observed_at_ms, excluded.observed_at_ms),
			fetched_at = MAX(channel_profiles.fetched_at, excluded.fetched_at),
			tombstone = excluded.tombstone
	`,
		channelID, platform, nilIfEmpty(profile.Handle), nilIfEmpty(profile.DisplayName),
		profile.Bio, nilIfEmpty(profile.Website), profile.Followers, profile.Following,
		boolToInt(profile.Verified), nilIfEmpty(profile.VerifiedType), boolToInt(profile.Protected),
		nilIfTimeZero(profile.ObservedAt), nilIfTimeZero(profile.FetchedAt), boolToInt(profile.Tombstone),
	)
	return err
}

func (db *DB) GetChannelProfile(channelID string) (*model.ChannelProfile, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil
	}
	return scanChannelProfile(db.conn.QueryRow(`
		SELECT cp.channel_id, cp.platform, COALESCE(cp.handle, ''),
		       COALESCE(cp.display_name, ''), COALESCE(cp.bio, ''), COALESCE(cp.website, ''),
		       cp.followers, cp.following, cp.verified, COALESCE(cp.verified_type, ''), cp.protected,
		       COALESCE(avatar.source_url, ''), COALESCE(banner.source_url, ''),
		       cp.observed_at_ms, cp.fetched_at, cp.tombstone
		FROM channel_profiles cp
		LEFT JOIN assets avatar
		  ON avatar.asset_kind = 'avatar' AND avatar.owner_kind = 'channel'
		 AND avatar.owner_id = cp.channel_id AND avatar.media_index = 0
		LEFT JOIN assets banner
		  ON banner.asset_kind = 'banner' AND banner.owner_kind = 'channel'
		 AND banner.owner_id = cp.channel_id AND banner.media_index = 0
		WHERE cp.channel_id = ?
	`, channelID))
}

func (db *DB) GetYouTubeChannelProfileByHandle(handle string) (*model.ChannelProfile, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	return scanChannelProfile(db.conn.QueryRow(`
		SELECT cp.channel_id, cp.platform, COALESCE(cp.handle, ''),
		       COALESCE(cp.display_name, ''), COALESCE(cp.bio, ''), COALESCE(cp.website, ''),
		       cp.followers, cp.following, cp.verified, COALESCE(cp.verified_type, ''), cp.protected,
		       COALESCE(avatar.source_url, ''), COALESCE(banner.source_url, ''),
		       cp.observed_at_ms, cp.fetched_at, cp.tombstone
		FROM channel_profiles cp
		LEFT JOIN assets avatar
		  ON avatar.asset_kind = 'avatar' AND avatar.owner_kind = 'channel'
		 AND avatar.owner_id = cp.channel_id AND avatar.media_index = 0
		LEFT JOIN assets banner
		  ON banner.asset_kind = 'banner' AND banner.owner_kind = 'channel'
		 AND banner.owner_id = cp.channel_id AND banner.media_index = 0
		WHERE LOWER(cp.platform) = 'youtube'
		  AND cp.tombstone = 0
		  AND cp.channel_id LIKE 'youtube_UC%'
		  AND LOWER(LTRIM(COALESCE(cp.handle, ''), '@')) = ?
		ORDER BY cp.fetched_at DESC
		LIMIT 1
	`, handle))
}

type channelProfileScanner interface {
	Scan(dest ...any) error
}

func scanChannelProfile(row channelProfileScanner) (*model.ChannelProfile, error) {
	var profile model.ChannelProfile
	var verified, protected, tombstone int
	var observedAt, fetchedAt sql.NullInt64
	err := row.Scan(
		&profile.ChannelID, &profile.Platform, &profile.Handle,
		&profile.DisplayName, &profile.Bio, &profile.Website,
		&profile.Followers, &profile.Following, &verified, &profile.VerifiedType, &protected,
		&profile.AvatarURL, &profile.BannerURL,
		&observedAt, &fetchedAt, &tombstone,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	profile.Verified = verified != 0
	profile.Protected = protected != 0
	profile.Tombstone = tombstone != 0
	profile.ObservedAt = millisToTimePtr(observedAt)
	profile.FetchedAt = millisToTimePtr(fetchedAt)
	return &profile, nil
}

func (db *DB) GetTwitterChannelProfilesByHandles(handles []string) (map[string]model.ChannelProfile, error) {
	keys := make([]string, 0, len(handles))
	seen := make(map[string]struct{}, len(handles))
	for _, raw := range handles {
		key := model.NormalizeTwitterHandle(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return map[string]model.ChannelProfile{}, nil
	}
	args := stringsToAny(keys)
	rows, err := db.conn.Query(`
		SELECT channel_id, COALESCE(handle, ''), COALESCE(display_name, '')
		FROM channel_profiles
		WHERE platform = 'twitter' AND tombstone = 0
		  AND LOWER(COALESCE(handle, '')) IN (`+placeholders(len(keys))+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]model.ChannelProfile, len(keys))
	for rows.Next() {
		var profile model.ChannelProfile
		if err := rows.Scan(&profile.ChannelID, &profile.Handle, &profile.DisplayName); err != nil {
			return nil, err
		}
		profile.Platform = "twitter"
		key := model.NormalizeTwitterHandle(profile.Handle)
		if key == "" && strings.HasPrefix(profile.ChannelID, "twitter_") {
			key = strings.TrimPrefix(strings.ToLower(profile.ChannelID), "twitter_")
			profile.Handle = key
		}
		if key != "" {
			out[key] = profile
		}
	}
	return out, rows.Err()
}

func nilIfTimeZero(value *time.Time) int64 {
	if value == nil || value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}
