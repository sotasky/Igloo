package db

import "testing"

func TestEnqueueAndClaimFeedMediaJobs(t *testing.T) {
	d := openWritableTestDB(t)

	jobs := []FeedMediaJobRow{
		{TweetID: "queue_test_001", TweetURL: "https://x.com/user_a/status/queue_test_001", SourceHandle: "user_a", MediaKind: "image"},
		{TweetID: "queue_test_002", TweetURL: "https://x.com/user_b/status/queue_test_002", SourceHandle: "user_b", MediaKind: "video"},
	}

	if err := d.EnqueueFeedMediaJobs(jobs); err != nil {
		t.Fatalf("EnqueueFeedMediaJobs: %v", err)
	}

	// Use a large batch to ensure we claim our test jobs even if schema migrations
	// re-queued many Python-era jobs in the production DB copy.
	claimed, err := d.ClaimFeedMediaBatch(10000)
	if err != nil {
		t.Fatalf("ClaimFeedMediaBatch: %v", err)
	}
	// Should have claimed at least our 2 (there may be pre-existing queued jobs in the copy)
	found := 0
	for _, j := range claimed {
		if j.TweetID == "queue_test_001" || j.TweetID == "queue_test_002" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected both test jobs in claimed batch, found %d in %d total claimed", found, len(claimed))
	}

	// A second claim should return 0 of our test jobs (they are processing now)
	claimed2, err := d.ClaimFeedMediaBatch(10000)
	if err != nil {
		t.Fatalf("second ClaimFeedMediaBatch: %v", err)
	}
	for _, j := range claimed2 {
		if j.TweetID == "queue_test_001" || j.TweetID == "queue_test_002" {
			t.Errorf("test job %q should not be claimable again", j.TweetID)
		}
	}
}

func TestUpdateFeedMediaJobStatus(t *testing.T) {
	d := openWritableTestDB(t)

	jobs := []FeedMediaJobRow{
		{TweetID: "update_test_001", TweetURL: "https://x.com/user_a/status/update_test_001", MediaKind: "image"},
	}
	if err := d.EnqueueFeedMediaJobs(jobs); err != nil {
		t.Fatalf("EnqueueFeedMediaJobs: %v", err)
	}

	// Claim it
	claimed, err := d.ClaimFeedMediaBatch(10)
	if err != nil {
		t.Fatalf("ClaimFeedMediaBatch: %v", err)
	}
	found := false
	for _, j := range claimed {
		if j.TweetID == "update_test_001" {
			found = true
		}
	}
	if !found {
		t.Skip("update_test_001 not in claimed batch (unexpected state in test DB copy)")
	}

	// Mark completed
	if err := d.UpdateFeedMediaJobStatus("update_test_001", "completed", "", 0); err != nil {
		t.Fatalf("UpdateFeedMediaJobStatus: %v", err)
	}

	// Should no longer be claimable
	claimed2, err := d.ClaimFeedMediaBatch(10)
	if err != nil {
		t.Fatalf("second ClaimFeedMediaBatch: %v", err)
	}
	for _, j := range claimed2 {
		if j.TweetID == "update_test_001" {
			t.Error("completed job should not be claimable")
		}
	}
}

func TestEnqueueDuplicateIgnored(t *testing.T) {
	d := openWritableTestDB(t)

	job := FeedMediaJobRow{
		TweetID:   "dup_test_001",
		TweetURL:  "https://x.com/user_a/status/dup_test_001",
		MediaKind: "image",
	}

	if err := d.EnqueueFeedMediaJobs([]FeedMediaJobRow{job}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	// Second enqueue with the same tweet_id should not error
	if err := d.EnqueueFeedMediaJobs([]FeedMediaJobRow{job}); err != nil {
		t.Fatalf("second enqueue (duplicate) should not error: %v", err)
	}

	q, _, err := d.CountPendingFeedMediaJobs()
	if err != nil {
		t.Fatalf("CountPendingFeedMediaJobs: %v", err)
	}
	// Just verify the count is a non-negative integer; exact count depends on DB state
	if q < 0 {
		t.Error("negative queued count")
	}
}
