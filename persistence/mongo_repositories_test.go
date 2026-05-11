package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/model"
)

func TestMongoFilterSupportsNativeBSONFilters(t *testing.T) {
	filter, err := mongoFilter(bsonFilter{"id": "song-1"})
	if err != nil {
		t.Fatalf("mongoFilter returned error: %v", err)
	}
	if got := filter["id"]; got != "song-1" {
		t.Fatalf("expected id filter to survive unchanged, got %#v", got)
	}
}

func TestMongoFilterMapsSquirrelFieldsToMongoFields(t *testing.T) {
	filter, err := mongoFilter(squirrel.Eq{
		"playlist.visibility": model.PlaylistVisibilityFeatured,
		"owner_id":            "user-1",
	})
	if err != nil {
		t.Fatalf("mongoFilter returned error: %v", err)
	}
	if got := filter["visibility"]; got != model.PlaylistVisibilityFeatured {
		t.Fatalf("expected visibility filter, got %#v", got)
	}
	if got := filter["ownerid"]; got != "user-1" {
		t.Fatalf("expected ownerid filter, got %#v", got)
	}
}

func TestScrobbleRecordRequiresAuthenticatedUser(t *testing.T) {
	repo := &mongoScrobbleRepository{ctx: context.Background()}
	err := repo.RecordScrobble("song-1", time.Now())
	if !errors.Is(err, model.ErrNotAuthorized) {
		t.Fatalf("expected ErrNotAuthorized, got %v", err)
	}
}
