package nativeapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/navidrome/navidrome/model/request"
)

type UserSyncResponse struct {
	UserData          string     `json:"userData"`
	UserDataUpdatedAt *time.Time `json:"userDataUpdatedAt"`
}

func (api *Router) getUserSync(w http.ResponseWriter, r *http.Request) {
	u, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	repo := api.ds.User(r.Context())
	user, err := repo.Get(u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := UserSyncResponse{
		UserData:          user.UserData,
		UserDataUpdatedAt: user.UserDataUpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (api *Router) postUserSync(w http.ResponseWriter, r *http.Request) {
	u, ok := request.UserFrom(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req UserSyncResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	repo := api.ds.User(r.Context())
	user, err := repo.Get(u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user.UserData = req.UserData
	now := time.Now()
	user.UserDataUpdatedAt = &now
	if req.UserDataUpdatedAt != nil {
		user.UserDataUpdatedAt = req.UserDataUpdatedAt
	}

	if err := repo.Put(user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := UserSyncResponse{
		UserData:          user.UserData,
		UserDataUpdatedAt: user.UserDataUpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
