package db

import "testing"

func TestProtectedFeedItemStubCandidateCount(t *testing.T) {
	d := openFreshTestDB(t)

	count, err := d.CountProtectedFeedItemStubCandidates()
	if err != nil {
		t.Fatalf("CountProtectedFeedItemStubCandidates empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty candidate count = %d, want 0", count)
	}

	d.ExecRaw(`
		INSERT INTO feed_likes (
			username, tweet_id, source_handle, author_handle, author_display_name,
			body_text, canonical_x_link, published_at, liked_at
		) VALUES
			('admin', 'liked_missing', 'source_a', 'author_a', 'Author A',
			 'liked body', 'https://x.com/author_a/status/liked_missing', 1000, 1100),
			('admin', 'liked_existing', 'source_b', 'author_b', 'Author B',
			 'existing body', 'https://x.com/author_b/status/liked_existing', 2000, 2100)
	`)
	d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, body_text, published_at, fetched_at, sync_seq)
		VALUES ('liked_existing', 'author_b', 'existing body', 2000, 2100, 1)
	`)
	d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, author_handle, body_text, quote_tweet_id, quote_author_handle,
			quote_author_display_name, quote_body_text, quote_published_at, published_at,
			fetched_at, sync_seq
		) VALUES (
			'wrapper', 'wrapper_author', 'wrapper body', 'quote_missing', 'quote_author',
			'Quote Author', 'quote body', 3000, 3100, 3200, 2
		)
	`)
	d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'quote_missing')`)

	count, err = d.CountProtectedFeedItemStubCandidates()
	if err != nil {
		t.Fatalf("CountProtectedFeedItemStubCandidates seeded: %v", err)
	}
	if count != 2 {
		t.Fatalf("candidate count = %d, want 2", count)
	}

	created, err := d.EnsureProtectedFeedItemStubs()
	if err != nil {
		t.Fatalf("EnsureProtectedFeedItemStubs: %v", err)
	}
	if created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}

	count, err = d.CountProtectedFeedItemStubCandidates()
	if err != nil {
		t.Fatalf("CountProtectedFeedItemStubCandidates after repair: %v", err)
	}
	if count != 0 {
		t.Fatalf("post-repair candidate count = %d, want 0", count)
	}

	created, err = d.EnsureProtectedFeedItemStubs()
	if err != nil {
		t.Fatalf("EnsureProtectedFeedItemStubs second run: %v", err)
	}
	if created != 0 {
		t.Fatalf("second created = %d, want 0", created)
	}
}
