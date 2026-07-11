package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/model"
)

type AndroidSyncFeedRow struct {
	Item model.FeedItem
}

type AndroidSyncFeedProjection struct {
	Rows           map[string]AndroidSyncFeedRow
	RetweetSources map[string][]RetweetSourceRow
}

func (db *DB) ListAndroidSyncFeedProjection(tweetIDs []string) (AndroidSyncFeedProjection, error) {
	out := AndroidSyncFeedProjection{
		Rows:           make(map[string]AndroidSyncFeedRow, len(tweetIDs)),
		RetweetSources: make(map[string][]RetweetSourceRow),
	}
	if len(tweetIDs) == 0 {
		return out, nil
	}
	settingValues, err := db.GetAllSettings()
	if err != nil {
		return out, err
	}
	targetLang := strings.ToLower(strings.TrimSpace(settingValues["translate_target_lang"]))
	if targetLang == "" {
		targetLang = "en"
	}
	translationSkip := make(map[string]bool)
	for _, value := range strings.Split(settingValues["translate_skip_langs"], ",") {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			translationSkip[value] = true
		}
	}
	keepTranslation := func(sourceText, translatedText, sourceLang string) bool {
		return strings.TrimSpace(translatedText) != "" &&
			!language.InSet(sourceLang, translationSkip) &&
			strings.Join(strings.Fields(sourceText), " ") != strings.Join(strings.Fields(translatedText), " ")
	}

	contentHashes := make(map[string]struct{})
	tweetIDs = uniqueStrings(tweetIDs)
	sort.Strings(tweetIDs)
	for _, chunk := range stringChunks(tweetIDs, androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT fi.tweet_id,
			       COALESCE(fi.body_text, ''), COALESCE(fi.lang, ''),
			       COALESCE(fi.is_retweet, 0), COALESCE(fi.quote_tweet_id, ''),
			       COALESCE(fi.quote_body_text, ''), COALESCE(fi.quote_lang, ''),
			       COALESCE(fi.quote_media_json, ''), COALESCE(fi.media_json, ''),
			       COALESCE(fi.canonical_url, ''), COALESCE(fi.reply_to_status, ''),
			       COALESCE(fi.is_reply, 0),
			       COALESCE(fi.is_ghost, 0), COALESCE(fi.quote_published_at, 0),
			       COALESCE(fi.views, 0), COALESCE(fi.likes, 0), COALESCE(fi.retweets, 0),
			       COALESCE(fi.published_at, 0), COALESCE(fi.content_hash, ''),
			       COALESCE(fi.canonical_tweet_id, ''), COALESCE(fi.channel_id, ''),
			       COALESCE(fi.quote_channel_id, ''), COALESCE(fi.source_channel_id, ''),
			       COALESCE(fi.reply_channel_id, ''), COALESCE(fi.reposter_channel_id, ''),
			       COALESCE(body_translation.translated_text, ''),
			       COALESCE(body_translation.source_lang, ''),
			       COALESCE(quote_translation.translated_text, ''),
			       COALESCE(quote_translation.source_lang, '')
			FROM feed_items fi
			LEFT JOIN translations body_translation
			  ON body_translation.tweet_id = fi.tweet_id AND body_translation.field = 'body' AND body_translation.target_lang = ?
			LEFT JOIN translations quote_translation
			  ON quote_translation.tweet_id = fi.tweet_id AND quote_translation.field = 'quote' AND quote_translation.target_lang = ?
			WHERE fi.tweet_id IN (`+placeholders(len(chunk))+`)
		`, append([]any{targetLang, targetLang}, stringsToAny(chunk)...)...)
		if err != nil {
			return out, fmt.Errorf("android sync feed projection: %w", err)
		}
		for rows.Next() {
			projection, err := scanAndroidSyncFeedRow(rows)
			if err != nil {
				_ = rows.Close()
				return out, err
			}
			if !keepTranslation(projection.Item.BodyText, projection.Item.BodyTranslation, projection.Item.BodySourceLang) {
				projection.Item.BodyTranslation = ""
				projection.Item.BodySourceLang = ""
			}
			if !keepTranslation(projection.Item.QuoteBodyText, projection.Item.QuoteTranslation, projection.Item.QuoteSourceLang) {
				projection.Item.QuoteTranslation = ""
				projection.Item.QuoteSourceLang = ""
			}
			if projection.Item.ContentHash != "" {
				contentHashes[projection.Item.ContentHash] = struct{}{}
			}
			out.Rows[projection.Item.TweetID] = projection
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return out, err
		}
		if err := rows.Close(); err != nil {
			return out, err
		}
	}

	for _, chunk := range stringChunks(sortedKeys(contentHashes), androidSyncProjectionChunkSize) {
		rows, err := db.GetRetweetSourceRows(chunk)
		if err != nil {
			return out, err
		}
		for hash, sourceRows := range rows {
			out.RetweetSources[hash] = append(out.RetweetSources[hash], sourceRows...)
		}
	}
	return out, nil
}

func scanAndroidSyncFeedRow(row *sql.Rows) (AndroidSyncFeedRow, error) {
	var out AndroidSyncFeedRow
	var quotePublishedAt, publishedAt int64
	err := row.Scan(
		&out.Item.TweetID,
		&out.Item.BodyText, &out.Item.Lang,
		&out.Item.IsRetweet, &out.Item.QuoteTweetID,
		&out.Item.QuoteBodyText, &out.Item.QuoteLang, &out.Item.QuoteMediaJSON, &out.Item.MediaJSON,
		&out.Item.CanonicalURL, &out.Item.ReplyToStatus,
		&out.Item.IsReply, &out.Item.IsGhost, &quotePublishedAt,
		&out.Item.Views, &out.Item.Likes, &out.Item.Retweets, &publishedAt,
		&out.Item.ContentHash, &out.Item.CanonicalTweetID, &out.Item.ChannelID,
		&out.Item.QuoteChannelID, &out.Item.SourceChannelID, &out.Item.ReplyChannelID,
		&out.Item.ReposterChannelID, &out.Item.BodyTranslation, &out.Item.BodySourceLang,
		&out.Item.QuoteTranslation, &out.Item.QuoteSourceLang,
	)
	if err != nil {
		return out, err
	}
	out.Item.QuotePublishedAt = millisToTimePtr(sql.NullInt64{Int64: quotePublishedAt, Valid: quotePublishedAt > 0})
	out.Item.PublishedAt = millisToTimePtr(sql.NullInt64{Int64: publishedAt, Valid: publishedAt > 0})
	return out, nil
}
