package persistence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/deluan/rest"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/id"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func mongoCount(ctx context.Context, c *mongo.Collection, opts ...model.QueryOptions) (int64, error) {
	filter, _, err := mongoQuery(opts...)
	if err != nil {
		return 0, err
	}
	return c.CountDocuments(ctx, filter)
}

func mongoExists(ctx context.Context, c *mongo.Collection, id string) (bool, error) {
	n, err := c.CountDocuments(ctx, bson.M{"id": id})
	return n > 0, err
}

func mongoReplace(ctx context.Context, c *mongo.Collection, id string, doc any) error {
	_, err := c.ReplaceOne(ctx, bson.M{"id": id}, doc, options.Replace().SetUpsert(true))
	return err
}

func mongoOne[T any](ctx context.Context, c *mongo.Collection, filter bson.M) (*T, error) {
	var out T
	err := c.FindOne(ctx, filter).Decode(&out)
	return &out, mongoErr(err)
}

func mongoAll[T any, S ~[]T](ctx context.Context, c *mongo.Collection, filter bson.M, opts ...options.Lister[options.FindOptions]) (S, error) {
	cur, err := c.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := S{}
	for cur.Next(ctx) {
		var item T
		if err := cur.Decode(&item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, cur.Err()
}

func mongoQuery(opts ...model.QueryOptions) (bson.M, *options.FindOptionsBuilder, error) {
	filter := bson.M{}
	findOpts := options.Find()
	if len(opts) == 0 {
		return filter, findOpts, nil
	}
	opt := opts[0]
	if opt.Filters != nil {
		var err error
		filter, err = mongoFilter(opt.Filters)
		if err != nil {
			return nil, nil, err
		}
	}
	if opt.Max > 0 {
		findOpts.SetLimit(int64(opt.Max))
	}
	if opt.Offset > 0 {
		findOpts.SetSkip(int64(opt.Offset))
	}
	if opt.Sort != "" {
		dir := 1
		if strings.EqualFold(opt.Order, "desc") {
			dir = -1
		}
		findOpts.SetSort(bson.D{{Key: mongoField(opt.Sort), Value: dir}})
	}
	return filter, findOpts, nil
}

func restToModelOptions(options ...rest.QueryOptions) []model.QueryOptions {
	if len(options) == 0 {
		return nil
	}
	opt := options[0]
	out := model.QueryOptions{
		Sort:   opt.Sort,
		Order:  opt.Order,
		Max:    opt.Max,
		Offset: opt.Offset,
	}
	if len(opt.Filters) > 0 {
		filters := make(map[string]any, len(opt.Filters))
		for k, v := range opt.Filters {
			switch k {
			case "seed":
				if seed, ok := v.(string); ok {
					out.Seed = seed
				}
				continue
			case "role":
				continue
			}
			filters[k] = v
		}
		if len(filters) > 0 {
			out.Filters = squirrel.Eq(filters)
		}
	}
	return []model.QueryOptions{out}
}

func restRole(options ...rest.QueryOptions) string {
	if len(options) == 0 || len(options[0].Filters) == 0 {
		return ""
	}
	role, _ := options[0].Filters["role"].(string)
	return role
}

func mongoFilter(expr squirrel.Sqlizer) (bson.M, error) {
	switch f := expr.(type) {
	case nil:
		return bson.M{}, nil
	case squirrel.Eq:
		out := bson.M{}
		for k, v := range f {
			field := mongoField(k)
			switch vv := v.(type) {
			case []string:
				out[field] = bson.M{"$in": vv}
			case []int:
				out[field] = bson.M{"$in": vv}
			case []bool:
				out[field] = bson.M{"$in": vv}
			default:
				out[field] = vv
			}
		}
		return out, nil
	case squirrel.NotEq:
		out := bson.M{}
		for k, v := range f {
			out[mongoField(k)] = bson.M{"$ne": v}
		}
		return out, nil
	case squirrel.And:
		var parts []bson.M
		for _, child := range f {
			part, err := mongoFilter(child)
			if err != nil {
				return nil, err
			}
			if len(part) > 0 {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return bson.M{}, nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return bson.M{"$and": parts}, nil
	case squirrel.Or:
		parts := make([]bson.M, 0, len(f))
		for _, child := range f {
			part, err := mongoFilter(child)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return bson.M{"$or": parts}, nil
	default:
		return nil, fmt.Errorf("unsupported Mongo query filter %T", expr)
	}
}

func mongoField(field string) string {
	field = strings.TrimSpace(field)
	if idx := strings.LastIndex(field, "."); idx >= 0 {
		field = field[idx+1:]
	}
	return strings.ReplaceAll(strings.ToLower(field), "_", "")
}

type mongoFolderRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoFolderRepository) c() *mongo.Collection { return r.store.collection("folders") }
func (r *mongoFolderRepository) Get(fid string) (*model.Folder, error) {
	return mongoOne[model.Folder](r.ctx, r.c(), bson.M{"id": fid})
}
func (r *mongoFolderRepository) GetByPath(lib model.Library, path string) (*model.Folder, error) {
	return mongoOne[model.Folder](r.ctx, r.c(), bson.M{"libraryid": lib.ID, "path": path})
}
func (r *mongoFolderRepository) GetAll(opts ...model.QueryOptions) ([]model.Folder, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	return mongoAll[model.Folder, []model.Folder](r.ctx, r.c(), filter, findOpts)
}
func (r *mongoFolderRepository) CountAll(opts ...model.QueryOptions) (int64, error) {
	return mongoCount(r.ctx, r.c(), opts...)
}
func (r *mongoFolderRepository) GetFolderUpdateInfo(lib model.Library, targetPaths ...string) (map[string]model.FolderUpdateInfo, error) {
	filter := bson.M{"libraryid": lib.ID}
	if len(targetPaths) > 0 {
		ids := make([]string, 0, len(targetPaths))
		for _, path := range targetPaths {
			ids = append(ids, model.FolderID(lib, path))
		}
		filter["id"] = bson.M{"$in": ids}
	}
	cur, err := r.c().Find(r.ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(r.ctx)
	out := map[string]model.FolderUpdateInfo{}
	for cur.Next(r.ctx) {
		var f model.Folder
		if err := cur.Decode(&f); err != nil {
			return nil, err
		}
		out[f.ID] = model.FolderUpdateInfo{UpdatedAt: f.UpdateAt, Hash: f.Hash}
	}
	return out, cur.Err()
}
func (r *mongoFolderRepository) Put(f *model.Folder) error {
	return mongoReplace(r.ctx, r.c(), f.ID, f)
}
func (r *mongoFolderRepository) MarkMissing(missing bool, ids ...string) error {
	_, err := r.c().UpdateMany(r.ctx, bson.M{"id": bson.M{"$in": ids}}, bson.M{"$set": bson.M{"missing": missing}})
	return err
}
func (r *mongoFolderRepository) GetTouchedWithPlaylists() (model.FolderCursor, error) {
	cur, err := r.c().Find(r.ctx, bson.M{"numplaylists": bson.M{"$gt": 0}, "missing": false})
	if err != nil {
		return nil, err
	}
	return model.FolderCursor(func(yield func(model.Folder, error) bool) {
		defer cur.Close(r.ctx)
		for cur.Next(r.ctx) {
			var f model.Folder
			err := cur.Decode(&f)
			if !yield(f, err) || err != nil {
				return
			}
		}
		if err := cur.Err(); err != nil {
			yield(model.Folder{}, err)
		}
	}), nil
}

type mongoAlbumRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoAlbumRepository) c() *mongo.Collection { return r.store.collection("albums") }
func (r *mongoAlbumRepository) CountAll(opts ...model.QueryOptions) (int64, error) {
	return mongoCount(r.ctx, r.c(), opts...)
}
func (r *mongoAlbumRepository) Count(options ...rest.QueryOptions) (int64, error) {
	return r.CountAll(restToModelOptions(options...)...)
}
func (r *mongoAlbumRepository) Exists(id string) (bool, error) { return mongoExists(r.ctx, r.c(), id) }
func (r *mongoAlbumRepository) Put(a *model.Album) error       { return mongoReplace(r.ctx, r.c(), a.ID, a) }
func (r *mongoAlbumRepository) UpdateExternalInfo(a *model.Album) error {
	return mongoReplace(r.ctx, r.c(), a.ID, a)
}
func (r *mongoAlbumRepository) Get(id string) (*model.Album, error) {
	return mongoOne[model.Album](r.ctx, r.c(), bson.M{"id": id})
}
func (r *mongoAlbumRepository) Read(id string) (any, error) { return r.Get(id) }
func (r *mongoAlbumRepository) GetAll(opts ...model.QueryOptions) (model.Albums, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	return mongoAll[model.Album, model.Albums](r.ctx, r.c(), filter, findOpts)
}
func (r *mongoAlbumRepository) ReadAll(options ...rest.QueryOptions) (any, error) {
	return r.GetAll(restToModelOptions(options...)...)
}
func (*mongoAlbumRepository) EntityName() string { return "album" }
func (*mongoAlbumRepository) NewInstance() any   { return &model.Album{} }
func (r *mongoAlbumRepository) Touch(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.c().UpdateMany(r.ctx, bson.M{"id": bson.M{"$in": ids}}, bson.M{"$set": bson.M{"updatedat": time.Now()}})
	return err
}
func (r *mongoAlbumRepository) TouchByMissingFolder() (int64, error) {
	res, err := r.c().UpdateMany(r.ctx, bson.M{}, bson.M{"$set": bson.M{"updatedat": time.Now()}})
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}
func (r *mongoAlbumRepository) GetTouchedAlbums(libID int) (model.AlbumCursor, error) {
	cur, err := r.c().Find(r.ctx, bson.M{"libraryid": libID})
	if err != nil {
		return nil, err
	}
	return model.AlbumCursor(func(yield func(model.Album, error) bool) {
		defer cur.Close(r.ctx)
		for cur.Next(r.ctx) {
			var a model.Album
			err := cur.Decode(&a)
			if !yield(a, err) || err != nil {
				return
			}
		}
		if err := cur.Err(); err != nil {
			yield(model.Album{}, err)
		}
	}), nil
}
func (*mongoAlbumRepository) RefreshPlayCounts() (int64, error)              { return 0, nil }
func (*mongoAlbumRepository) CopyAttributes(string, string, ...string) error { return nil }
func (*mongoAlbumRepository) IncPlayCount(string, time.Time) error           { return nil }
func (*mongoAlbumRepository) SetStar(bool, ...string) error                  { return nil }
func (*mongoAlbumRepository) SetRating(int, string) error                    { return nil }
func (*mongoAlbumRepository) ReassignAnnotation(string, string) error        { return nil }
func (*mongoAlbumRepository) Search(string, ...model.QueryOptions) (model.Albums, error) {
	return nil, nil
}

type mongoArtistRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoArtistRepository) c() *mongo.Collection { return r.store.collection("artists") }
func (r *mongoArtistRepository) CountAll(opts ...model.QueryOptions) (int64, error) {
	return mongoCount(r.ctx, r.c(), opts...)
}
func (r *mongoArtistRepository) Count(options ...rest.QueryOptions) (int64, error) {
	role := restRole(options...)
	if role == "" {
		return r.CountAll(restToModelOptions(options...)...)
	}
	filter, _, err := mongoQuery(restToModelOptions(options...)...)
	if err != nil {
		return 0, err
	}
	filter, err = r.addRoleFilter(filter, role)
	if err != nil {
		return 0, err
	}
	return r.c().CountDocuments(r.ctx, filter)
}
func (r *mongoArtistRepository) Exists(id string) (bool, error) { return mongoExists(r.ctx, r.c(), id) }
func (r *mongoArtistRepository) Put(a *model.Artist, _ ...string) error {
	return mongoReplace(r.ctx, r.c(), a.ID, a)
}
func (r *mongoArtistRepository) UpdateExternalInfo(a *model.Artist) error {
	return mongoReplace(r.ctx, r.c(), a.ID, a)
}
func (r *mongoArtistRepository) Get(id string) (*model.Artist, error) {
	return mongoOne[model.Artist](r.ctx, r.c(), bson.M{"id": id})
}
func (r *mongoArtistRepository) Read(id string) (any, error) { return r.Get(id) }
func (r *mongoArtistRepository) GetAll(opts ...model.QueryOptions) (model.Artists, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	return mongoAll[model.Artist, model.Artists](r.ctx, r.c(), filter, findOpts)
}
func (r *mongoArtistRepository) ReadAll(options ...rest.QueryOptions) (any, error) {
	role := restRole(options...)
	if role == "" {
		return r.GetAll(restToModelOptions(options...)...)
	}
	filter, findOpts, err := mongoQuery(restToModelOptions(options...)...)
	if err != nil {
		return nil, err
	}
	filter, err = r.addRoleFilter(filter, role)
	if err != nil {
		return nil, err
	}
	return mongoAll[model.Artist, model.Artists](r.ctx, r.c(), filter, findOpts)
}
func (*mongoArtistRepository) EntityName() string { return "artist" }
func (*mongoArtistRepository) NewInstance() any   { return &model.Artist{} }
func (r *mongoArtistRepository) addRoleFilter(filter bson.M, role string) (bson.M, error) {
	ids, err := r.artistIDsForRole(role)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return bson.M{"id": bson.M{"$in": []string{}}}, nil
	}
	if len(filter) == 0 {
		return bson.M{"id": bson.M{"$in": ids}}, nil
	}
	return bson.M{"$and": []bson.M{filter, {"id": bson.M{"$in": ids}}}}, nil
}
func (r *mongoArtistRepository) artistIDsForRole(role string) ([]string, error) {
	field := "participants." + role + ".artist.id"
	if role == model.RoleMainCredit.String() {
		return r.artistIDsForRoles(model.RoleAlbumArtist.String(), model.RoleArtist.String())
	}
	return r.distinctArtistIDs(field)
}
func (r *mongoArtistRepository) artistIDsForRoles(roles ...string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, role := range roles {
		ids, err := r.artistIDsForRole(role)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}
func (r *mongoArtistRepository) distinctArtistIDs(field string) ([]string, error) {
	res := r.store.collection("media_files").Distinct(r.ctx, field, bson.M{"missing": false})
	if err := res.Err(); err != nil {
		return nil, err
	}
	var ids []string
	if err := res.Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}
func (*mongoArtistRepository) GetIndex(bool, []int, ...model.Role) (model.ArtistIndexes, error) {
	return nil, nil
}
func (*mongoArtistRepository) RefreshPlayCounts() (int64, error)       { return 0, nil }
func (*mongoArtistRepository) RefreshStats(bool) (int64, error)        { return 0, nil }
func (*mongoArtistRepository) IncPlayCount(string, time.Time) error    { return nil }
func (*mongoArtistRepository) SetStar(bool, ...string) error           { return nil }
func (*mongoArtistRepository) SetRating(int, string) error             { return nil }
func (*mongoArtistRepository) ReassignAnnotation(string, string) error { return nil }
func (*mongoArtistRepository) Search(string, ...model.QueryOptions) (model.Artists, error) {
	return nil, nil
}

type mongoMediaFileRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoMediaFileRepository) c() *mongo.Collection { return r.store.collection("media_files") }
func (r *mongoMediaFileRepository) CountAll(opts ...model.QueryOptions) (int64, error) {
	return mongoCount(r.ctx, r.c(), opts...)
}
func (r *mongoMediaFileRepository) Count(options ...rest.QueryOptions) (int64, error) {
	return r.CountAll(restToModelOptions(options...)...)
}
func (*mongoMediaFileRepository) CountBySuffix(...model.QueryOptions) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (r *mongoMediaFileRepository) Exists(id string) (bool, error) {
	return mongoExists(r.ctx, r.c(), id)
}
func (r *mongoMediaFileRepository) Put(m *model.MediaFile) error {
	// Assign ID from PID if not already set. PID is the persistent identifier
	// derived from tags; ID is the stable DB key used for upserts.
	if m.ID == "" {
		if m.PID != "" {
			m.ID = m.PID
		} else {
			m.ID = id.NewRandom()
		}
	}
	return mongoReplace(r.ctx, r.c(), m.ID, m)
}
func (r *mongoMediaFileRepository) UpdateProbeData(id, data string) error {
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": id}, bson.M{"$set": bson.M{"probedata": data}})
	return err
}
func (r *mongoMediaFileRepository) Get(id string) (*model.MediaFile, error) {
	return mongoOne[model.MediaFile](r.ctx, r.c(), bson.M{"id": id})
}
func (r *mongoMediaFileRepository) Read(id string) (any, error) { return r.Get(id) }
func (r *mongoMediaFileRepository) GetWithParticipants(id string) (*model.MediaFile, error) {
	return r.Get(id)
}
func (r *mongoMediaFileRepository) GetAll(opts ...model.QueryOptions) (model.MediaFiles, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	return mongoAll[model.MediaFile, model.MediaFiles](r.ctx, r.c(), filter, findOpts)
}
func (r *mongoMediaFileRepository) ReadAll(options ...rest.QueryOptions) (any, error) {
	return r.GetAll(restToModelOptions(options...)...)
}
func (*mongoMediaFileRepository) EntityName() string { return "song" }
func (*mongoMediaFileRepository) NewInstance() any   { return &model.MediaFile{} }
func (*mongoMediaFileRepository) GetAllByTags(model.TagName, []string, ...model.QueryOptions) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoMediaFileRepository) GetTopPlayedByArtist(string, int) (model.MediaFiles, error) {
	return nil, nil
}
func (r *mongoMediaFileRepository) GetCursor(opts ...model.QueryOptions) (model.MediaFileCursor, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	cur, err := r.c().Find(r.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	return model.MediaFileCursor(func(yield func(model.MediaFile, error) bool) {
		defer cur.Close(r.ctx)
		for cur.Next(r.ctx) {
			var mf model.MediaFile
			err := cur.Decode(&mf)
			if !yield(mf, err) || err != nil {
				return
			}
		}
		if err := cur.Err(); err != nil {
			yield(model.MediaFile{}, err)
		}
	}), nil
}
func (r *mongoMediaFileRepository) Delete(id string) error {
	_, err := r.c().DeleteOne(r.ctx, bson.M{"id": id})
	return err
}
func (r *mongoMediaFileRepository) DeleteMissing(ids []string) error {
	filter := bson.M{"missing": true}
	if len(ids) > 0 {
		filter["id"] = bson.M{"$in": ids}
	}
	_, err := r.c().DeleteMany(r.ctx, filter)
	return err
}
func (r *mongoMediaFileRepository) DeleteAllMissing() (int64, error) {
	res, err := r.c().DeleteMany(r.ctx, bson.M{"missing": true})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}
func (r *mongoMediaFileRepository) FindByPaths(paths []string) (model.MediaFiles, error) {
	if len(paths) == 0 {
		return model.MediaFiles{}, nil
	}
	return mongoAll[model.MediaFile, model.MediaFiles](r.ctx, r.c(), bson.M{"path": bson.M{"$in": paths}})
}
func (r *mongoMediaFileRepository) MarkMissing(missing bool, mfs ...*model.MediaFile) error {
	if len(mfs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mfs))
	for _, mf := range mfs {
		if mf != nil {
			ids = append(ids, mf.ID)
		}
	}
	_, err := r.c().UpdateMany(r.ctx, bson.M{"id": bson.M{"$in": ids}}, bson.M{"$set": bson.M{"missing": missing}})
	return err
}
func (r *mongoMediaFileRepository) MarkMissingByFolder(missing bool, folderIDs ...string) error {
	if len(folderIDs) == 0 {
		return nil
	}
	_, err := r.c().UpdateMany(r.ctx, bson.M{"folderid": bson.M{"$in": folderIDs}}, bson.M{"$set": bson.M{"missing": missing}})
	return err
}
func (r *mongoMediaFileRepository) GetMissingAndMatching(libID int) (model.MediaFileCursor, error) {
	cur, err := r.c().Find(r.ctx, bson.M{"libraryid": libID, "$or": []bson.M{{"missing": true}, {"missing": false}}})
	if err != nil {
		return nil, err
	}
	return model.MediaFileCursor(func(yield func(model.MediaFile, error) bool) {
		defer cur.Close(r.ctx)
		for cur.Next(r.ctx) {
			var mf model.MediaFile
			err := cur.Decode(&mf)
			if !yield(mf, err) || err != nil {
				return
			}
		}
		if err := cur.Err(); err != nil {
			yield(model.MediaFile{}, err)
		}
	}), nil
}
func (*mongoMediaFileRepository) FindRecentFilesByMBZTrackID(model.MediaFile, time.Time) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoMediaFileRepository) FindRecentFilesByProperties(model.MediaFile, time.Time) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoMediaFileRepository) IncPlayCount(string, time.Time) error    { return nil }
func (*mongoMediaFileRepository) SetStar(bool, ...string) error           { return nil }
func (*mongoMediaFileRepository) SetRating(int, string) error             { return nil }
func (*mongoMediaFileRepository) ReassignAnnotation(string, string) error { return nil }
func (*mongoMediaFileRepository) AddBookmark(string, string, int64) error { return nil }
func (*mongoMediaFileRepository) DeleteBookmark(string) error             { return nil }
func (*mongoMediaFileRepository) GetBookmarks() (model.Bookmarks, error)  { return nil, nil }
func (*mongoMediaFileRepository) Search(string, ...model.QueryOptions) (model.MediaFiles, error) {
	return nil, nil
}

type mongoPlaylistRepository struct {
	mongoResourceRepository
	ctx   context.Context
	store *MongoStore
}

func (*mongoPlaylistRepository) CountAll(...model.QueryOptions) (int64, error) { return 0, nil }
func (*mongoPlaylistRepository) Exists(string) (bool, error)                   { return false, nil }
func (*mongoPlaylistRepository) Put(*model.Playlist) error                     { return nil }
func (*mongoPlaylistRepository) Get(string) (*model.Playlist, error)           { return nil, model.ErrNotFound }
func (*mongoPlaylistRepository) GetWithTracks(string, bool, bool) (*model.Playlist, error) {
	return nil, model.ErrNotFound
}
func (*mongoPlaylistRepository) GetAll(...model.QueryOptions) (model.Playlists, error) {
	return nil, nil
}
func (*mongoPlaylistRepository) FindByPath(string) (*model.Playlist, error) {
	return nil, model.ErrNotFound
}
func (*mongoPlaylistRepository) Delete(string) error { return nil }
func (*mongoPlaylistRepository) Tracks(string, bool) model.PlaylistTrackRepository {
	return &mongoPlaylistTrackRepository{}
}
func (*mongoPlaylistRepository) GetPlaylists(string) (model.Playlists, error) { return nil, nil }

type mongoPlaylistTrackRepository struct{ mongoResourceRepository }

func (*mongoPlaylistTrackRepository) GetAll(...model.QueryOptions) (model.PlaylistTracks, error) {
	return nil, nil
}
func (*mongoPlaylistTrackRepository) GetAlbumIDs(...model.QueryOptions) ([]string, error) {
	return nil, nil
}
func (*mongoPlaylistTrackRepository) Add([]string) (int, error)            { return 0, nil }
func (*mongoPlaylistTrackRepository) AddAlbums([]string) (int, error)      { return 0, nil }
func (*mongoPlaylistTrackRepository) AddArtists([]string) (int, error)     { return 0, nil }
func (*mongoPlaylistTrackRepository) AddDiscs([]model.DiscID) (int, error) { return 0, nil }
func (*mongoPlaylistTrackRepository) Delete(...string) error               { return nil }
func (*mongoPlaylistTrackRepository) DeleteAll() error                     { return nil }
func (*mongoPlaylistTrackRepository) Reorder(int, int) error               { return nil }

type mongoPlayQueueRepository struct{ ctx context.Context }

func (*mongoPlayQueueRepository) Store(*model.PlayQueue, ...string) error { return nil }
func (*mongoPlayQueueRepository) Retrieve(string) (*model.PlayQueue, error) {
	return nil, model.ErrNotFound
}
func (*mongoPlayQueueRepository) RetrieveWithMediaFiles(string) (*model.PlayQueue, error) {
	return nil, model.ErrNotFound
}
func (*mongoPlayQueueRepository) Clear(string) error { return nil }

type mongoRadioRepository struct {
	mongoResourceRepository
	ctx   context.Context
	store *MongoStore
}

func (*mongoRadioRepository) CountAll(...model.QueryOptions) (int64, error)      { return 0, nil }
func (*mongoRadioRepository) Delete(string) error                                { return nil }
func (*mongoRadioRepository) Get(string) (*model.Radio, error)                   { return nil, model.ErrNotFound }
func (*mongoRadioRepository) GetAll(...model.QueryOptions) (model.Radios, error) { return nil, nil }
func (*mongoRadioRepository) Put(*model.Radio, ...string) error                  { return nil }

type mongoShareRepository struct{ ctx context.Context }

func (*mongoShareRepository) Exists(string) (bool, error)                        { return false, nil }
func (*mongoShareRepository) Get(string) (*model.Share, error)                   { return nil, model.ErrNotFound }
func (*mongoShareRepository) GetAll(...model.QueryOptions) (model.Shares, error) { return nil, nil }
func (*mongoShareRepository) CountAll(...model.QueryOptions) (int64, error)      { return 0, nil }

type mongoPluginRepository struct {
	mongoResourceRepository
	ctx   context.Context
	store *MongoStore
}

func (*mongoPluginRepository) ClearErrors() error                                  { return nil }
func (*mongoPluginRepository) CountAll(...model.QueryOptions) (int64, error)       { return 0, nil }
func (*mongoPluginRepository) Delete(string) error                                 { return nil }
func (*mongoPluginRepository) Get(string) (*model.Plugin, error)                   { return nil, model.ErrNotFound }
func (*mongoPluginRepository) GetAll(...model.QueryOptions) (model.Plugins, error) { return nil, nil }
func (*mongoPluginRepository) Put(*model.Plugin) error                             { return nil }

var _ rest.Repository = (*mongoUserRepository)(nil)
