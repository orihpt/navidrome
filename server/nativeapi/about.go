package nativeapi

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
)

type aboutResponse struct {
	ID       string `json:"id"`
	Format   string `json:"format"`
	Markdown string `json:"markdown"`
}

func getAbout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aboutPath := conf.Server.WavesMusicAboutPath
	if aboutPath == "" {
		http.Error(w, "WAVES_MUSIC_ABOUT_MD_PATH is not configured", http.StatusNotFound)
		return
	}

	markdown, err := os.ReadFile(aboutPath) //nolint:gosec
	if err != nil {
		log.Error(ctx, "Error reading Waves Music about markdown", "path", aboutPath, err)
		http.Error(w, "Unable to read about page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(aboutResponse{
		ID:       "about",
		Format:   "markdown",
		Markdown: string(markdown),
	}); err != nil {
		log.Error(ctx, "Error encoding about response", err)
	}
}
