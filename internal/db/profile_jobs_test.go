package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestUpsertFeedItemsPersistsAndQueuesRoleIdentities(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	items := []model.FeedItem{
		{
			TweetID:                "sample_tweet",
			SourceHandle:           "sample_source",
			AuthorHandle:           "Sample_Author",
			AuthorDisplayName:      "Sample Author",
			AuthorAvatarURL:        "https://pbs.twimg.com/profile_images/100/sample_author.jpg",
			IsRetweet:              true,
			RetweetedByHandle:      "Sample_Reposter",
			RetweetedByDisplayName: "Sample Reposter",
			QuoteTweetID:           "sample_quote_tweet",
			QuoteAuthorHandle:      "Sample_Quote",
			QuoteAuthorDisplayName: "Sample Quote",
			QuoteAuthorAvatarURL:   "https://pbs.twimg.com/profile_images/101/sample_quote.jpg",
			ReplyToHandle:          "Sample_Reply",
			ReplyToStatus:          "sample_parent_tweet",
			IsReply:                true,
			PublishedAt:            &now,
		},
		{
			TweetID:              "sample_handleless_outer",
			AuthorHandle:         "Sample_Second",
			QuoteTweetID:         "sample_handleless_quote",
			QuoteAuthorAvatarURL: "https://pbs.twimg.com/profile_images/102/sample_handleless.jpg",
			PublishedAt:          &now,
		},
	}

	if n, err := d.UpsertFeedItems(items); err != nil || n != len(items) {
		t.Fatalf("UpsertFeedItems = (%d, %v), want (%d, nil)", n, err, len(items))
	}

	var authorID, quoteID, reposterID, replyID string
	if err := d.QueryRow(`
		SELECT COALESCE(channel_id, ''), COALESCE(quote_channel_id, ''),
		       COALESCE(reposter_channel_id, ''), COALESCE(reply_channel_id, '')
		FROM feed_items WHERE tweet_id = 'sample_tweet'
	`).Scan(&authorID, &quoteID, &reposterID, &replyID); err != nil {
		t.Fatalf("read persisted roles: %v", err)
	}
	if authorID != "twitter_sample_author" || quoteID != "twitter_sample_quote" ||
		reposterID != "twitter_sample_reposter" || replyID != "twitter_sample_reply" {
		t.Fatalf("persisted roles = %q, %q, %q, %q", authorID, quoteID, reposterID, replyID)
	}

	wantJobs := map[string]bool{
		"twitter_sample_author":   true,
		"twitter_sample_quote":    true,
		"twitter_sample_reposter": true,
		"twitter_sample_reply":    true,
		"twitter_sample_second":   true,
		"twitter_sample_source":   true,
	}
	rows, err := d.conn.Query(`SELECT channel_id FROM profile_jobs ORDER BY channel_id`)
	if err != nil {
		t.Fatalf("list profile jobs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	gotJobs := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan profile job: %v", err)
		}
		gotJobs[id] = true
	}
	if len(gotJobs) != len(wantJobs) {
		t.Fatalf("profile jobs = %v, want %v", gotJobs, wantJobs)
	}
	for id := range wantJobs {
		if !gotJobs[id] {
			t.Fatalf("missing profile job %q in %v", id, gotJobs)
		}
	}
	for _, forbiddenID := range []string{"twitter_avatarhash_"} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM channel_profiles WHERE channel_id = ? OR channel_id LIKE ?`, forbiddenID, forbiddenID+"%").Scan(&count); err != nil {
			t.Fatalf("count forbidden profile %s: %v", forbiddenID, err)
		}
		if count != 0 {
			t.Fatalf("synthetic profile %q count = %d, want 0", forbiddenID, count)
		}
	}

	for _, channelID := range []string{"twitter_sample_author", "twitter_sample_quote"} {
		asset, err := d.GetAssetByOwnerIdentity("avatar", "channel", channelID, 0)
		if err != nil || asset != nil {
			t.Fatalf("observation created channel avatar %s: %+v err=%v", channelID, asset, err)
		}
	}
	handleless, err := d.GetAssetByOwnerIdentity("avatar", "tweet", "sample_handleless_quote", 0)
	if err != nil {
		t.Fatal(err)
	}
	if handleless != nil {
		t.Fatalf("handleless quote created unowned identity asset: %+v", handleless)
	}
}

func TestUpsertFeedItemsQueuesLinkedMentionIdentities(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "sample_mention_only",
		AuthorHandle:  "sample_post_author",
		ReplyToHandle: "Sample_Reply",
		BodyText:      "with @Sample_Body and @sample_collection and sample_local@sample_domain.test https://x.com/@sample_url/status/1",
		QuoteBodyText: "quoted @Sample_Quote and @sample_domain.test",
		PublishedAt:   &now,
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	for _, channelID := range []string{
		"twitter_sample_reply",
		"twitter_sample_body",
		"twitter_sample_quote",
	} {
		var handle string
		var requested, completed int64
		if err := d.QueryRow(`
			SELECT cp.handle, pj.requested_revision, pj.completed_revision
			FROM channel_profiles cp
			JOIN profile_jobs pj ON pj.channel_id = cp.channel_id
			WHERE cp.channel_id = ?
		`, channelID).Scan(&handle, &requested, &completed); err != nil {
			t.Fatalf("read identity %s: %v", channelID, err)
		}
		if handle != strings.TrimPrefix(channelID, "twitter_") || requested != 1 || completed != 0 {
			t.Fatalf("identity %s = handle %q revision %d/%d", channelID, handle, requested, completed)
		}
	}

	for _, channelID := range []string{"twitter_sample_collection", "twitter_sample_domain", "twitter_sample_url"} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM profile_jobs WHERE channel_id = ?`, channelID).Scan(&count); err != nil {
			t.Fatalf("count excluded identity %s: %v", channelID, err)
		}
		if count != 0 {
			t.Fatalf("excluded identity %s queued %d jobs", channelID, count)
		}
	}
}

func TestUpsertFeedItemsRollsBackFeedIdentityAndAssetsTogether(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		CREATE TRIGGER fail_sample_profile_job
		BEFORE INSERT ON profile_jobs
		WHEN new.channel_id = 'twitter_sample_profile'
		BEGIN
			SELECT RAISE(ABORT, 'sample profile job failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:         "sample_tweet",
		AuthorHandle:    "sample_profile",
		AuthorAvatarURL: "https://pbs.twimg.com/profile_images/200/sample_profile.jpg",
	}})
	if err == nil {
		t.Fatal("UpsertFeedItems unexpectedly succeeded")
	}
	for table, predicate := range map[string]string{
		"feed_items":       "tweet_id = 'sample_tweet'",
		"channel_profiles": "channel_id = 'twitter_sample_profile'",
		"profile_jobs":     "channel_id = 'twitter_sample_profile'",
		"assets":           "owner_kind = 'channel' AND owner_id = 'twitter_sample_profile'",
	} {
		var count int
		if err := d.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + predicate).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after rollback = %d, want 0", table, count)
		}
	}
}

func TestProfileJobRevisionSupersedesInFlightResultAndRetriesDurably(t *testing.T) {
	d := openWritableTestDB(t)
	observedAt := time.Now().UTC().Truncate(time.Millisecond)
	firstAvatar := "https://pbs.twimg.com/profile_images/300/sample_first.jpg"
	item := model.FeedItem{
		TweetID:           "sample_tweet",
		AuthorHandle:      "sample_profile",
		AuthorDisplayName: "Sample First",
		AuthorAvatarURL:   firstAvatar,
		PublishedAt:       &observedAt,
		FetchedAt:         observedAt,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("initial UpsertFeedItems: %v", err)
	}

	claimNow := observedAt.Add(time.Second).UnixMilli()
	claimed, err := d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: claimNow, LeaseMs: 60_000, Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial ClaimProfileJobs = (%v, %v), want one", claimed, err)
	}
	firstJob := claimed[0]
	stored, complete, err := d.CompleteProfileJob(firstJob, model.ChannelProfile{
		ChannelID:   firstJob.ChannelID,
		Platform:    "twitter",
		Handle:      "sample_profile",
		DisplayName: "Sample First",
		AvatarURL:   firstAvatar,
	}, []Asset{profileJobTestReplacement(t, d, firstJob, "avatar", "first")}, observedAt.Add(2*time.Second).UnixMilli())
	if err != nil || !stored || !complete {
		t.Fatalf("CompleteProfileJob = (%v, %v, %v), want (true, true, nil)", stored, complete, err)
	}

	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("same identity UpsertFeedItems: %v", err)
	}
	job, err := d.GetProfileJob(firstJob.ChannelID)
	if err != nil {
		t.Fatalf("GetProfileJob after same identity: %v", err)
	}
	if job.RequestedRevision != 1 || job.CompletedRevision != 1 {
		t.Fatalf("same identity revisions = %d/%d, want 1/1", job.RequestedRevision, job.CompletedRevision)
	}

	item.AuthorDisplayName = "Sample Changed"
	item.AuthorAvatarURL = "https://pbs.twimg.com/profile_images/301/sample_changed.jpg"
	item.FetchedAt = observedAt.Add(3 * time.Second)
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("changed identity UpsertFeedItems: %v", err)
	}
	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: claimNow + 2000, LeaseMs: 60_000, Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].RequestedRevision != 2 {
		t.Fatalf("second ClaimProfileJobs = (%v, %v), want revision 2", claimed, err)
	}
	secondJob := claimed[0]

	item.AuthorDisplayName = "Sample Newest"
	item.FetchedAt = observedAt.Add(4 * time.Second)
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatalf("newer identity UpsertFeedItems: %v", err)
	}
	job, err = d.GetProfileJob(secondJob.ChannelID)
	if err != nil {
		t.Fatalf("GetProfileJob while revision active: %v", err)
	}
	if job.RequestedRevision != 2 || job.CompletedRevision != 1 || job.LeaseOwner != "sample-worker" {
		t.Fatalf("ingest superseded active revision: %#v", job)
	}
	if err := d.RequestProfileJob(secondJob.ChannelID, observedAt.Add(5*time.Second).UnixMilli()); err != nil {
		t.Fatalf("explicit RequestProfileJob: %v", err)
	}
	stored, complete, err = d.CompleteProfileJob(secondJob, model.ChannelProfile{
		ChannelID:   secondJob.ChannelID,
		Platform:    "twitter",
		Handle:      "sample_profile",
		DisplayName: "Sample Stale Result",
		AvatarURL:   item.AuthorAvatarURL,
	}, nil, claimNow+5000)
	if err != nil || stored || complete {
		t.Fatalf("superseded CompleteProfileJob = (%v, %v, %v), want (false, false, nil)", stored, complete, err)
	}
	profile, err := d.GetChannelProfile(secondJob.ChannelID)
	if err != nil {
		t.Fatalf("GetChannelProfile: %v", err)
	}
	if profile.DisplayName != "Sample First" {
		t.Fatalf("profile display after stale result = %q, want last fetched profile", profile.DisplayName)
	}
	job, err = d.GetProfileJob(secondJob.ChannelID)
	if err != nil {
		t.Fatalf("GetProfileJob after supersede: %v", err)
	}
	if job.RequestedRevision != 3 || job.CompletedRevision != 1 || job.LeaseOwner != "" {
		t.Fatalf("superseded job = %#v, want pending revision 3 with no lease", job)
	}

	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: claimNow + 6000, LeaseMs: 60_000, Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].RequestedRevision != 3 {
		t.Fatalf("third ClaimProfileJobs = (%v, %v), want revision 3", claimed, err)
	}
	retryAt := claimNow + 7000
	if err := d.RetryProfileJob(claimed[0], "sample transient failure", time.Minute, retryAt); err != nil {
		t.Fatalf("RetryProfileJob: %v", err)
	}
	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: retryAt + 59_000, Limit: 1})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("early retry claim = (%v, %v), want none", claimed, err)
	}
	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: retryAt + 60_000, Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 || claimed[0].LastError == "" {
		t.Fatalf("due retry claim = (%#v, %v), want durable failure state", claimed, err)
	}
}

func TestClaimProfileJobsClaimsNewestDueRequestFirst(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	handles := []string{
		"sample_a", "sample_b", "sample_alpha", "sample_beta", "sample_gamma",
		"sample_delta", "sample_first", "sample_second", "sample_user",
	}
	items := make([]model.FeedItem, 0, len(handles))
	for i, handle := range handles {
		publishedAt := base.Add(time.Duration(i) * time.Minute)
		items = append(items, model.FeedItem{
			TweetID:      fmt.Sprintf("sample_fair_job_%02d", i),
			AuthorHandle: handle,
			PublishedAt:  &publishedAt,
			FetchedAt:    publishedAt,
		})
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	claimed, err := d.ClaimProfileJobs(LeaseOptions{
		Owner: "sample-worker",
		NowMs: base.Add(10 * time.Minute).UnixMilli(),
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("ClaimProfileJobs: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ChannelID != "twitter_sample_user" {
		t.Fatalf("first claim = %+v, want newest due request", claimed)
	}
}

func TestProfileJobIngestObservationsCoalesceWhileRevisionIsInFlight(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	item := model.FeedItem{
		TweetID: "sample_coalesced_identity", AuthorHandle: "sample_author",
		AuthorDisplayName: "Observed Initial", PublishedAt: &now, FetchedAt: now,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatal(err)
	}
	claimed, err := d.ClaimProfileJobs(LeaseOptions{
		Owner: "sample-worker", NowMs: now.Add(time.Second).UnixMilli(), LeaseMs: 60_000, Limit: 1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v err=%v", claimed, err)
	}
	active := claimed[0]

	for i := 1; i <= 5; i++ {
		item.AuthorDisplayName = fmt.Sprintf("Observed Change %d", i)
		item.AuthorAvatarURL = fmt.Sprintf("https://pbs.twimg.com/profile_images/%d/sample.jpg", 600+i)
		item.FetchedAt = now.Add(time.Duration(i+1) * time.Second)
		if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
			t.Fatalf("observation %d: %v", i, err)
		}
		job, err := d.GetProfileJob(active.ChannelID)
		if err != nil {
			t.Fatalf("GetProfileJob after observation %d: %v", i, err)
		}
		if job.RequestedRevision != active.RequestedRevision || job.CompletedRevision != 0 ||
			job.LeaseOwner != active.LeaseOwner || profileJobLeaseUntilMs(*job) != profileJobLeaseUntilMs(active) {
			t.Fatalf("observation %d replaced active revision: active=%+v job=%+v", i, active, job)
		}
	}

	stored, complete, err := d.CompleteProfileJob(active, model.ChannelProfile{
		ChannelID: active.ChannelID, Platform: "twitter", Handle: "sample_author",
		DisplayName: "Fetched Current",
	}, nil, now.Add(10*time.Second).UnixMilli())
	if err != nil || !stored || !complete {
		t.Fatalf("completion after repeated observations = %v/%v err=%v", stored, complete, err)
	}
	job, err := d.GetProfileJob(active.ChannelID)
	if err != nil || job == nil || job.RequestedRevision != 1 || job.CompletedRevision != 1 {
		t.Fatalf("completed coalesced job = %+v err=%v", job, err)
	}
}

func TestProfileJobLeaseTokenRejectsExpiredSameOwnerClaim(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID: "sample_lease_token", AuthorHandle: "sample_test",
		AuthorDisplayName: "Observed Name", PublishedAt: &now,
	}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := d.ClaimProfileJobs(LeaseOptions{
		Owner: "sample-worker", NowMs: now.UnixMilli(), LeaseMs: 1000, Limit: 1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim = %+v err=%v", claimed, err)
	}
	stale := claimed[0]
	claimed, err = d.ClaimProfileJobs(LeaseOptions{
		Owner: "sample-worker", NowMs: now.Add(2 * time.Second).UnixMilli(), LeaseMs: 1000, Limit: 1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("replacement claim = %+v err=%v", claimed, err)
	}
	current := claimed[0]
	if stale.LeaseUntil == nil || current.LeaseUntil == nil || stale.LeaseUntil.Equal(*current.LeaseUntil) {
		t.Fatalf("lease tokens were not replaced: stale=%+v current=%+v", stale, current)
	}

	stored, complete, err := d.CompleteProfileJob(stale, model.ChannelProfile{
		ChannelID: stale.ChannelID, Platform: "twitter", Handle: "sample_test",
		DisplayName: "Stale Fetch",
	}, nil, now.Add(3*time.Second).UnixMilli())
	if stored || complete || !errors.Is(err, ErrQueueLeaseNotHeld) {
		t.Fatalf("stale same-owner completion = %v/%v err=%v", stored, complete, err)
	}
	stored, complete, err = d.CompleteProfileJob(current, model.ChannelProfile{
		ChannelID: current.ChannelID, Platform: "twitter", Handle: "sample_test",
		DisplayName: "Current Fetch",
	}, nil, now.Add(4*time.Second).UnixMilli())
	if err != nil || !stored || !complete {
		t.Fatalf("current completion = %v/%v err=%v", stored, complete, err)
	}
	profile, err := d.GetChannelProfile(current.ChannelID)
	if err != nil || profile == nil || profile.DisplayName != "Current Fetch" {
		t.Fatalf("published profile = %+v err=%v", profile, err)
	}
}

func TestCompleteProfileJobPreservesReadyAssetUntilReplacementAndRemovesAuthoritativeNoSource(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID: "sample_atomic_profile", AuthorHandle: "sample_profile",
		AuthorDisplayName: "Observed Name", PublishedAt: &now,
	}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: now.Add(time.Second).UnixMilli(), Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim = %+v err=%v", claimed, err)
	}
	job := claimed[0]
	oldSource := "https://example.test/old-avatar.png"
	stored, complete, err := d.CompleteProfileJob(job, model.ChannelProfile{
		ChannelID: job.ChannelID, Platform: "twitter", Handle: "sample_profile",
		DisplayName: "Old Fetched Name", AvatarURL: oldSource,
	}, []Asset{profileJobTestReplacement(t, d, job, "avatar", "old")}, now.Add(2*time.Second).UnixMilli())
	if err != nil || !stored || !complete {
		t.Fatalf("initial completion = %v/%v err=%v", stored, complete, err)
	}
	oldAsset, err := d.GetAssetByOwnerIdentity("avatar", "channel", job.ChannelID, 0)
	if err != nil || oldAsset == nil || oldAsset.State != AssetStateReady {
		t.Fatalf("old ready asset = %+v err=%v", oldAsset, err)
	}

	if err := d.RequestProfileJob(job.ChannelID, now.Add(3*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: now.Add(4 * time.Second).UnixMilli(), Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("replacement claim = %+v err=%v", claimed, err)
	}
	job = claimed[0]
	stored, complete, err = d.CompleteProfileJob(job, model.ChannelProfile{
		ChannelID: job.ChannelID, Platform: "twitter", Handle: "sample_profile",
		DisplayName: "Fetched Before Avatar", AvatarURL: "https://example.test/new-avatar.png",
	}, nil, now.Add(5*time.Second).UnixMilli())
	if err != nil || !stored || complete {
		t.Fatalf("partial completion = %v/%v err=%v, want stored and pending", stored, complete, err)
	}
	profile, err := d.GetChannelProfile(job.ChannelID)
	if err != nil || profile == nil || profile.DisplayName != "Fetched Before Avatar" || profile.AvatarURL != oldSource {
		t.Fatalf("partial profile was not published: %+v err=%v", profile, err)
	}
	asset, err := d.GetAssetByOwnerIdentity("avatar", "channel", job.ChannelID, 0)
	if err != nil || asset == nil || asset.FilePath != oldAsset.FilePath || asset.SHA256 != oldAsset.SHA256 || asset.SourceURL != oldSource {
		t.Fatalf("ready asset changed before replacement: %+v err=%v", asset, err)
	}

	if err := d.RetryProfileJob(job, "avatar unavailable", 0, now.Add(6*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	claimed, err = d.ClaimProfileJobs(LeaseOptions{Owner: "sample-worker", NowMs: now.Add(7 * time.Second).UnixMilli(), Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("no-source claim = %+v err=%v", claimed, err)
	}
	job = claimed[0]
	if err := d.ExecRaw(`
		CREATE TRIGGER fail_sample_profile_completion
		BEFORE UPDATE OF completed_revision ON profile_jobs
		WHEN old.channel_id = 'twitter_sample_profile'
		 AND new.completed_revision > old.completed_revision
		BEGIN
			SELECT RAISE(ABORT, 'sample completion failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	stored, complete, err = d.CompleteProfileJob(job, model.ChannelProfile{
		ChannelID: job.ChannelID, Platform: "twitter", Handle: "sample_profile",
		DisplayName: "Must Roll Back",
	}, nil, now.Add(8*time.Second).UnixMilli())
	if err == nil || stored || complete {
		t.Fatalf("triggered completion = %v/%v err=%v, want rollback", stored, complete, err)
	}
	profile, err = d.GetChannelProfile(job.ChannelID)
	if err != nil || profile == nil || profile.DisplayName != "Fetched Before Avatar" || profile.AvatarURL != oldSource {
		t.Fatalf("profile after completion rollback: %+v err=%v", profile, err)
	}
	asset, err = d.GetAssetByOwnerIdentity("avatar", "channel", job.ChannelID, 0)
	if err != nil || asset == nil || asset.FilePath != oldAsset.FilePath || asset.SourceURL != oldSource {
		t.Fatalf("asset after completion rollback: %+v err=%v", asset, err)
	}
	if err := d.ExecRaw(`DROP TRIGGER fail_sample_profile_completion`); err != nil {
		t.Fatal(err)
	}
	stored, complete, err = d.CompleteProfileJob(job, model.ChannelProfile{
		ChannelID: job.ChannelID, Platform: "twitter", Handle: "sample_profile",
		DisplayName: "Authoritative No Avatar",
	}, nil, now.Add(9*time.Second).UnixMilli())
	if err != nil || !stored || !complete {
		t.Fatalf("no-source completion = %v/%v err=%v", stored, complete, err)
	}
	if asset, err := d.GetAssetByOwnerIdentity("avatar", "channel", job.ChannelID, 0); err != nil || asset != nil {
		t.Fatalf("authoritative no-source asset = %+v err=%v", asset, err)
	}
}

func TestChannelIngestOwnersCreateDurableProfileJobs(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.AddChannel(model.Channel{
		ChannelID: "youtube_sample_channel",
		SourceID:  "sample_channel",
		Name:      "Sample Owner",
		Platform:  "youtube",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := d.EnsureTikTokChannelForRepost("tiktok_sample_creator", "sample_creator", "Sample Creator"); err != nil {
		t.Fatalf("EnsureTikTokChannelForRepost: %v", err)
	}
	if err := d.EnsureInstagramChannelForTagged("instagram_sample_tagged", "sample_tagged", "Sample Tagged", ""); err != nil {
		t.Fatalf("EnsureInstagramChannelForTagged: %v", err)
	}
	if err := d.ApplyFollowMutation("instagram_sample.followed", "set", time.Now().UnixMilli()); err != nil {
		t.Fatalf("ApplyFollowMutation: %v", err)
	}
	if _, err := d.ImportConfig(ConfigExport{
		Version: ConfigExportVersion,
		Subscriptions: []ChannelExport{{
			ChannelID: "youtube_sample_imported",
			Name:      "Sample Imported",
			Platform:  "youtube",
		}},
	}, false); err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}

	for _, channelID := range []string{
		"youtube_sample_channel",
		"tiktok_sample_creator",
		"instagram_sample_tagged",
		"instagram_sample.followed",
		"youtube_sample_imported",
	} {
		job, err := d.GetProfileJob(channelID)
		if err != nil {
			t.Fatalf("GetProfileJob(%s): %v", channelID, err)
		}
		if job == nil || job.RequestedRevision != 1 || job.CompletedRevision != 0 {
			t.Fatalf("profile job %s = %#v, want pending revision 1", channelID, job)
		}
	}
}

func TestLikeStubPersistsIdentityWithTheUserAction(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		tweetID   = "sample_liked_identity"
		channelID = "twitter_sample_author"
	)
	if err := d.InsertFeedLike(tweetID, map[string]string{
		"author_handle":       "sample_author",
		"author_display_name": "Sample Liked Author",
		"avatar_url":          "https://pbs.twimg.com/profile_images/500/sample_liked.jpg",
		"body_text":           "Sample liked content",
	}); err != nil {
		t.Fatalf("InsertFeedLike: %v", err)
	}
	var storedChannelID string
	if err := d.QueryRow(`SELECT COALESCE(channel_id, '') FROM feed_items WHERE tweet_id = ?`, tweetID).Scan(&storedChannelID); err != nil {
		t.Fatalf("read liked feed role: %v", err)
	}
	if storedChannelID != channelID {
		t.Fatalf("liked feed channel_id = %q, want %q", storedChannelID, channelID)
	}
	job, err := d.GetProfileJob(channelID)
	if err != nil {
		t.Fatalf("GetProfileJob: %v", err)
	}
	if job == nil || job.RequestedRevision != 1 || job.CompletedRevision != 0 {
		t.Fatalf("liked identity job = %#v, want pending revision 1", job)
	}
	asset, err := d.GetAsset(BuildAssetID("twitter", "channel", channelID, "avatar", 0), "avatar")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if asset != nil {
		t.Fatalf("liked observation created channel avatar before profile completion: %#v", asset)
	}
}

func profileJobTestReplacement(t *testing.T, d *DB, job model.ProfileJob, kind, suffix string) Asset {
	t.Helper()
	dir := "avatars"
	if kind == "banner" {
		dir = "banners"
	}
	rel := filepath.Join("thumbnails", dir, job.ChannelID+"-r"+suffix+".png")
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), rel), []byte("profile-"+kind+"-"+suffix))
	return Asset{AssetKind: kind, FilePath: rel, ContentType: "image/png"}
}
