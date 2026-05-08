package model

import "time"

type Scrobble struct {
	MediaFileID    string
	UserID         string
	SubmissionTime time.Time
}

type ScrobbleRepository interface {
	RecordScrobble(mediaFileID string, submissionTime time.Time) error
	GetRecentlyPlayed(userID string, limit int) (MediaFiles, error)
	GetCommunityRecentlyPlayed(limit int) (MediaFiles, error)
	GetCommunityMostPlayed(limit int) (MediaFiles, error)
	GetFollowingRecentlyPlayed(userID string, limit int) (MediaFiles, error)
}
