package subsonic

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	. "github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/server/subsonic/responses"
	"github.com/navidrome/navidrome/utils/req"
)

const (
	artistInfoJSON = "info.json"
	artistAvatar   = "avatar.jpg"
	artistHeader   = "header.jpg"
)

type localArtistInfoFile struct {
	Name       string   `json:"name"`
	Genres     []string `json:"genres"`
	Followers  *int64   `json:"followers"`
	Popularity *int     `json:"popularity"`
	Biography  string   `json:"biography"`
}

func (api *Router) GetArtistRichInfo(r *http.Request) (*responses.Subsonic, error) {
	id, err := req.Params(r).String("id")
	if err != nil {
		return nil, err
	}

	artist, artistDir, err := api.localArtistDir(r, id)
	if err != nil {
		return nil, err
	}

	info := responses.ArtistRichInfo{Name: artist.Name}
	if data, err := os.ReadFile(filepath.Join(artistDir, artistInfoJSON)); err == nil {
		var fileInfo localArtistInfoFile
		if err := json.Unmarshal(data, &fileInfo); err != nil {
			log.Warn(r, "Could not parse artist rich info file", "artist", artist.Name, "path", filepath.Join(artistDir, artistInfoJSON), err)
		} else {
			info.Name = firstNonEmpty(fileInfo.Name, artist.Name)
			info.Genres = fileInfo.Genres
			info.Followers = fileInfo.Followers
			info.Popularity = fileInfo.Popularity
			info.Biography = fileInfo.Biography
		}
	}

	info.HasAvatar = regularFileExists(filepath.Join(artistDir, artistAvatar))
	info.HasHeader = regularFileExists(filepath.Join(artistDir, artistHeader))

	response := newResponse()
	response.ArtistRichInfo = &info
	return response, nil
}

func (api *Router) GetArtistRichImage(w http.ResponseWriter, r *http.Request) (*responses.Subsonic, error) {
	id, err := req.Params(r).String("id")
	if err != nil {
		return nil, err
	}
	imageType := req.Params(r).StringOr("type", "avatar")

	var filename string
	switch imageType {
	case "avatar":
		filename = artistAvatar
	case "header":
		filename = artistHeader
	default:
		return nil, newError(responses.ErrorGeneric, "Invalid artist image type")
	}

	_, artistDir, err := api.localArtistDir(r, id)
	if err != nil {
		return nil, err
	}
	imagePath := filepath.Join(artistDir, filename)
	if !regularFileExists(imagePath) {
		return nil, newError(responses.ErrorDataNotFound, "Artist image not found")
	}

	if contentType := mime.TypeByExtension(filepath.Ext(imagePath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, imagePath)
	return nil, nil
}

func (api *Router) localArtistDir(r *http.Request, id string) (*model.Artist, string, error) {
	ctx := r.Context()
	artist, err := api.ds.Artist(ctx).Get(id)
	if errors.Is(err, model.ErrNotFound) {
		return nil, "", newError(responses.ErrorDataNotFound, "Artist not found")
	}
	if err != nil {
		return nil, "", err
	}

	if conf.Server.MusicFolder != "" {
		if dir, ok := cleanArtistDir(conf.Server.MusicFolder, artist.Name); ok {
			return artist, dir, nil
		}
	}

	mediaFiles, err := api.ds.MediaFile(ctx).GetAll(model.QueryOptions{
		Filters: Or{
			Eq{"artist_id": id},
			Eq{"album_artist_id": id},
		},
		Sort: "path",
		Max:  1,
	})
	if err != nil {
		return nil, "", err
	}
	if len(mediaFiles) > 0 {
		libPath, err := api.ds.Library(ctx).GetPath(mediaFiles[0].LibraryID)
		if err != nil {
			return nil, "", err
		}
		if dir, ok := cleanArtistDir(libPath, artist.Name); ok {
			return artist, dir, nil
		}
	}

	return artist, "", newError(responses.ErrorDataNotFound, "Artist rich info not found")
}

func cleanArtistDir(libraryPath, artistName string) (string, bool) {
	dir := filepath.Join(libraryPath, artistName)
	cleanLibraryPath, err := filepath.Abs(libraryPath)
	if err != nil {
		return "", false
	}
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(cleanLibraryPath, cleanDir)
	if err != nil || rel == ".." || rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return cleanDir, true
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
