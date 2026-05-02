package model

import "time"

const (
	ArtistRequestStatusWishlist      = "wishlist"
	ArtistRequestStatusAvailableSoon = "available_soon"
)

type ArtistRequest struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	NormalizedName string     `json:"normalizedName"`
	Status         string     `json:"status"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	MovedAt        *time.Time `json:"movedAt,omitempty"`
	VoteCount      int        `json:"voteCount"`
	UserVoted      bool       `json:"userVoted"`
}

type ArtistRequests []ArtistRequest

type ArtistRequestRepository interface {
	GetAll(userID string) (ArtistRequests, error)
	Create(name, normalizedName, userID string) (*ArtistRequest, error)
	ToggleVote(id, userID string) error
	Delete(id string) error
	UpdateName(id, name, normalizedName string) error
	Move(id, status string) error
}
