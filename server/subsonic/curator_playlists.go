package subsonic

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/server/subsonic/responses"
	"github.com/navidrome/navidrome/utils/req"
	"github.com/navidrome/navidrome/utils/slice"
)

const curatorUserName = "wavesmusic_curator"

type curatorImportTrack struct {
	Index  int
	Artist string
	Album  string
	Name   string
	ISRC   string
}

func (api *Router) GetCuratorPlaylists(r *http.Request) (*responses.Subsonic, error) {
	ctx := r.Context()
	curator, err := api.ds.User(ctx).FindByUsername(curatorUserName)
	if errors.Is(err, model.ErrNotFound) {
		resp := newResponse()
		resp.Playlists = &responses.Playlists{}
		return resp, nil
	}
	if err != nil {
		return nil, err
	}

	pls, err := api.playlists.GetAll(ctx, model.QueryOptions{
		Sort:  "curator_pinned asc, updated_at asc",
		Order: "desc",
		Filters: And{
			Eq{"playlist.owner_id": curator.ID},
			Eq{"playlist.public": true},
		},
	})
	if err != nil {
		return nil, err
	}

	resp := newResponse()
	resp.Playlists = &responses.Playlists{Playlist: slice.MapWithArg(pls, ctx, api.buildPlaylist)}
	return resp, nil
}

func (api *Router) SetCuratorPlaylistPublished(r *http.Request) (*responses.Subsonic, error) {
	ctx := r.Context()
	usr, _ := request.UserFrom(ctx)
	if usr.UserName != curatorUserName {
		return nil, model.ErrNotAuthorized
	}
	p := req.Params(r)
	playlistID, err := p.String("playlistId")
	if err != nil {
		return nil, err
	}
	published, err := p.Bool("published")
	if err != nil {
		return nil, err
	}
	pls, err := api.playlists.Get(ctx, playlistID)
	if err != nil {
		return nil, err
	}
	if pls.OwnerID != usr.ID {
		return nil, model.ErrNotAuthorized
	}
	if err := api.playlists.Update(ctx, playlistID, nil, nil, &published, nil, nil); err != nil {
		return nil, err
	}
	return newResponse(), nil
}

func (api *Router) SetCuratorPlaylistPinned(r *http.Request) (*responses.Subsonic, error) {
	p := req.Params(r)
	playlistID, err := p.String("playlistId")
	if err != nil {
		return nil, err
	}
	pinned, err := p.Bool("pinned")
	if err != nil {
		return nil, err
	}
	if err := api.playlists.SetCuratorPinned(r.Context(), playlistID, pinned); err != nil {
		return nil, err
	}
	return newResponse(), nil
}

func (api *Router) ImportCuratorPlaylist(w http.ResponseWriter, r *http.Request) (*responses.Subsonic, error) {
	ctx := r.Context()
	usr, _ := request.UserFrom(ctx)
	if usr.UserName != curatorUserName {
		return nil, model.ErrNotAuthorized
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return nil, newError(responses.ErrorMissingParameter, "Required name parameter is missing")
	}
	description := strings.TrimSpace(r.FormValue("description"))
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, newError(responses.ErrorMissingParameter, "Required file parameter is missing")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		return nil, err
	}

	rows, err := parseCuratorImportFile(filepath.Ext(header.Filename), data)
	if err != nil {
		return nil, err
	}
	result, err := api.matchCuratorImportRows(ctx, rows)
	if err != nil {
		return nil, err
	}
	ids := append([]string{}, extractSongIDs(result.ExactMatches.Rows)...)
	ids = append(ids, extractSongIDs(result.NameMatches.Rows)...)
	playlistID, err := api.playlists.Create(ctx, "", name, ids)
	if err != nil {
		return nil, err
	}
	if description != "" {
		if err := api.playlists.Update(ctx, playlistID, nil, &description, nil, nil, nil); err != nil {
			return nil, err
		}
	}
	result.PlaylistID = playlistID

	resp := newResponse()
	resp.CuratorPlaylistImport = result
	return resp, nil
}

func (api *Router) matchCuratorImportRows(ctx context.Context, rows []curatorImportTrack) (*responses.CuratorPlaylistImport, error) {
	result := &responses.CuratorPlaylistImport{}
	for _, row := range rows {
		respRow := responses.CuratorImportRow{Index: row.Index, Artist: row.Artist, Album: row.Album, Name: row.Name, ISRC: row.ISRC}
		if row.ISRC != "" {
			mf, err := api.findTrackByISRC(ctx, row.ISRC)
			if err != nil {
				return nil, err
			}
			if mf != nil {
				respRow.SongID = mf.ID
				respRow.Title = mf.Title
				result.ExactMatches.Rows = append(result.ExactMatches.Rows, respRow)
				continue
			}
		}
		mf, err := api.findTrackByName(ctx, row)
		if err != nil {
			return nil, err
		}
		if mf != nil {
			respRow.SongID = mf.ID
			respRow.Title = mf.Title
			result.NameMatches.Rows = append(result.NameMatches.Rows, respRow)
			continue
		}
		result.MissingTracks.Rows = append(result.MissingTracks.Rows, respRow)
	}
	return result, nil
}

func (api *Router) findTrackByISRC(ctx context.Context, isrc string) (*model.MediaFile, error) {
	matches, err := api.ds.MediaFile(ctx).GetAll(model.QueryOptions{
		Max: 2,
		Filters: And{
			Eq{"media_file.missing": false},
			NotEq{"media_file.tags": ""},
			Expr("exists (select 1 from json_each(media_file.tags, '$.isrc') where lower(json_each.value) = lower(?))", strings.TrimSpace(isrc)),
		},
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func (api *Router) findTrackByName(ctx context.Context, row curatorImportTrack) (*model.MediaFile, error) {
	if row.Artist == "" || row.Album == "" || row.Name == "" {
		return nil, nil
	}
	matches, err := api.ds.MediaFile(ctx).GetAll(model.QueryOptions{
		Max: 2,
		Filters: And{
			Eq{"media_file.missing": false},
			Expr("lower(media_file.artist) = lower(?)", row.Artist),
			Expr("lower(media_file.album) = lower(?)", row.Album),
			Expr("lower(media_file.title) = lower(?)", row.Name),
		},
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func extractSongIDs(rows []responses.CuratorImportRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.SongID != "" {
			ids = append(ids, row.SongID)
		}
	}
	return ids
}

func parseCuratorImportFile(ext string, data []byte) ([]curatorImportTrack, error) {
	if strings.EqualFold(ext, ".xlsx") {
		return parseCuratorXLSX(data)
	}
	return parseCuratorCSV(bytes.NewReader(data))
}

func parseCuratorCSV(reader io.Reader) ([]curatorImportTrack, error) {
	r := csv.NewReader(reader)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := mapHeader(records[0])
	rows := make([]curatorImportTrack, 0, len(records)-1)
	for i, rec := range records[1:] {
		row := importRowFromRecord(i+1, header, rec)
		if row.Artist != "" || row.Album != "" || row.Name != "" || row.ISRC != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func mapHeader(header []string) map[string]int {
	result := map[string]int{}
	for i, h := range header {
		result[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return result
}

func importRowFromRecord(index int, header map[string]int, rec []string) curatorImportTrack {
	value := func(name string) string {
		i, ok := header[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	return curatorImportTrack{
		Index:  index,
		Artist: value("artist"),
		Album:  value("album"),
		Name:   value("name"),
		ISRC:   value("isrc"),
	}
}

func parseCuratorXLSX(data []byte) ([]curatorImportTrack, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	shared, err := readSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}
	sheetName, err := firstWorksheetName(files)
	if err != nil {
		return nil, err
	}
	sheetRows, err := readSheetRows(files[sheetName], shared)
	if err != nil {
		return nil, err
	}
	if len(sheetRows) == 0 {
		return nil, nil
	}
	header := mapHeader(sheetRows[0])
	rows := make([]curatorImportTrack, 0, len(sheetRows)-1)
	for i, rec := range sheetRows[1:] {
		row := importRowFromRecord(i+1, header, rec)
		if row.Artist != "" || row.Album != "" || row.Name != "" || row.ISRC != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func readSharedStrings(f *zip.File) ([]string, error) {
	if f == nil {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var sst struct {
		SI []struct {
			T []string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.NewDecoder(rc).Decode(&sst); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(sst.SI))
	for _, si := range sst.SI {
		var b strings.Builder
		for _, t := range si.T {
			b.WriteString(t)
		}
		for _, r := range si.R {
			b.WriteString(r.T)
		}
		values = append(values, b.String())
	}
	return values, nil
}

func firstWorksheetName(files map[string]*zip.File) (string, error) {
	if files["xl/worksheets/sheet1.xml"] != nil {
		return "xl/worksheets/sheet1.xml", nil
	}
	for name := range files {
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			return name, nil
		}
	}
	return "", model.ErrNotFound
}

func readSheetRows(f *zip.File, shared []string) ([][]string, error) {
	if f == nil {
		return nil, model.ErrNotFound
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var ws struct {
		SheetData struct {
			Rows []struct {
				Cells []struct {
					Ref  string `xml:"r,attr"`
					Type string `xml:"t,attr"`
					V    string `xml:"v"`
					IS   struct {
						T string `xml:"t"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"row"`
		} `xml:"sheetData"`
	}
	if err := xml.NewDecoder(rc).Decode(&ws); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(ws.SheetData.Rows))
	for _, row := range ws.SheetData.Rows {
		values := []string{}
		for i, cell := range row.Cells {
			col := xlsxColumnIndex(cell.Ref)
			if col < 0 {
				col = i
			}
			for len(values) <= col {
				values = append(values, "")
			}
			values[col] = xlsxCellValue(cell.Type, cell.V, cell.IS.T, shared)
		}
		rows = append(rows, values)
	}
	return rows, nil
}

func xlsxColumnIndex(ref string) int {
	result := 0
	seen := false
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		seen = true
		result = result*26 + int(ch-'A'+1)
	}
	if !seen {
		return -1
	}
	return result - 1
}

func xlsxCellValue(cellType, raw, inline string, shared []string) string {
	switch cellType {
	case "s":
		idx, err := strconv.Atoi(raw)
		if err == nil && idx >= 0 && idx < len(shared) {
			return strings.TrimSpace(shared[idx])
		}
	case "inlineStr":
		return strings.TrimSpace(inline)
	}
	return strings.TrimSpace(raw)
}
