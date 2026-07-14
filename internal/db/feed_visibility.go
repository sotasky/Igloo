package db

import "fmt"

func feedPrimaryItemPredicate(alias string) string {
	return fmt.Sprintf("COALESCE(%s.is_ghost, 0) = 0", alias)
}

func feedActiveOwnerPredicate(alias string) string {
	return fmt.Sprintf(`(
		(
			COALESCE(%[1]s.is_retweet, 0) = 0
			AND EXISTS (
				SELECT 1
				FROM channel_follows cf
				WHERE cf.channel_id = %[1]s.source_channel_id
				   OR cf.channel_id = %[1]s.channel_id
			)
		)
		OR (
			COALESCE(%[1]s.is_retweet, 0) = 1
			AND EXISTS (
				SELECT 1
				FROM channel_follows cf
				WHERE cf.channel_id = %[1]s.reposter_channel_id
				   OR cf.channel_id = %[1]s.source_channel_id
			)
		)
		OR (
			COALESCE(%[1]s.is_retweet, 0) = 1
			AND COALESCE(%[1]s.content_hash, '') != ''
			AND EXISTS (
				SELECT 1
				FROM retweet_sources rs
				JOIN channel_follows cf ON cf.channel_id = rs.retweeter_channel_id
				WHERE rs.content_hash = %[1]s.content_hash
				  AND rs.tweet_id = %[1]s.tweet_id
			)
		)
		OR EXISTS (
			SELECT 1
			FROM feed_item_sources fis
			JOIN feed_sources fs ON fs.source_id = fis.source_id
			WHERE fis.tweet_id = %[1]s.tweet_id
			  AND fs.enabled = 1
		)
	)`, alias)
}
