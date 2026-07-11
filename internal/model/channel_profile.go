package model

import "time"

// ChannelProfile is the unified profile record for a channel across all
// platforms. Fields that don't apply to a given platform are zero-value
// (e.g., Followers is 0 for TikTok, VerifiedType is "" for YouTube).
type ChannelProfile struct {
	ChannelID         string // 'twitter_alice' | 'youtube_UC...' | 'tiktok_bob'
	Platform          string // 'twitter' | 'youtube' | 'tiktok'
	Handle            string // display handle (lowercase twitter handle; tiktok uniqueId; youtube @handle if known)
	DisplayName       string
	Bio               string
	Website           string
	Followers         int // 0 when unavailable for platform
	Following         int // 0 when unavailable
	Verified          bool
	VerifiedType      string // twitter only: individual/business/government
	Protected         bool   // twitter only
	AvatarURL         string // source URL (change detection)
	BannerURL         string // source URL; "" when platform has no banner
	ObservedAt        *time.Time
	FetchedAt         *time.Time
	Tombstone         bool
	StoryState        string
	StoryCount        int
	StoryUnseenCount  int
	StoryFirstVideoID string
}

// ProfileJob is the durable fetch request for one channel identity. A request
// remains pending while RequestedRevision is newer than CompletedRevision.
type ProfileJob struct {
	ChannelID         string
	RequestedRevision int64
	CompletedRevision int64
	RequestedAt       time.Time
	LeaseOwner        string
	LeaseUntil        *time.Time
	Attempts          int
	NextAttemptAt     *time.Time
	LastError         string
	UpdatedAt         time.Time
}
