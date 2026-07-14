package db

import (
	"fmt"
	"strings"
)

// FeedSnapshotHealth summarizes whether the durable ranked snapshot has caught
// up to feed rows that are eligible for the user-visible feed.
type FeedSnapshotHealth struct {
	SnapshotAtMs                 int64
	CandidateCount               int
	LatestCandidateFetchedAtMs   int64
	LatestCandidatePublishedAtMs int64
	FreshItemsSinceSnapshot      int
}

func (db *DB) GetFeedSnapshotHealth() (FeedSnapshotHealth, error) {
	var out FeedSnapshotHealth
	snapshotAt, err := db.SnapshotComputedAt()
	if err != nil {
		return out, err
	}
	out.SnapshotAtMs = snapshotAt

	where := []string{
		feedPrimaryItemPredicate("fi"),
		feedActiveOwnerPredicate("fi"),
		"fi.published_at > 0",
		"(fi.canonical_tweet_id IS NULL OR fi.canonical_tweet_id = '' OR fi.canonical_tweet_id = fi.tweet_id)",
		retweetFilterClause("fi"),
	}
	args := []any{snapshotAt}
	muted, _ := db.GetMutedChannelIDs()
	if len(muted) > 0 {
		ph := strings.Repeat("?,", len(muted))
		ph = ph[:len(ph)-1]
		where = append(where, "fi.channel_id NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
		where = append(where, "COALESCE(fi.source_channel_id,'') NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
	}
	where = append(where, feedUnseenPredicate("fi"))

	query := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(MAX(fi.fetched_at), 0),
		       COALESCE(MAX(fi.published_at), 0),
		       COALESCE(SUM(CASE WHEN fi.fetched_at > ? THEN 1 ELSE 0 END), 0)
		FROM feed_items fi
		WHERE %s
	`, strings.Join(where, " AND "))
	if err := db.conn.QueryRow(query, args...).Scan(
		&out.CandidateCount,
		&out.LatestCandidateFetchedAtMs,
		&out.LatestCandidatePublishedAtMs,
		&out.FreshItemsSinceSnapshot,
	); err != nil {
		return out, err
	}
	return out, nil
}
