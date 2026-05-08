package tests

import (
	"context"
	"time"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
)

type MockScrobbleRepo struct {
	RecordedScrobbles []model.Scrobble
	ctx               context.Context
}

func (m *MockScrobbleRepo) RecordScrobble(fileID string, submissionTime time.Time) error {
	user, _ := request.UserFrom(m.ctx)
	m.RecordedScrobbles = append(m.RecordedScrobbles, model.Scrobble{
		MediaFileID:    fileID,
		UserID:         user.ID,
		SubmissionTime: submissionTime,
	})
	return nil
}

func (m *MockScrobbleRepo) GetRecentlyPlayed(string, int) (model.MediaFiles, error) {
	return model.MediaFiles{}, nil
}

func (m *MockScrobbleRepo) GetCommunityRecentlyPlayed(int) (model.MediaFiles, error) {
	return model.MediaFiles{}, nil
}

func (m *MockScrobbleRepo) GetCommunityMostPlayed(int) (model.MediaFiles, error) {
	return model.MediaFiles{}, nil
}

func (m *MockScrobbleRepo) GetFollowingRecentlyPlayed(string, int) (model.MediaFiles, error) {
	return model.MediaFiles{}, nil
}
