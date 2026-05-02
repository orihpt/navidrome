package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/navidrome/navidrome/model"
	"github.com/pocketbase/dbx"
)

type artistRequestRepository struct {
	ctx context.Context
	db  dbx.Builder
}

func NewArtistRequestRepository(ctx context.Context, db dbx.Builder) model.ArtistRequestRepository {
	return &artistRequestRepository{ctx: ctx, db: db}
}

type dbArtistRequest struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	NormalizedName string  `db:"normalized_name"`
	Status         string  `db:"status"`
	CreatedBy      string  `db:"created_by"`
	CreatedAt      string  `db:"created_at"`
	UpdatedAt      string  `db:"updated_at"`
	MovedAt        *string `db:"moved_at"`
	VoteCount      int     `db:"vote_count"`
	UserVoted      bool    `db:"user_voted"`
}

func (r *artistRequestRepository) GetAll(userID string) (model.ArtistRequests, error) {
	var rows []dbArtistRequest
	err := r.db.NewQuery(`
		SELECT
			ar.id,
			ar.name,
			ar.normalized_name,
			ar.status,
			ar.created_by,
			ar.created_at,
			ar.updated_at,
			ar.moved_at,
			COUNT(arv.user_id) AS vote_count,
			CASE WHEN uv.user_id IS NULL THEN 0 ELSE 1 END AS user_voted
		FROM artist_request ar
		LEFT JOIN artist_request_vote arv ON arv.artist_request_id = ar.id
		LEFT JOIN artist_request_vote uv ON uv.artist_request_id = ar.id AND uv.user_id = {:user_id}
		GROUP BY ar.id
		ORDER BY
			CASE WHEN ar.status = 'wishlist' THEN COUNT(arv.user_id) END DESC,
			CASE WHEN ar.status = 'wishlist' THEN lower(ar.name) END ASC,
			CASE WHEN ar.status = 'available_soon' THEN ar.moved_at END ASC,
			lower(ar.name) ASC
	`).Bind(dbx.Params{"user_id": userID}).All(&rows)
	if err != nil {
		return nil, err
	}

	requests := make(model.ArtistRequests, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, row.toModel())
	}
	return requests, nil
}

func (r *artistRequestRepository) Create(name, normalizedName, userID string) (*model.ArtistRequest, error) {
	id := uuid.NewString()
	_, err := r.db.NewQuery(`
		INSERT INTO artist_request (id, name, normalized_name, status, created_by)
		VALUES ({:id}, {:name}, {:normalized_name}, 'wishlist', {:user_id})
	`).Bind(dbx.Params{
		"id":              id,
		"name":            name,
		"normalized_name": normalizedName,
		"user_id":         userID,
	}).Execute()
	if err != nil {
		return nil, err
	}

	_, _ = r.db.NewQuery(`
		INSERT OR IGNORE INTO artist_request_vote (artist_request_id, user_id)
		VALUES ({:id}, {:user_id})
	`).Bind(dbx.Params{"id": id, "user_id": userID}).Execute()

	requests, err := r.GetAll(userID)
	if err != nil {
		return nil, err
	}
	for i := range requests {
		if requests[i].ID == id {
			return &requests[i], nil
		}
	}
	return nil, model.ErrNotFound
}

func (r *artistRequestRepository) ToggleVote(id, userID string) error {
	var row struct {
		Status string `db:"status"`
	}
	if err := r.db.NewQuery("SELECT status FROM artist_request WHERE id = {:id}").
		Bind(dbx.Params{"id": id}).One(&row); err != nil {
		return err
	}

	var existing struct {
		Count int `db:"count"`
	}
	err := r.db.NewQuery(`
		SELECT COUNT(*) AS count FROM artist_request_vote
		WHERE artist_request_id = {:id} AND user_id = {:user_id}
	`).Bind(dbx.Params{"id": id, "user_id": userID}).One(&existing)
	if err != nil {
		return err
	}

	if existing.Count > 0 {
		_, err = r.db.NewQuery(`
			DELETE FROM artist_request_vote
			WHERE artist_request_id = {:id} AND user_id = {:user_id}
		`).Bind(dbx.Params{"id": id, "user_id": userID}).Execute()
	} else {
		_, err = r.db.NewQuery(`
			INSERT INTO artist_request_vote (artist_request_id, user_id)
			VALUES ({:id}, {:user_id})
		`).Bind(dbx.Params{"id": id, "user_id": userID}).Execute()
	}
	if err != nil {
		return err
	}

	_, err = r.db.NewQuery(`
		DELETE FROM artist_request
		WHERE id = {:id}
		  AND status = 'wishlist'
		  AND NOT EXISTS (
			SELECT 1 FROM artist_request_vote WHERE artist_request_id = {:id}
		  )
	`).Bind(dbx.Params{"id": id}).Execute()
	return err
}

func (r *artistRequestRepository) Delete(id string) error {
	_, err := r.db.NewQuery("DELETE FROM artist_request WHERE id = {:id}").
		Bind(dbx.Params{"id": id}).Execute()
	return err
}

func (r *artistRequestRepository) UpdateName(id, name, normalizedName string) error {
	_, err := r.db.NewQuery(`
		UPDATE artist_request
		SET name = {:name}, normalized_name = {:normalized_name}, updated_at = datetime('now')
		WHERE id = {:id}
	`).Bind(dbx.Params{"id": id, "name": name, "normalized_name": normalizedName}).Execute()
	return err
}

func (r *artistRequestRepository) Move(id, status string) error {
	if status != model.ArtistRequestStatusWishlist && status != model.ArtistRequestStatusAvailableSoon {
		return errors.New("invalid artist request status")
	}

	movedAt := "NULL"
	if status == model.ArtistRequestStatusAvailableSoon {
		movedAt = "datetime('now')"
	}

	_, err := r.db.NewQuery(`
		UPDATE artist_request
		SET status = {:status}, moved_at = ` + movedAt + `, updated_at = datetime('now')
		WHERE id = {:id}
	`).Bind(dbx.Params{"id": id, "status": status}).Execute()
	return err
}

func (r dbArtistRequest) toModel() model.ArtistRequest {
	return model.ArtistRequest{
		ID:             r.ID,
		Name:           r.Name,
		NormalizedName: r.NormalizedName,
		Status:         r.Status,
		CreatedBy:      r.CreatedBy,
		VoteCount:      r.VoteCount,
		UserVoted:      r.UserVoted,
	}
}
