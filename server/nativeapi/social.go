package nativeapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"

	. "github.com/Masterminds/squirrel"
	"github.com/go-chi/chi/v5"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
)

func (api *Router) addSocialRoutes(r chi.Router) {
	r.Get("/community/activity", api.getCommunityActivity)
	r.Get("/community/featured", api.getCommunityFeatured)
	r.Get("/user/me", api.getCurrentUser)
	r.Get("/user/search", api.searchUsers)
	r.Get("/community/popular_playlists", api.getPopularPlaylists)
	r.Post("/user/avatar", api.uploadUserAvatar)
	r.Get("/user/{id}/profile", api.getUserProfile)
	r.Get("/user/{id}/avatar", api.getUserAvatar)
	r.Post("/user/{id}/follow", api.followUser)
	r.Delete("/user/{id}/follow", api.unfollowUser)
}

func (api *Router) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	current, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	user, err := api.ds.User(r.Context()).Get(current.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, user)
}

func (api *Router) getUserAvatar(w http.ResponseWriter, r *http.Request) {
	user, err := api.ds.User(r.Context()).Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if user.AvatarPath() == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, user.AvatarPath())
}

func (api *Router) getUserProfile(w http.ResponseWriter, r *http.Request) {
	userIDOrName := chi.URLParam(r, "id")
	users := api.ds.User(r.Context())
	user, err := users.Get(userIDOrName)
	if err != nil {
		user, err = users.FindByUsername(userIDOrName)
	}
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	playlists, err := api.ds.Playlist(r.Context()).GetAll(model.QueryOptions{
		Sort:  "updated_at",
		Order: "desc",
		Filters: And{
			Eq{"playlist.owner_id": user.ID},
			NotEq{"playlist.visibility": model.PlaylistVisibilityPrivate},
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	recentTracks, err := api.ds.Scrobble(r.Context()).GetRecentlyPlayed(user.ID, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type artistSummary struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		PlayCount int    `json:"playCount"`
	}
	artistsByID := map[string]*artistSummary{}
	for _, track := range recentTracks {
		artistID := track.ArtistID
		artistName := track.Artist
		if artistID == "" {
			artistID = artistName
		}
		if artistID == "" && artistName == "" {
			continue
		}
		if artistName == "" {
			artistName = artistID
		}
		artist, ok := artistsByID[artistID]
		if !ok {
			artist = &artistSummary{ID: artistID, Name: artistName}
			artistsByID[artistID] = artist
		}
		artist.PlayCount++
	}
	listeningArtists := make([]artistSummary, 0, len(artistsByID))
	for _, artist := range artistsByID {
		listeningArtists = append(listeningArtists, *artist)
	}
	sort.Slice(listeningArtists, func(i, j int) bool {
		if listeningArtists[i].PlayCount == listeningArtists[j].PlayCount {
			return listeningArtists[i].Name < listeningArtists[j].Name
		}
		return listeningArtists[i].PlayCount > listeningArtists[j].PlayCount
	})
	if len(listeningArtists) > 12 {
		listeningArtists = listeningArtists[:12]
	}

	writeJSON(w, struct {
		ID               string          `json:"id"`
		UserName         string          `json:"userName"`
		DisplayName      string          `json:"display_name"`
		AvatarFile       string          `json:"avatarFile,omitempty"`
		About            string          `json:"about,omitempty"`
		Playlists        model.Playlists `json:"playlists"`
		ListeningArtists []artistSummary `json:"listeningArtists"`
	}{
		ID:               user.ID,
		UserName:         user.UserName,
		DisplayName:      user.Name,
		AvatarFile:       user.AvatarFile,
		About:            user.About,
		Playlists:        playlists,
		ListeningArtists: listeningArtists,
	})
}

func (api *Router) uploadUserAvatar(w http.ResponseWriter, r *http.Request) {
	handleImageUpload(func(ctx context.Context, reader io.Reader, ext string) error {
		current, ok := request.UserFrom(ctx)
		if !ok {
			return model.ErrNotAuthorized
		}
		user, err := api.ds.User(ctx).Get(current.ID)
		if err != nil {
			return err
		}
		filename, err := api.imgUpload.SetImage(ctx, consts.EntityUser, user.ID, user.UserName, user.AvatarPath(), reader, ext)
		if err != nil {
			return err
		}
		user.AvatarFile = filename
		return api.ds.User(ctx).Put(user)
	})(w, r)
}

func (api *Router) getCommunityActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	limit := intParam(r, "limit", 20)
	repo := api.ds.Scrobble(r.Context())
	recent, err := repo.GetCommunityRecentlyPlayed(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mostPlayed, err := repo.GetCommunityMostPlayed(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	followingRecent, err := repo.GetFollowingRecentlyPlayed(user.ID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		RecentlyPlayed  model.MediaFiles `json:"recentlyPlayed"`
		MostPlayed      model.MediaFiles `json:"mostPlayed"`
		FollowingRecent model.MediaFiles `json:"followingRecent"`
	}{
		RecentlyPlayed:  recent,
		MostPlayed:      mostPlayed,
		FollowingRecent: followingRecent,
	})
}

func (api *Router) getCommunityFeatured(w http.ResponseWriter, r *http.Request) {
	playlists, err := api.ds.Playlist(r.Context()).GetAll(model.QueryOptions{
		Max:     intParam(r, "limit", 24),
		Sort:    "updated_at",
		Order:   "desc",
		Filters: Eq{"playlist.visibility": model.PlaylistVisibilityFeatured},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, playlists)
}

func (api *Router) getPopularPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := api.ds.Playlist(r.Context()).GetAll(model.QueryOptions{
		Max:   intParam(r, "limit", 24),
		Sort:  "play_count",
		Order: "desc",
		Filters: Or{
			Eq{"playlist.visibility": model.PlaylistVisibilityPublic},
			Eq{"playlist.visibility": model.PlaylistVisibilityFeatured},
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, playlists)
}

func (api *Router) searchUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	users, err := api.ds.User(r.Context()).Search(query, model.QueryOptions{Max: intParam(r, "limit", 20)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, users)
}

func (api *Router) followUser(w http.ResponseWriter, r *http.Request) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	followedID := chi.URLParam(r, "id")
	if followedID == "" || followedID == user.ID {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := api.ds.User(r.Context()).Follow(user.ID, followedID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": followedID, "following": true})
}

func (api *Router) unfollowUser(w http.ResponseWriter, r *http.Request) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	followedID := chi.URLParam(r, "id")
	if err := api.ds.User(r.Context()).Unfollow(user.ID, followedID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": followedID, "following": false})
}

func intParam(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
