package db

import "fmt"

func feedUnseenPredicate(alias string) string {
	return fmt.Sprintf(`NOT EXISTS (
			SELECT 1
			FROM feed_seen fs
			WHERE fs.tweet_id = %[1]s.tweet_id
		)
		AND (
			NULLIF(TRIM(COALESCE(%[1]s.content_hash, '')), '') IS NULL
			OR NOT EXISTS (
				SELECT 1
				FROM feed_items seen_fi INDEXED BY idx_feed_items_content_hash
				JOIN feed_seen fs ON fs.tweet_id = seen_fi.tweet_id
				WHERE seen_fi.content_hash = %[1]s.content_hash
				  AND seen_fi.content_hash IS NOT NULL
				  AND seen_fi.content_hash != ''
			)
		)`, alias)
}

func feedUnseenPredicateArgs() []any {
	return nil
}
