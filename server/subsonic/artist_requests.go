package subsonic

import (
	"bufio"
	"os"
	"strings"
	"unicode"

	"net/http"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/server/subsonic/responses"
	"github.com/navidrome/navidrome/utils/req"
	"golang.org/x/text/unicode/norm"
)

const artistRequestSuggestionsEnv = "WAVES_MUSIC_ARTIST_REQUEST_SUGGESTIONS_TXT"

func (api *Router) GetArtistRequests(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		return nil, newError(responses.ErrorGeneric, "Internal error")
	}

	items, err := api.ds.ArtistRequest(r.Context()).GetAll(user.ID)
	if err != nil {
		return nil, err
	}

	response := newResponse()
	response.ArtistRequests = &responses.ArtistRequests{
		Artist:  artistRequestsToResponse(items),
		IsAdmin: user.IsAdmin,
	}
	return response, nil
}

func (api *Router) CreateArtistRequest(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		return nil, newError(responses.ErrorGeneric, "Internal error")
	}

	name, err := req.Params(r).String("name")
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	normalized := normalizeArtistRequestName(name)
	if normalized == "" {
		return nil, newError(responses.ErrorMissingParameter, "name")
	}

	_, err = api.ds.ArtistRequest(r.Context()).Create(name, normalized, user.ID)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, newError(responses.ErrorGeneric, "האמן כבר נמצא ברשימה")
		}
		return nil, err
	}

	return api.GetArtistRequests(r)
}

func (api *Router) ToggleArtistRequestVote(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		return nil, newError(responses.ErrorGeneric, "Internal error")
	}

	id, err := req.Params(r).String("id")
	if err != nil {
		return nil, err
	}
	if err := api.ds.ArtistRequest(r.Context()).ToggleVote(id, user.ID); err != nil {
		return nil, err
	}
	return api.GetArtistRequests(r)
}

func (api *Router) DeleteArtistRequest(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok || !user.IsAdmin {
		return nil, newError(responses.ErrorAuthorizationFail)
	}

	id, err := req.Params(r).String("id")
	if err != nil {
		return nil, err
	}
	if err := api.ds.ArtistRequest(r.Context()).Delete(id); err != nil {
		return nil, err
	}
	return api.GetArtistRequests(r)
}

func (api *Router) MoveArtistRequest(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok || !user.IsAdmin {
		return nil, newError(responses.ErrorAuthorizationFail)
	}

	p := req.Params(r)
	id, err := p.String("id")
	if err != nil {
		return nil, err
	}
	status, err := p.String("status")
	if err != nil {
		return nil, err
	}
	if err := api.ds.ArtistRequest(r.Context()).Move(id, status); err != nil {
		return nil, err
	}
	return api.GetArtistRequests(r)
}

func (api *Router) UpdateArtistRequestName(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok || !user.IsAdmin {
		return nil, newError(responses.ErrorAuthorizationFail)
	}

	p := req.Params(r)
	id, err := p.String("id")
	if err != nil {
		return nil, err
	}
	name, err := p.String("name")
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	normalized := normalizeArtistRequestName(name)
	if normalized == "" {
		return nil, newError(responses.ErrorMissingParameter, "name")
	}

	if err := api.ds.ArtistRequest(r.Context()).UpdateName(id, name, normalized); err != nil {
		if isUniqueConstraintError(err) {
			return nil, newError(responses.ErrorGeneric, "האמן כבר נמצא ברשימה")
		}
		return nil, err
	}
	return api.GetArtistRequests(r)
}

func (api *Router) GetArtistRequestSuggestions(r *http.Request) (*responses.Subsonic, error) {
	user, ok := request.UserFrom(r.Context())
	if !ok {
		return nil, newError(responses.ErrorGeneric, "Internal error")
	}

	p := req.Params(r)
	q := strings.TrimSpace(p.StringOr("query", ""))
	normalizedQuery := normalizeArtistRequestName(q)
	seen := map[string]bool{}
	suggestions := make([]string, 0, 16)

	add := func(name string) {
		name = strings.TrimSpace(name)
		normalized := normalizeArtistRequestName(name)
		if name == "" || normalized == "" || seen[normalized] {
			return
		}
		if normalizedQuery != "" && !strings.Contains(normalized, normalizedQuery) {
			return
		}
		seen[normalized] = true
		suggestions = append(suggestions, name)
	}

	if requests, err := api.ds.ArtistRequest(r.Context()).GetAll(user.ID); err == nil {
		for _, item := range requests {
			add(item.Name)
		}
	}
	if artists, err := api.ds.Artist(r.Context()).GetAll(model.QueryOptions{Sort: "name", Max: 1000}); err == nil {
		for _, artist := range artists {
			add(artist.Name)
		}
	}
	for _, name := range readArtistRequestSuggestionFile() {
		add(name)
	}

	if len(suggestions) > 12 {
		suggestions = suggestions[:12]
	}

	response := newResponse()
	response.ArtistRequestSuggestions = &responses.ArtistRequestSuggestions{Name: suggestions}
	return response, nil
}

func artistRequestsToResponse(items model.ArtistRequests) []responses.ArtistRequest {
	res := make([]responses.ArtistRequest, len(items))
	for i, item := range items {
		res[i] = responses.ArtistRequest{
			ID:        item.ID,
			Name:      item.Name,
			Status:    item.Status,
			VoteCount: item.VoteCount,
			UserVoted: item.UserVoted,
		}
	}
	return res
}

func normalizeArtistRequestName(name string) string {
	decomposed := norm.NFD.String(strings.ToLower(name))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func readArtistRequestSuggestionFile() []string {
	path := os.Getenv(artistRequestSuggestionsEnv)
	if path == "" {
		return nil
	}

	file, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil
	}
	defer file.Close()

	var names []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if name := strings.TrimSpace(scanner.Text()); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func isUniqueConstraintError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}
