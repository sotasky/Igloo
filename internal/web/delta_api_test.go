package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

// #6 — basic delta roundtrip + cursor advance + end_of_stream.
func TestFeedDeltaEmpty(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/feed/delta", nil)
	req = attachTestAuth(req, "alice")
	srv.handleFeedDelta(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d — %s", rec.Code, rec.Body.String())
	}
	body := decodeDelta(t, rec.Body.Bytes())
	if len(body.Bundles) != 0 {
		t.Errorf("expected 0 bundles, got %d", len(body.Bundles))
	}
	if !body.EndOfStream {
		t.Errorf("expected end_of_stream=true on empty result")
	}
}

func TestFeedDeltaEmitsBundlesAndAdvancesCursor(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItem(t, srv, "tw_1", "alice", time.Now().UnixMilli(), 100)
	insertFeedItem(t, srv, "tw_2", "alice", time.Now().UnixMilli(), 101)

	body := callFeedDelta(t, srv, "alice", "")
	if len(body.Bundles) != 2 {
		t.Fatalf("expected 2 bundles, got %d", len(body.Bundles))
	}
	if body.Bundles[0].PrimaryKind != "feed_items" {
		t.Errorf("primary_kind = %q, want feed_items", body.Bundles[0].PrimaryKind)
	}
	if body.NextMarker != "101" {
		t.Errorf("next_marker = %q, want 101 (max sync_seq of batch)", body.NextMarker)
	}
	if !body.EndOfStream {
		t.Errorf("end_of_stream should be true when batch < cap")
	}

	// Cursor advance: same `since` should NOT include the rows.
	follow := callFeedDelta(t, srv, "alice", body.NextMarker)
	if len(follow.Bundles) != 0 {
		t.Errorf("follow-up call expected 0 bundles, got %d", len(follow.Bundles))
	}
}

func TestFeedDeltaCarriesSeenAndMutedStateAttachment(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItem(t, srv, "tw_visible", "good_handle", time.Now().UnixMilli(), 200)
	insertFeedItem(t, srv, "tw_seen", "good_handle", time.Now().UnixMilli(), 201)
	insertFeedItem(t, srv, "tw_muted", "spammer", time.Now().UnixMilli(), 202)

	// Mark tw_seen seen by alice.
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
		"alice", "tw_seen", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	// Mute spammer for everyone (single-user model).
	if err := srv.db.ExecRaw(
		`INSERT INTO muted_accounts (handle, muted_at) VALUES (?, ?)`,
		"spammer", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	body := callFeedDelta(t, srv, "alice", "")
	if len(body.Bundles) != 3 {
		t.Fatalf("expected all 3 bundles to ride the delta, got %d", len(body.Bundles))
	}
	byID := make(map[string]deltaBundle, len(body.Bundles))
	for _, b := range body.Bundles {
		byID[toString(b.Primary["tweet_id"])] = b
	}
	if _, ok := byID["tw_seen"].Primary["is_seen"]; ok {
		t.Fatalf("primary should not carry inline is_seen: %#v", byID["tw_seen"].Primary)
	}
	if feedSeen := userStateRows(t, byID["tw_seen"], "feed_seen"); len(feedSeen) != 1 || feedSeen[0]["tweet_id"] != "tw_seen" {
		t.Fatalf("expected tw_seen feed_seen attachment, got %#v", feedSeen)
	}
	muted := userStateRows(t, byID["tw_muted"], "muted_accounts")
	if len(muted) != 1 || muted[0]["handle"] != "spammer" || muted[0]["muted"] != true {
		t.Fatalf("expected tw_muted muted attachment, got %#v", muted)
	}
	if rows := userStateRows(t, byID["tw_visible"], "feed_seen"); len(rows) != 0 {
		t.Fatalf("tw_visible should not carry feed_seen row, got %#v", rows)
	}
}

func TestFeedDeltaRetweetSourcesAttachmentMatchesRoomSchema(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text,
			is_retweet, content_hash, published_at, fetched_at, sync_seq
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tw_hash", "orig", "orig", "body",
		0, "hash_1", now, now, 300,
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO retweet_sources (content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at)
		 VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"hash_1", "rt1", "RT One", "tw_rt1", now-1000,
		"hash_1", "rt2", "RT Two", "tw_rt2", now-2000,
	); err != nil {
		t.Fatal(err)
	}

	body := callFeedDelta(t, srv, "alice", "")
	byID := make(map[string]deltaBundle, len(body.Bundles))
	for _, b := range body.Bundles {
		byID[toString(b.Primary["tweet_id"])] = b
	}
	bundle, ok := byID["tw_hash"]
	if !ok {
		t.Fatalf("tw_hash missing from delta response: %#v", body.Bundles)
	}
	raw, ok := bundle.Attachments["retweet_sources"]
	if !ok {
		t.Fatalf("retweet_sources missing from attachments: %#v", bundle.Attachments)
	}
	rows, ok := raw.([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("retweet_sources=%#v, want non-empty array", raw)
	}
	row0, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("retweet_sources[0]=%#v, want object", rows[0])
	}
	for _, key := range []string{"content_hash", "retweeter_handle", "tweet_id", "published_at"} {
		if _, ok := row0[key]; !ok {
			t.Fatalf("retweet_sources row missing %q: %#v", key, row0)
		}
	}
	if _, ok := row0["handle"]; ok {
		t.Fatalf("retweet_sources should use Room columns, got presentation row %#v", row0)
	}
	if row0["content_hash"] != "hash_1" {
		t.Fatalf("content_hash=%#v, want hash_1 in row %#v", row0["content_hash"], row0)
	}
}

func TestFeedDeltaCarriesThreadFieldsInline(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UnixMilli()
	insertFeedItemAt(t, srv, "tw_parent", "thread_author", now-1_000, 220)
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text,
			reply_to_handle, reply_to_status, is_reply, is_ghost,
			published_at, fetched_at, sync_seq
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tw_child", "thread_author", "thread_author", "reply",
		"thread_author", "tw_parent", 1, 0,
		now, now, 221,
	); err != nil {
		t.Fatal(err)
	}

	body := callFeedDelta(t, srv, "alice", "")
	byID := make(map[string]map[string]any, len(body.Bundles))
	for _, b := range body.Bundles {
		byID[toString(b.Primary["tweet_id"])] = b.Primary
	}
	child := byID["tw_child"]
	if child == nil {
		t.Fatalf("expected child bundle, got %#v", byID)
	}
	if got := toString(child["reply_to_handle"]); got != "thread_author" {
		t.Fatalf("reply_to_handle = %q, want thread_author", got)
	}
	if got := toString(child["reply_to_status"]); got != "tw_parent" {
		t.Fatalf("reply_to_status = %q, want tw_parent", got)
	}
	if child["is_reply"] != true {
		t.Fatalf("is_reply = %#v, want true", child["is_reply"])
	}
	if child["is_ghost"] != false {
		t.Fatalf("is_ghost = %#v, want false", child["is_ghost"])
	}
}

func TestFeedDeltaCarriesProtectedSeenAndMutedRowsWithState(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItem(t, srv, "tw_liked", "liked_handle", time.Now().UnixMilli(), 210)
	insertFeedItem(t, srv, "tw_bookmarked", "bookmark_handle", time.Now().UnixMilli(), 211)
	insertFeedItem(t, srv, "tw_hidden", "muted_handle", time.Now().UnixMilli(), 212)

	if err := srv.db.ExecRaw(
		`INSERT INTO feed_likes (username, tweet_id, liked_at) VALUES (?, ?, ?)`,
		"alice", "tw_liked", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO bookmarks (user_id, video_id, bookmarked_at) VALUES (?, ?, ?)`,
		"alice", "tw_bookmarked", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"alice", "tw_liked", time.Now().UnixMilli(),
		"alice", "tw_bookmarked", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO muted_accounts (handle, muted_at) VALUES (?, ?), (?, ?)`,
		"liked_handle", time.Now().UnixMilli(),
		"bookmark_handle", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO muted_accounts (handle, muted_at) VALUES (?, ?)`,
		"muted_handle", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	body := callFeedDelta(t, srv, "alice", "")
	if len(body.Bundles) != 3 {
		t.Fatalf("expected all 3 bundles, got %d", len(body.Bundles))
	}
	byID := map[string]deltaBundle{}
	for _, b := range body.Bundles {
		byID[toString(b.Primary["tweet_id"])] = b
	}
	if likes := userStateRows(t, byID["tw_liked"], "feed_likes"); len(likes) != 1 || likes[0]["liked"] != true {
		t.Fatalf("expected tw_liked like state, got %#v", likes)
	}
	if seen := userStateRows(t, byID["tw_liked"], "feed_seen"); len(seen) != 1 || seen[0]["seen"] != true {
		t.Fatalf("expected tw_liked seen state, got %#v", seen)
	}
	if bookmarks := userStateRows(t, byID["tw_bookmarked"], "bookmarks"); len(bookmarks) != 1 || bookmarks[0]["bookmarked"] != true {
		t.Fatalf("expected tw_bookmarked bookmark state, got %#v", bookmarks)
	}
	if muted := userStateRows(t, byID["tw_hidden"], "muted_accounts"); len(muted) != 1 || muted[0]["muted"] != true {
		t.Fatalf("expected tw_hidden muted state, got %#v", muted)
	}
}

func TestFeedDeltaDoesNotMaterializePropagatedBookmarkState(t *testing.T) {
	srv := newTestServer(t)
	nowMs := time.Now().UnixMilli()

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('twitter_hidden_source', 'hidden source', 'twitter', ?)`,
		219,
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_settings (channel_id, include_reposts, updated_at)
		VALUES ('twitter_hidden_source', 0, ?)`,
		nowMs,
	); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		id      string
		handle  string
		seq     int
		book    bool
		content string
		source  string
		retweet int
	}{
		{id: "tw_direct_bookmark", handle: "direct_handle", seq: 220, book: true, content: "shared_hash"},
		{id: "tw_sibling", handle: "sibling_handle", seq: 221, content: "shared_hash"},
		{id: "tw_filtered_sibling", handle: "filtered_handle", source: "hidden_source", retweet: 1, seq: 222, content: "shared_hash"},
	} {
		source := row.source
		if source == "" {
			source = row.handle
		}
		if err := srv.db.ExecRaw(`
			INSERT INTO feed_items (
				tweet_id, source_handle, author_handle, body_text, is_retweet,
				content_hash, published_at, fetched_at, sync_seq
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.id, source, row.handle, "body", row.retweet, row.content, nowMs, nowMs, row.seq,
		); err != nil {
			t.Fatal(err)
		}
		if row.book {
			if err := srv.db.ExecRaw(
				`INSERT INTO bookmarks (
					user_id, video_id, category_id, custom_title,
					account_handles, media_indices, bookmarked_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				"alice", row.id, 2, "saved title", "alice,bob", "0,2", nowMs+10,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := callFeedDeltaWithCutoff(t, srv, "alice", "", nowMs-1000)
	if len(body.Bundles) != 3 {
		t.Fatalf("expected 3 bundles, got %d", len(body.Bundles))
	}
	byID := map[string]deltaBundle{}
	for _, b := range body.Bundles {
		byID[toString(b.Primary["tweet_id"])] = b
	}
	directBookmarks := userStateRows(t, byID["tw_direct_bookmark"], "bookmarks")
	if len(directBookmarks) != 1 || directBookmarks[0]["bookmarked"] != true {
		t.Fatalf("direct bookmark should stay bookmarked, got %#v", directBookmarks)
	}
	if directBookmarks[0]["bookmarked_at"] != float64(nowMs+10) {
		t.Fatalf("direct bookmark should carry bookmarked_at, got %#v", directBookmarks[0])
	}
	if got := toString(directBookmarks[0]["custom_title"]); got != "saved title" {
		t.Fatalf("direct bookmark should carry custom title, got %q", got)
	}
	if got := toString(directBookmarks[0]["account_handles"]); got != "alice,bob" {
		t.Fatalf("direct bookmark should carry account handles, got %q", got)
	}
	if got := toString(directBookmarks[0]["media_indices"]); got != "0,2" {
		t.Fatalf("direct bookmark should carry media indices, got %q", got)
	}
	siblingBookmarks := userStateRows(t, byID["tw_sibling"], "bookmarks")
	if len(siblingBookmarks) != 1 || siblingBookmarks[0]["bookmarked"] != false {
		t.Fatalf("sibling display propagation must become authoritative clear, got %#v", siblingBookmarks)
	}
	if _, ok := byID["tw_sibling"].Primary["bookmarked_at"]; ok {
		t.Fatalf("sibling primary should not carry bookmarked_at, got %#v", byID["tw_sibling"].Primary)
	}
	filteredBookmarks := userStateRows(t, byID["tw_filtered_sibling"], "bookmarks")
	if len(filteredBookmarks) != 1 || filteredBookmarks[0]["bookmarked"] != false {
		t.Fatalf("filtered sibling still needs an authoritative clear, got %#v", filteredBookmarks)
	}
	if _, ok := byID["tw_filtered_sibling"].Primary["bookmarked_at"]; ok {
		t.Fatalf("filtered sibling primary should not carry bookmarked_at, got %#v", byID["tw_filtered_sibling"].Primary)
	}
}

func TestFeedDeltaPreservesRowsAndUsesRawPageSize(t *testing.T) {
	srv := newTestServer(t)
	nowMs := time.Now().UnixMilli()
	for i := 1; i <= 501; i++ {
		if err := srv.db.ExecRaw(
			`INSERT INTO feed_items (
				tweet_id, source_handle, author_handle, body_text,
				is_retweet, content_hash, published_at, fetched_at, sync_seq
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("tw_rt_%03d", i),
			"same_handle",
			"same_handle",
			"body",
			1,
			"shared_hash",
			nowMs,
			nowMs,
			i,
		); err != nil {
			t.Fatal(err)
		}
	}

	body := callFeedDelta(t, srv, "alice", "")
	if len(body.Bundles) != 500 {
		t.Fatalf("expected delta to preserve all rows in the page, got %d", len(body.Bundles))
	}
	if body.NextMarker != "500" {
		t.Fatalf("expected next_marker=500, got %q", body.NextMarker)
	}
	if body.EndOfStream {
		t.Fatalf("expected end_of_stream=false because raw page hit the cap")
	}
}

func TestFeedDeltaCutoffFiltersOldUnprotectedRowsButKeepsProtectedRows(t *testing.T) {
	srv := newTestServer(t)
	nowMs := time.Now().UnixMilli()
	cutoffMs := nowMs - 7*24*60*60*1000
	oldMs := cutoffMs - 24*60*60*1000

	insertFeedItemAt(t, srv, "tw_recent", "recent_handle", nowMs, 100)
	insertFeedItemAt(t, srv, "tw_old_hidden", "old_hidden", oldMs, 101)
	insertFeedItemAt(t, srv, "tw_old_liked", "old_liked", oldMs, 102)
	insertFeedItemAt(t, srv, "tw_old_bookmarked", "old_bookmarked", oldMs, 103)

	if err := srv.db.ExecRaw(
		`INSERT INTO feed_likes (username, tweet_id, liked_at) VALUES (?, ?, ?)`,
		"alice", "tw_old_liked", nowMs,
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO bookmarks (user_id, video_id, bookmarked_at) VALUES (?, ?, ?)`,
		"alice", "tw_old_bookmarked", nowMs,
	); err != nil {
		t.Fatal(err)
	}

	body := callFeedDeltaWithCutoff(t, srv, "alice", "", cutoffMs)
	if len(body.Bundles) != 3 {
		t.Fatalf("expected 3 bundles inside cutoff/protection, got %d", len(body.Bundles))
	}
	seen := map[string]bool{}
	for _, b := range body.Bundles {
		seen[toString(b.Primary["tweet_id"])] = true
	}
	if !seen["tw_recent"] {
		t.Fatalf("expected recent row to survive cutoff")
	}
	if !seen["tw_old_liked"] {
		t.Fatalf("expected liked row to survive cutoff")
	}
	if !seen["tw_old_bookmarked"] {
		t.Fatalf("expected bookmarked row to survive cutoff")
	}
	if seen["tw_old_hidden"] {
		t.Fatalf("old unprotected row should have been filtered out")
	}
}

// #7 — YouTube delta carries video_comments + sponsorblock_segments +
// sponsorblock_checked attachments inline. Comments capped at 50.
func TestVideosDeltaCarriesYouTubeAttachments(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_chan_a", "youtube", "Channel A")
	insertVideo(t, srv, "vid_youtube_1", "youtube_chan_a")
	if err := srv.db.ExecRaw(`UPDATE videos SET duration = 754, metadata_json = '{"view_count":182191,"like_count":9051}' WHERE video_id = 'vid_youtube_1'`); err != nil {
		t.Fatal(err)
	}

	// 60 comments — bundle should cap at 50.
	for i := 0; i < 60; i++ {
		if err := srv.db.ExecRaw(
			`INSERT INTO video_comments (video_id, comment_id, parent_id, author_name, author_id,
			   author_thumbnail, text, like_count, published_at, platform, fetched_at)
			 VALUES (?, ?, '', 'a', 'aid', '', 't', ?, ?, 'youtube', ?)`,
			"vid_youtube_1", fmt.Sprintf("c_%d", i), 60-i, time.Now().UnixMilli(), time.Now().UnixMilli(),
		); err != nil {
			t.Fatal(err)
		}
	}
	// One sponsorblock segment + checked-marker.
	if err := srv.db.ExecRaw(
		`INSERT INTO sponsorblock_segments (video_id, start_time, end_time, category) VALUES (?, ?, ?, ?)`,
		"vid_youtube_1", 12.5, 25.0, "sponsor",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.MarkSponsorBlockChecked("vid_youtube_1", "fresh"); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/videos/delta", "alice", "")
	if len(body.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(body.Bundles))
	}
	if got := toString(body.Bundles[0].Primary["duration_label"]); got != "12:34" {
		t.Fatalf("duration_label = %q, want 12:34", got)
	}
	metadataJSON, _ := body.Bundles[0].Primary["metadata_json"].(string)
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode metadata_json: %v", err)
	}
	if metadata["view_count_label"] != "182K" || metadata["like_count_label"] != "9.1K" {
		t.Fatalf("metadata labels = %#v", metadata)
	}
	atts := body.Bundles[0].Attachments
	comments, _ := atts["video_comments"].([]any)
	if len(comments) != 50 {
		t.Errorf("YouTube comments cap should be 50, got %d", len(comments))
	}
	firstComment, _ := comments[0].(map[string]any)
	for _, field := range []string{"thread_order", "thread_depth", "parent_order", "reply_to_author", "is_creator", "like_count_label"} {
		if _, ok := firstComment[field]; !ok {
			t.Fatalf("comment attachment missing %s: %#v", field, firstComment)
		}
	}
	if firstComment["like_count_label"] != "60" {
		t.Fatalf("comment like count label = %#v", firstComment)
	}
	segments, _ := atts["sponsorblock_segments"].([]any)
	if len(segments) != 1 {
		t.Errorf("expected 1 sponsorblock segment, got %d", len(segments))
	}
	if _, ok := atts["sponsorblock_checked"].(map[string]any); !ok {
		t.Errorf("missing sponsorblock_checked singleton: %v", atts["sponsorblock_checked"])
	}
}

func TestVideosDeltaCarriesCanonicalVideoURL(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_channel_link", "youtube", "Channel Link")
	insertVideo(t, srv, "youtube_sample_video_link", "youtube_channel_link")

	body := callDelta(t, srv, "/api/videos/delta", "alice", "")
	b := mustOneBundle(t, body)
	want := "https://www.youtube.com/watch?v=sample_video_link"
	if got := b.Primary["canonical_url"]; got != want {
		t.Fatalf("canonical_url = %#v, want %#v in primary %#v", got, want, b.Primary)
	}
}

func TestVideosDeltaReemitsVideoWhenCommentsArriveAfterCursor(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_chan_comments", "youtube", "Comments Channel")
	if err := srv.db.InsertVideo(
		"vid_youtube_comments",
		"youtube_chan_comments",
		"title",
		"",
		0,
		"",
		"",
		0,
		time.Now().UnixMilli(),
		"",
		"",
		0,
		false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	initial := callDelta(t, srv, "/api/videos/delta", "alice", "")
	if len(initial.Bundles) != 1 {
		t.Fatalf("expected initial video bundle, got %d", len(initial.Bundles))
	}

	inserted, err := srv.db.AddComments("vid_youtube_comments", []db.CommentInput{
		{
			CommentID: "comment-after-delta",
			Author:    "author",
			Text:      "comment text",
			Timestamp: time.Now().Unix(),
		},
	}, "youtube")
	if err != nil {
		t.Fatalf("AddComments: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted comments = %d, want 1", inserted)
	}

	follow := callDelta(t, srv, "/api/videos/delta", "alice", initial.NextMarker)
	if len(follow.Bundles) != 1 {
		t.Fatalf("expected video to re-emit after comments arrived, got %d bundles", len(follow.Bundles))
	}
	atts := follow.Bundles[0].Attachments
	comments, _ := atts["video_comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("expected 1 inline comment attachment, got %d (%v)", len(comments), atts["video_comments"])
	}
}

func TestVideosDeltaPrimaryCarriesDearrowFields(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_chan_b", "youtube", "Channel B")
	insertVideo(t, srv, "vid_youtube_da", "youtube_chan_b")

	title := "Real Title"
	casual := "Casual Title"
	thumb := "thumbnails/dearrow/vid_youtube_da.jpg"
	if err := srv.db.SetDearrowData("vid_youtube_da", &title, &casual, &thumb, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	body := callDelta(t, srv, "/api/videos/delta", "alice", "")
	if len(body.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(body.Bundles))
	}
	primary := body.Bundles[0].Primary
	if primary == nil {
		t.Fatal("primary nil")
	}

	// JSON round-trips string fields as string and int64 as float64.
	if got, _ := primary["dearrow_title"].(string); got != "Real Title" {
		t.Errorf("dearrow_title = %v, want 'Real Title'", primary["dearrow_title"])
	}
	if got, _ := primary["dearrow_title_casual"].(string); got != "Casual Title" {
		t.Errorf("dearrow_title_casual = %v, want 'Casual Title'", primary["dearrow_title_casual"])
	}
	if got, _ := primary["display_title"].(string); got != "Real Title" {
		t.Errorf("display_title = %v, want 'Real Title'", primary["display_title"])
	}
	if got, _ := primary["display_title_casual"].(string); got != "Casual Title" {
		t.Errorf("display_title_casual = %v, want 'Casual Title'", primary["display_title_casual"])
	}
	if got, _ := primary["dearrow_thumb_path"].(string); got != thumb {
		t.Errorf("dearrow_thumb_path = %v, want %q", primary["dearrow_thumb_path"], thumb)
	}
	if got, _ := primary["dearrow_checked_at_ms"].(float64); int64(got) != 1_700_000_000_000 {
		t.Errorf("dearrow_checked_at_ms = %v, want 1_700_000_000_000", primary["dearrow_checked_at_ms"])
	}
}

func TestVideosDeltaPrimaryEmitsNullDearrowFieldsWhenUnset(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_chan_c", "youtube", "Channel C")
	insertVideo(t, srv, "vid_youtube_plain", "youtube_chan_c")

	body := callDelta(t, srv, "/api/videos/delta", "alice", "")
	if len(body.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(body.Bundles))
	}
	primary := body.Bundles[0].Primary

	// ptrOrNil / ptrOrNilInt64 encode SQL NULL as JSON null. After round-trip
	// through json.Unmarshal into map[string]any, a JSON null shows up as a nil
	// interface — the type assertion fails, which is what we assert.
	for _, key := range []string{"dearrow_title", "dearrow_title_casual", "dearrow_thumb_path", "dearrow_checked_at_ms"} {
		if _, exists := primary[key]; !exists {
			t.Errorf("primary missing key %q (should be present as null)", key)
			continue
		}
		if primary[key] != nil {
			t.Errorf("primary[%q] = %v (type %T), want JSON null (nil)", key, primary[key], primary[key])
		}
	}
	if got, _ := primary["display_title"].(string); got != "title" {
		t.Errorf("display_title = %v, want original title", primary["display_title"])
	}
	if got, _ := primary["display_title_casual"].(string); got != "title" {
		t.Errorf("display_title_casual = %v, want original title", primary["display_title_casual"])
	}
}

func TestVideosDeltaCarriesBookmarkAndChannelState(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_chan_b", "youtube", "Channel B")
	insertVideo(t, srv, "vid_youtube_bookmarked", "youtube_chan_b")

	if err := srv.db.ExecRaw(
		`INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at) VALUES (?, ?, ?, ?)`,
		"alice", "vid_youtube_bookmarked", 7, time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, ?)`,
		"youtube_chan_b", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', ?, ?)`,
		"youtube_chan_b", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/videos/delta", "alice", "")
	b := mustOneBundle(t, body)
	if _, ok := b.Primary["is_bookmarked"]; ok {
		t.Fatalf("primary should not carry inline bookmark state: %#v", b.Primary)
	}
	bookmarks := userStateRows(t, b, "bookmarks")
	if len(bookmarks) != 1 || bookmarks[0]["bookmarked"] != true || bookmarks[0]["category_id"] != float64(7) {
		t.Fatalf("expected bookmark attachment with category 7, got %#v", bookmarks)
	}
	if follows := userStateRows(t, b, "channel_follows"); len(follows) != 1 || follows[0]["followed"] != true {
		t.Fatalf("expected channel follow attachment, got %#v", follows)
	}
	if stars := userStateRows(t, b, "channel_stars"); len(stars) != 1 || stars[0]["starred"] != true {
		t.Fatalf("expected channel star attachment, got %#v", stars)
	}
}

func TestShortsDeltaCarriesBookmarkAndChannelState(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "tiktok_chan_a", "tiktok", "TikTok A")
	insertVideo(t, srv, "vid_short_bookmarked", "tiktok_chan_a")

	if err := srv.db.ExecRaw(
		`INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at) VALUES (?, ?, ?, ?)`,
		"alice", "vid_short_bookmarked", 9, time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, ?)`,
		"tiktok_chan_a", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/shorts/delta", "alice", "")
	b := mustOneBundle(t, body)
	bookmarks := userStateRows(t, b, "bookmarks")
	if len(bookmarks) != 1 || bookmarks[0]["bookmarked"] != true {
		t.Fatalf("expected bookmark attachment, got %#v", bookmarks)
	}
	if bookmarks[0]["category_id"] != float64(9) {
		t.Fatalf("expected bookmark category 9, got %#v", bookmarks[0])
	}
	follows := userStateRows(t, b, "channel_follows")
	if len(follows) != 1 || follows[0]["followed"] != true {
		t.Fatalf("expected channel follow attachment, got %#v", follows)
	}
}

func TestShortsDeltaCarriesCanonicalVideoURL(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "tiktok_sample_channel_link", "tiktok", "Sample Channel")
	insertVideo(t, srv, "tiktok_sample_video_link", "tiktok_sample_channel_link")
	if err := srv.db.ExecRaw(
		`UPDATE channels SET source_id = ? WHERE channel_id = ?`,
		"sample_handle",
		"tiktok_sample_channel_link",
	); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/shorts/delta", "alice", "")
	b := mustOneBundle(t, body)
	want := "https://www.tiktok.com/@sample_handle/video/sample_video_link"
	if got := b.Primary["canonical_url"]; got != want {
		t.Fatalf("canonical_url = %#v, want %#v in primary %#v", got, want, b.Primary)
	}
}

func TestShortsDeltaCarriesInstagramRepostSources(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UnixMilli()
	insertChannel(t, srv, "instagram_owner", "instagram", "Owner")
	insertChannel(t, srv, "instagram_reposter", "instagram", "Reposter")
	insertVideo(t, srv, "instagram_reel_reposted", "instagram_owner")
	if _, err := srv.db.UpsertVideoRepostSources([]model.VideoRepostSource{{
		VideoID:             "instagram_reel_reposted",
		ReposterChannelID:   "instagram_reposter",
		ReposterHandle:      "reposter",
		ReposterDisplayName: "Reposter",
		FirstSeenAtMs:       now,
		UpdatedAtMs:         now,
	}}); err != nil {
		t.Fatalf("upsert repost source: %v", err)
	}

	body := callDelta(t, srv, "/api/shorts/delta", "alice", "")
	b := mustOneBundle(t, body)
	rows, ok := b.Attachments["video_repost_sources"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("video_repost_sources = %#v", b.Attachments["video_repost_sources"])
	}
	row := rows[0].(map[string]any)
	if got := row["repost_author_label"]; got != "Reposter" {
		t.Fatalf("repost_author_label = %#v, want Reposter; row=%#v", got, row)
	}
}

// #6 — channels delta primary row carries follow + star scalars from
// the side tables.
func TestChannelsDeltaCarriesUserStateAttachment(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "youtube_followed", "youtube", "Followed")
	insertChannel(t, srv, "youtube_unfollowed", "youtube", "Unfollowed")

	if err := srv.db.ExecRaw(
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, ?)`,
		"youtube_followed", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', ?, ?)`,
		"youtube_followed", time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/channels/delta", "alice", "")
	if len(body.Bundles) != 2 {
		t.Fatalf("expected 2 channel bundles, got %d", len(body.Bundles))
	}
	for _, b := range body.Bundles {
		isFollowed := b.Primary["channel_is_followed"]
		isStarred := b.Primary["channel_is_starred"]
		if isFollowed != nil || isStarred != nil {
			t.Fatalf("channel primary should not carry inline state: %#v", b.Primary)
		}
		if _, ok := b.Primary["check_interval"]; ok {
			t.Fatalf("channel primary should not carry retired check_interval: %#v", b.Primary)
		}
		if b.Primary["channel_id"] == "youtube_followed" {
			if follows := userStateRows(t, b, "channel_follows"); len(follows) != 1 || follows[0]["followed"] != true {
				t.Errorf("followed channel attachment = %#v", follows)
			}
			if stars := userStateRows(t, b, "channel_stars"); len(stars) != 1 || stars[0]["starred"] != true {
				t.Errorf("starred channel attachment = %#v", stars)
			}
		} else {
			if follows := userStateRows(t, b, "channel_follows"); len(follows) != 1 || follows[0]["followed"] != false {
				t.Errorf("unfollowed channel attachment = %#v", follows)
			}
			if stars := userStateRows(t, b, "channel_stars"); len(stars) != 1 || stars[0]["starred"] != false {
				t.Errorf("unstarred channel attachment = %#v", stars)
			}
		}
	}
}

func TestChannelsDeltaCarriesSourceIdAndChannelProfileAttachment(t *testing.T) {
	srv := newTestServer(t)
	insertChannel(t, srv, "twitter_alice", "twitter", "@alice")
	if err := srv.db.ExecRaw(
		`UPDATE channels SET source_id = ? WHERE channel_id = ?`,
		"alice", "twitter_alice",
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:    "twitter_alice",
		Platform:     "twitter",
		Handle:       "alice",
		DisplayName:  "Alice Doe",
		Bio:          "bio text",
		Website:      "https://example.com",
		Followers:    76123,
		Following:    228,
		Verified:     true,
		VerifiedType: "business",
		AvatarURL:    "https://example.com/avatar.jpg",
		BannerURL:    "https://example.com/banner.jpg",
	}); err != nil {
		t.Fatal(err)
	}

	body := callDelta(t, srv, "/api/channels/delta", "alice", "")
	b := mustOneBundle(t, body)
	if got := toString(b.Primary["source_id"]); got != "alice" {
		t.Fatalf("expected source_id=alice, got %q", got)
	}
	profile, ok := b.Attachments["channel_profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected channel_profile attachment, got %#v", b.Attachments["channel_profile"])
	}
	if got := toString(profile["display_name"]); got != "Alice Doe" {
		t.Fatalf("expected display_name attachment, got %q", got)
	}
	if got := toString(profile["bio"]); got != "bio text" {
		t.Fatalf("expected bio attachment, got %q", got)
	}
	if got := profile["followers"]; got != float64(76123) {
		t.Fatalf("expected followers=76123, got %#v", got)
	}
	if got := toString(profile["followers_label"]); got != "76.1K" {
		t.Fatalf("expected followers_label=76.1K, got %q", got)
	}
	if got := toString(profile["following_label"]); got != "228" {
		t.Fatalf("expected following_label=228, got %q", got)
	}
	if got := toString(profile["profile_url"]); got != "https://x.com/alice" {
		t.Fatalf("expected profile_url=https://x.com/alice, got %q", got)
	}
}

// Twitter media-completeness contract: each test inserts a feed_items row
// matching one row of the matrix and asserts the bundle.primary carries the
// documented media_json + quote_media_json shape verbatim.
func TestFeedDeltaMediaCompleteness_TextOnly(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_text", "u", 300, "", "")
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	assertMediaJSONAbsentOrEmpty(t, b.Primary, "media_json")
	assertMediaJSONAbsentOrEmpty(t, b.Primary, "quote_media_json")
}

func TestFeedDeltaMediaCompleteness_ParentOnly(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_pm", "u", 301,
		`[{"type":"image","url":"https://example.com/i.jpg"}]`, "")
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	if !strings.Contains(toString(b.Primary["media_json"]), `"image"`) {
		t.Errorf("expected media_json with image kind, got %v", b.Primary["media_json"])
	}
	assertMediaJSONAbsentOrEmpty(t, b.Primary, "quote_media_json")
}

func TestFeedDeltaMediaCompleteness_ParentAndQuote(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_pq", "u", 302,
		`[{"type":"image"}]`, `[{"type":"video"}]`)
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	if toString(b.Primary["media_json"]) == "" || toString(b.Primary["quote_media_json"]) == "" {
		t.Errorf("expected both media_json + quote_media_json populated; got %v / %v",
			b.Primary["media_json"], b.Primary["quote_media_json"])
	}
}

func TestFeedDeltaMediaCompleteness_TextParentQuoteMedia(t *testing.T) {
	srv := newTestServer(t)
	// Parent text-only, quote has video — #8 cascade case.
	insertFeedItemWithMedia(t, srv, "tw_tpqm", "u", 303,
		"", `[{"type":"video","url":"https://x.com/v.mp4"}]`)
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	assertMediaJSONAbsentOrEmpty(t, b.Primary, "media_json")
	if !strings.Contains(toString(b.Primary["quote_media_json"]), `"video"`) {
		t.Errorf("expected quote_media_json with video kind, got %v", b.Primary["quote_media_json"])
	}
}

func TestFeedDeltaMediaCompleteness_MixedSlideshow(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_mix", "u", 304,
		`[{"type":"image"},{"type":"video"},{"type":"gif"}]`, "")
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	mj := toString(b.Primary["media_json"])
	for _, kind := range []string{`"image"`, `"video"`, `"gif"`} {
		if !strings.Contains(mj, kind) {
			t.Errorf("mixed slideshow missing %s kind in %s", kind, mj)
		}
	}
}

func TestFeedDeltaMediaCompleteness_VideoItem(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_v", "u", 305,
		`[{"type":"video","url":"https://x.com/v.mp4","thumbnail_url":"https://x.com/t.jpg"}]`, "")
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	mj := toString(b.Primary["media_json"])
	if !strings.Contains(mj, `"video"`) {
		t.Errorf("expected video kind in media_json, got %s", mj)
	}
}

func TestFeedDeltaMediaCompleteness_GifItem(t *testing.T) {
	srv := newTestServer(t)
	insertFeedItemWithMedia(t, srv, "tw_g", "u", 306,
		`[{"type":"gif","url":"https://x.com/g.mp4"}]`, "")
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	if !strings.Contains(toString(b.Primary["media_json"]), `"gif"`) {
		t.Errorf("expected gif kind in media_json, got %v", b.Primary["media_json"])
	}
}

func TestFeedDeltaMediaCompleteness_QuoteHasVideoParentText(t *testing.T) {
	srv := newTestServer(t)
	// Same shape as TextParentQuoteMedia but explicitly named for the
	// matrix row "Quote has video (parent text)".
	insertFeedItemWithMedia(t, srv, "tw_qhv", "u", 307, "",
		`[{"type":"video"}]`)
	b := mustOneBundle(t, callFeedDelta(t, srv, "alice", ""))
	assertMediaJSONAbsentOrEmpty(t, b.Primary, "media_json")
	if !strings.Contains(toString(b.Primary["quote_media_json"]), `"video"`) {
		t.Errorf("quote_media_json should carry video kind, got %v", b.Primary["quote_media_json"])
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func decodeDelta(t *testing.T, raw []byte) deltaResponse {
	t.Helper()
	var body deltaResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode delta: %v (%s)", err, string(raw))
	}
	return body
}

func callFeedDelta(t *testing.T, srv *testServer, user, since string) deltaResponse {
	t.Helper()
	return callDelta(t, srv, "/api/feed/delta", user, since)
}

func callFeedDeltaWithCutoff(t *testing.T, srv *testServer, user, since string, cutoffMs int64) deltaResponse {
	t.Helper()
	path := "/api/feed/delta"
	query := make([]string, 0, 2)
	if since != "" {
		query = append(query, "since="+since)
	}
	if cutoffMs > 0 {
		query = append(query, fmt.Sprintf("cutoff_ms=%d", cutoffMs))
	}
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	req := httptest.NewRequest("GET", path, nil)
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%s: status %d — %s", path, rec.Code, rec.Body.String())
	}
	return decodeDelta(t, rec.Body.Bytes())
}

func callDelta(t *testing.T, srv *testServer, path, user, since string) deltaResponse {
	t.Helper()
	url := path
	if since != "" {
		url += "?since=" + since
	}
	req := httptest.NewRequest("GET", url, nil)
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%s: status %d — %s", path, rec.Code, rec.Body.String())
	}
	return decodeDelta(t, rec.Body.Bytes())
}

func mustOneBundle(t *testing.T, body deltaResponse) deltaBundle {
	t.Helper()
	if len(body.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(body.Bundles))
	}
	return body.Bundles[0]
}

func userStateRows(t *testing.T, bundle deltaBundle, key string) []map[string]any {
	t.Helper()
	raw, ok := bundle.Attachments["user_state"].(map[string]any)
	if !ok {
		t.Fatalf("missing user_state attachment on %#v", bundle)
	}
	if raw["version"] != float64(1) {
		t.Fatalf("user_state version = %#v, want 1", raw["version"])
	}
	values, _ := raw[key].([]any)
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		row, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("user_state[%s] row = %#v, want object", key, value)
		}
		rows = append(rows, row)
	}
	return rows
}

func assertRetweetSourceRoomRow(t *testing.T, bundle deltaBundle, contentHash, handle, displayName, tweetID string, publishedAt int64) {
	t.Helper()
	raw, ok := bundle.Attachments["retweet_sources"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("retweet_sources = %#v, want one row", bundle.Attachments["retweet_sources"])
	}
	row, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("retweet_sources[0] = %#v, want object", raw[0])
	}
	if _, ok := row["handle"]; ok {
		t.Fatalf("retweet_sources should use Room columns, got presentation row %#v", row)
	}
	if row["content_hash"] != contentHash ||
		row["retweeter_handle"] != handle ||
		row["retweeter_display_name"] != displayName ||
		row["tweet_id"] != tweetID ||
		int64(row["published_at"].(float64)) != publishedAt {
		t.Fatalf("retweet_sources row = %#v", row)
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func assertMediaJSONAbsentOrEmpty(t *testing.T, primary map[string]any, key string) {
	t.Helper()
	if v, present := primary[key]; present {
		if s := toString(v); s != "" && s != "[]" && s != "null" {
			t.Errorf("expected %s absent/empty, got %v", key, v)
		}
	}
}

func insertFeedItem(t *testing.T, srv *testServer, tweetID, handle string, publishedAt int64, syncSeq int64) {
	t.Helper()
	insertFeedItemAt(t, srv, tweetID, handle, publishedAt, syncSeq)
}

func insertFeedItemWithMedia(t *testing.T, srv *testServer, tweetID, handle string, syncSeq int64, mediaJSON, quoteMediaJSON string) {
	t.Helper()
	insertFeedItemWithMediaAt(t, srv, tweetID, handle, time.Now().UnixMilli(), syncSeq, mediaJSON, quoteMediaJSON)
}

func insertFeedItemAt(t *testing.T, srv *testServer, tweetID, handle string, publishedAt int64, syncSeq int64) {
	t.Helper()
	insertFeedItemWithMediaAt(t, srv, tweetID, handle, publishedAt, syncSeq, "", "")
}

func insertFeedItemWithRetweetSource(t *testing.T, srv *testServer, tweetID, contentHash string, publishedAt int64, syncSeq int64) {
	t.Helper()
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, source_handle, author_handle, body_text,
			content_hash, published_at, fetched_at, sync_seq
		)
		VALUES (?, 'sample_author', 'sample_author', 'body', ?, ?, ?, ?)`,
		tweetID, contentHash, publishedAt, publishedAt, syncSeq,
	); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO retweet_sources (
			content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at
		)
		VALUES (?, 'sample_reposter', 'Sample Reposter', 'sample_tweet_repost', ?)`,
		contentHash, publishedAt-1_000,
	); err != nil {
		t.Fatal(err)
	}
}

func insertFeedItemWithMediaAt(t *testing.T, srv *testServer, tweetID, handle string, publishedAt int64, syncSeq int64, mediaJSON, quoteMediaJSON string) {
	t.Helper()
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, body_text,
		  media_json, quote_media_json, published_at, fetched_at, sync_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tweetID, handle, handle, "body", mediaJSON, quoteMediaJSON, publishedAt, publishedAt, syncSeq,
	); err != nil {
		t.Fatal(err)
	}
}

func insertChannel(t *testing.T, srv *testServer, channelID, platform, name string) {
	t.Helper()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES (?, ?, ?, ?)`,
		channelID, name, platform, time.Now().UnixNano(),
	); err != nil {
		t.Fatal(err)
	}
}

func insertVideo(t *testing.T, srv *testServer, videoID, channelID string) {
	t.Helper()
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, sync_seq, published_at)
		VALUES (?, ?, ?, ?, ?)`,
		videoID, channelID, "title", time.Now().UnixNano(), time.Now().UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
}
