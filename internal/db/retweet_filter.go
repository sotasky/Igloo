package db

import "fmt"

// retweetFilterClause returns a SQL WHERE fragment (no leading WHERE/AND)
// implementing the include-reposts filter with self-pass and
// dedup-aware semantics described in the design spec.
//
// Branches:
//
//  1. Pure RT branch — hides a row when it's a non-self retweet whose
//     source channel has include_reposts=0 AND every retweeter for
//     its content_hash is muted. Joins channel_settings by the canonical
//     retweeter channel id.
//     Treats a missing settings row or NULL setting as "include reposts"
//     via COALESCE.
//
//  2. Quote-tweet branch — hides a row when its author has
//     include_reposts=0 AND the quote points at someone else.
//
// `alias` is the SQL alias used for feed_items in the surrounding query.
func retweetFilterClause(alias string) string {
	return fmt.Sprintf(`NOT (
		COALESCE(%[1]s.is_retweet,0) = 1
		AND COALESCE(%[1]s.source_channel_id,'') != ''
		AND %[1]s.source_channel_id != COALESCE(%[1]s.channel_id,'')
		AND EXISTS (
			SELECT 1 FROM channel_settings cs
			WHERE cs.channel_id = %[1]s.source_channel_id
			  AND cs.include_reposts = 0
		)
		AND NOT EXISTS (
			SELECT 1 FROM retweet_sources rs
			LEFT JOIN channel_settings cs2 ON cs2.channel_id = rs.retweeter_channel_id
			WHERE rs.content_hash = COALESCE(%[1]s.content_hash,'')
			  AND COALESCE(cs2.include_reposts, 1) != 0
		)
	)
	AND NOT (
		COALESCE(%[1]s.quote_tweet_id,'') != ''
		AND COALESCE(%[1]s.channel_id,'') != ''
		AND %[1]s.channel_id != COALESCE(%[1]s.quote_channel_id,'')
		AND EXISTS (
			SELECT 1 FROM channel_settings cs
			WHERE cs.channel_id = %[1]s.channel_id
			  AND cs.include_reposts = 0
		)
	)`, alias)
}
