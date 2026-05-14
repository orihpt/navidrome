package persistence

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
	err := c.FindOne(ctx, bson.M{"id": id}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, err
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

func mongoSearch[T any, S ~[]T](ctx context.Context, c *mongo.Collection, q string, fields []string, opts ...model.QueryOptions) (S, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}

	q = strings.TrimSpace(q)
	if q == "" || q == `""` {
		return mongoAll[T, S](ctx, c, filter, findOpts)
	}

	terms := strings.Fields(q)
	if len(terms) == 0 {
		terms = []string{q}
	}

	termFilters := make([]bson.M, 0, len(terms))
	for _, term := range terms {
		regex := bson.M{"$regex": regexp.QuoteMeta(term), "$options": "i"}
		fieldFilters := make([]bson.M, 0, len(fields))
		for _, field := range fields {
			fieldFilters = append(fieldFilters, bson.M{field: regex})
		}
		termFilters = append(termFilters, bson.M{"$or": fieldFilters})
	}

	searchFilter := bson.M{}
	if len(termFilters) == 1 {
		searchFilter = termFilters[0]
	} else {
		searchFilter = bson.M{"$and": termFilters}
	}

	if len(filter) == 0 {
		filter = searchFilter
	} else {
		filter = bson.M{"$and": []bson.M{filter, searchFilter}}
	}

	return mongoAll[T, S](ctx, c, filter, findOpts)
}

func mongoFilter(expr squirrel.Sqlizer) (bson.M, error) {
	switch f := expr.(type) {
	case nil:
		return bson.M{}, nil
	case bsonFilter:
		return bson.M(f), nil
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

type bsonFilter bson.M

func (f bsonFilter) ToSql() (string, []any, error) {
	return "", nil, fmt.Errorf("bsonFilter cannot be converted to SQL")
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
func (r *mongoAlbumRepository) Search(q string, opts ...model.QueryOptions) (model.Albums, error) {
	return mongoSearch[model.Album, model.Albums](r.ctx, r.c(), q, []string{
		"id",
		"name",
		"orderalbumname",
		"albumartist",
		"orderalbumartistname",
		"mbzalbumid",
		"mbzreleasegroupid",
	}, opts...)
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
func (r *mongoArtistRepository) GetIndex(includeMissing bool, libraryIds []int, roles ...model.Role) (model.ArtistIndexes, error) {
	opts := model.QueryOptions{Sort: "name"}
	filter := bson.M{}
	if !includeMissing {
		filter["missing"] = false
	}
	if len(libraryIds) > 0 {
		filter["libraryid"] = bson.M{"$in": libraryIds}
	}
	// Note: Role filtering for GetIndex is simplified here
	opts.Filters = bsonFilter(filter)

	artists, err := r.GetAll(opts)
	if err != nil {
		return nil, err
	}

	indexes := model.ArtistIndexes{}
	currentIndex := model.ArtistIndex{}
	for _, a := range artists {
		firstChar := "#"
		if len(a.Name) > 0 {
			// Basic first character grouping
			firstChar = strings.ToUpper(a.Name[:1])
			if firstChar[0] < 'A' || firstChar[0] > 'Z' {
				firstChar = "#"
			}
		}
		if currentIndex.ID != firstChar {
			if currentIndex.ID != "" {
				indexes = append(indexes, currentIndex)
			}
			currentIndex = model.ArtistIndex{ID: firstChar}
		}
		currentIndex.Artists = append(currentIndex.Artists, a)
	}
	if currentIndex.ID != "" {
		indexes = append(indexes, currentIndex)
	}
	return indexes, nil
}
func (*mongoArtistRepository) RefreshPlayCounts() (int64, error) { return 0, nil }
func (r *mongoArtistRepository) RefreshStats(allArtists bool) (int64, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"missing": false}}},
		{{Key: "$group", Value: bson.M{
			"_id":        "$artistid",
			"songCount":  bson.M{"$sum": 1},
			"albumCount": bson.M{"$addToSet": "$albumid"},
			"size":       bson.M{"$sum": "$size"},
		}}},
	}

	cursor, err := r.store.collection("media_files").Aggregate(r.ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(r.ctx)

	var count int64
	for cursor.Next(r.ctx) {
		var result struct {
			ID         string   `bson:"_id"`
			SongCount  int      `bson:"songCount"`
			AlbumCount []string `bson:"albumCount"`
			Size       int64    `bson:"size"`
		}
		if err := cursor.Decode(&result); err != nil {
			return count, err
		}

		if result.ID == "" {
			continue
		}

		stats := map[model.Role]model.ArtistStats{
			model.RoleAlbumArtist: {
				SongCount:  result.SongCount,
				AlbumCount: len(result.AlbumCount),
				Size:       result.Size,
			},
		}

		_, err := r.c().UpdateOne(r.ctx, bson.M{"id": result.ID}, bson.M{
			"$set": bson.M{
				"stats":      stats,
				"songcount":  result.SongCount,
				"albumcount": len(result.AlbumCount),
				"size":       result.Size,
				"updatedat":  time.Now(),
			},
		})
		if err == nil {
			count++
		}
	}
	return count, cursor.Err()
}
func (*mongoArtistRepository) IncPlayCount(string, time.Time) error    { return nil }
func (*mongoArtistRepository) SetStar(bool, ...string) error           { return nil }
func (*mongoArtistRepository) SetRating(int, string) error             { return nil }
func (*mongoArtistRepository) ReassignAnnotation(string, string) error { return nil }
func (r *mongoArtistRepository) Search(q string, opts ...model.QueryOptions) (model.Artists, error) {
	return mongoSearch[model.Artist, model.Artists](r.ctx, r.c(), q, []string{
		"id",
		"name",
		"orderartistname",
		"sortartistname",
		"mbzartistid",
	}, opts...)
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
func (r *mongoMediaFileRepository) Search(q string, opts ...model.QueryOptions) (model.MediaFiles, error) {
	return mongoSearch[model.MediaFile, model.MediaFiles](r.ctx, r.c(), q, []string{
		"id",
		"title",
		"ordertitle",
		"album",
		"orderalbumname",
		"artist",
		"orderartistname",
		"albumartist",
		"orderalbumartistname",
		"mbzrecordingid",
		"mbzreleasetrackid",
	}, opts...)
}

type mongoPlaylistRepository struct {
	mongoResourceRepository
	ctx   context.Context
	store *MongoStore
}

func (r *mongoPlaylistRepository) c() *mongo.Collection { return r.store.collection("playlists") }
func (r *mongoPlaylistRepository) CountAll(opts ...model.QueryOptions) (int64, error) {
	return mongoCount(r.ctx, r.c(), opts...)
}
func (r *mongoPlaylistRepository) Exists(id string) (bool, error) {
	return mongoExists(r.ctx, r.c(), id)
}
func (r *mongoPlaylistRepository) Put(pls *model.Playlist) error {
	if pls.ID == "" {
		pls.ID = id.NewRandom()
	}
	if pls.CreatedAt.IsZero() {
		pls.CreatedAt = time.Now()
	}
	pls.UpdatedAt = time.Now()
	pls.NormalizeVisibility()
	doc := *pls
	doc.Tracks = nil
	if err := mongoReplace(r.ctx, r.c(), pls.ID, &doc); err != nil {
		return err
	}
	tracks := r.store.collection("playlist_tracks")
	if len(pls.Tracks) == 0 {
		_, err := tracks.DeleteMany(r.ctx, bson.M{"playlistid": pls.ID})
		return err
	}
	if _, err := tracks.DeleteMany(r.ctx, bson.M{"playlistid": pls.ID}); err != nil {
		return err
	}
	docs := make([]any, 0, len(pls.Tracks))
	for i, track := range pls.Tracks {
		if track.MediaFileID == "" {
			track.MediaFileID = track.MediaFile.ID
		}
		docs = append(docs, bson.M{
			"id":          fmt.Sprintf("%d", i+1),
			"position":    i + 1,
			"playlistid":  pls.ID,
			"mediafileid": track.MediaFileID,
		})
	}
	_, err := tracks.InsertMany(r.ctx, docs)
	return err
}
func (r *mongoPlaylistRepository) Get(id string) (*model.Playlist, error) {
	return mongoOne[model.Playlist](r.ctx, r.c(), bson.M{"id": id})
}
func (r *mongoPlaylistRepository) GetWithTracks(id string, _ bool, includeMissing bool) (*model.Playlist, error) {
	pls, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	filter := squirrel.Eq{"playlist_id": id}
	if includeMissing {
		filter = squirrel.Eq{"playlist_id": id}
	}
	tracks, err := r.Tracks(id, false).GetAll(model.QueryOptions{Sort: "id", Filters: filter})
	if err != nil {
		return nil, err
	}
	pls.SetTracks(tracks)
	return pls, nil
}
func (r *mongoPlaylistRepository) GetAll(opts ...model.QueryOptions) (model.Playlists, error) {
	filter, findOpts, err := mongoQuery(opts...)
	if err != nil {
		return nil, err
	}
	playlists, err := mongoAll[model.Playlist, model.Playlists](r.ctx, r.c(), filter, findOpts)
	if err != nil {
		return nil, err
	}
	for i := range playlists {
		playlists[i].NormalizeVisibility()
	}
	return playlists, nil
}
func (r *mongoPlaylistRepository) FindByPath(path string) (*model.Playlist, error) {
	return mongoOne[model.Playlist](r.ctx, r.c(), bson.M{"path": path})
}
func (r *mongoPlaylistRepository) Delete(id string) error {
	if _, err := r.store.collection("playlist_tracks").DeleteMany(r.ctx, bson.M{"playlistid": id}); err != nil {
		return err
	}
	_, err := r.c().DeleteOne(r.ctx, bson.M{"id": id})
	return err
}
func (r *mongoPlaylistRepository) IncPlayCount(id string) error {
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": id}, bson.M{"$inc": bson.M{"playcount": 1}})
	return err
}
func (r *mongoPlaylistRepository) Tracks(playlistID string, _ bool) model.PlaylistTrackRepository {
	return &mongoPlaylistTrackRepository{ctx: r.ctx, store: r.store, playlistID: playlistID}
}
func (r *mongoPlaylistRepository) GetPlaylists(mediaFileID string) (model.Playlists, error) {
	ids := r.store.collection("playlist_tracks").Distinct(r.ctx, "playlistid", bson.M{"mediafileid": mediaFileID})
	if err := ids.Err(); err != nil {
		return nil, err
	}
	var playlistIDs []string
	if err := ids.Decode(&playlistIDs); err != nil {
		return nil, err
	}
	if len(playlistIDs) == 0 {
		return model.Playlists{}, nil
	}
	return r.GetAll(model.QueryOptions{Filters: bsonFilter{"id": bson.M{"$in": playlistIDs}}})
}
func (r *mongoPlaylistRepository) Count(options ...rest.QueryOptions) (int64, error) {
	return r.CountAll(restToModelOptions(options...)...)
}
func (r *mongoPlaylistRepository) Read(id string) (any, error) { return r.Get(id) }
func (r *mongoPlaylistRepository) ReadAll(options ...rest.QueryOptions) (any, error) {
	return r.GetAll(restToModelOptions(options...)...)
}
func (*mongoPlaylistRepository) EntityName() string { return "playlist" }
func (*mongoPlaylistRepository) NewInstance() any   { return &model.Playlist{} }
func (r *mongoPlaylistRepository) Save(entity any) (string, error) {
	pls := entity.(*model.Playlist)
	pls.ID = ""
	if err := r.Put(pls); err != nil {
		return "", err
	}
	return pls.ID, nil
}
func (r *mongoPlaylistRepository) Update(id string, entity any, _ ...string) error {
	pls := entity.(*model.Playlist)
	pls.ID = id
	return r.Put(pls)
}

type mongoPlaylistTrackRepository struct {
	mongoResourceRepository
	ctx        context.Context
	store      *MongoStore
	playlistID string
}

func (r *mongoPlaylistTrackRepository) c() *mongo.Collection {
	return r.store.collection("playlist_tracks")
}
func (r *mongoPlaylistTrackRepository) GetAll(opts ...model.QueryOptions) (model.PlaylistTracks, error) {
	filter := bson.M{"playlistid": r.playlistID}
	if len(opts) > 0 && opts[0].Filters != nil {
		extra, err := mongoFilter(opts[0].Filters)
		if err != nil {
			return nil, err
		}
		filter = bson.M{"$and": []bson.M{filter, extra}}
	}
	findOpts := options.Find().SetSort(bson.D{{Key: "position", Value: 1}, {Key: "id", Value: 1}})
	cur, err := r.c().Find(r.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(r.ctx)
	var tracks model.PlaylistTracks
	var mediaIDs []string
	for cur.Next(r.ctx) {
		var track model.PlaylistTrack
		if err := cur.Decode(&track); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
		mediaIDs = append(mediaIDs, track.MediaFileID)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	sortPlaylistTracks(tracks)
	if len(mediaIDs) == 0 {
		return tracks, nil
	}
	mfs, err := (&mongoMediaFileRepository{ctx: r.ctx, store: r.store}).GetAll(model.QueryOptions{Filters: bsonFilter{"id": bson.M{"$in": mediaIDs}}})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.MediaFile, len(mfs))
	for _, mf := range mfs {
		byID[mf.ID] = mf
	}
	for i := range tracks {
		if mf, ok := byID[tracks[i].MediaFileID]; ok {
			tracks[i].MediaFile = mf
		}
	}
	return tracks, nil
}

func sortPlaylistTracks(tracks model.PlaylistTracks) {
	sort.SliceStable(tracks, func(i, j int) bool {
		left, leftErr := strconv.Atoi(tracks[i].ID)
		right, rightErr := strconv.Atoi(tracks[j].ID)
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return tracks[i].ID < tracks[j].ID
	})
}
func (r *mongoPlaylistTrackRepository) GetAlbumIDs(opts ...model.QueryOptions) ([]string, error) {
	tracks, err := r.GetAll(opts...)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var ids []string
	for _, track := range tracks {
		if track.AlbumID == "" {
			continue
		}
		if _, ok := seen[track.AlbumID]; ok {
			continue
		}
		seen[track.AlbumID] = struct{}{}
		ids = append(ids, track.AlbumID)
	}
	return ids, nil
}
func (r *mongoPlaylistTrackRepository) Add(mediaFileIDs []string) (int, error) {
	if len(mediaFileIDs) == 0 {
		return 0, nil
	}
	count, err := r.c().CountDocuments(r.ctx, bson.M{"playlistid": r.playlistID})
	if err != nil {
		return 0, err
	}
	docs := make([]any, 0, len(mediaFileIDs))
	for i, mediaFileID := range mediaFileIDs {
		pos := count + int64(i) + 1
		docs = append(docs, bson.M{
			"id":          fmt.Sprintf("%d", pos),
			"position":    pos,
			"playlistid":  r.playlistID,
			"mediafileid": mediaFileID,
		})
	}
	_, err = r.c().InsertMany(r.ctx, docs)
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}
func (r *mongoPlaylistTrackRepository) AddAlbums(albumIDs []string) (int, error) {
	mfs, err := (&mongoMediaFileRepository{ctx: r.ctx, store: r.store}).GetAll(model.QueryOptions{Filters: bsonFilter{"albumid": bson.M{"$in": albumIDs}}})
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(mfs))
	for _, mf := range mfs {
		ids = append(ids, mf.ID)
	}
	return r.Add(ids)
}
func (r *mongoPlaylistTrackRepository) AddArtists(artistIDs []string) (int, error) {
	mfs, err := (&mongoMediaFileRepository{ctx: r.ctx, store: r.store}).GetAll(model.QueryOptions{Filters: bsonFilter{"artistid": bson.M{"$in": artistIDs}}})
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(mfs))
	for _, mf := range mfs {
		ids = append(ids, mf.ID)
	}
	return r.Add(ids)
}
func (*mongoPlaylistTrackRepository) AddDiscs([]model.DiscID) (int, error) { return 0, nil }
func (r *mongoPlaylistTrackRepository) Delete(ids ...string) error {
	filter := bson.M{"playlistid": r.playlistID}
	if len(ids) > 0 {
		filter["id"] = bson.M{"$in": ids}
	}
	_, err := r.c().DeleteMany(r.ctx, filter)
	return err
}
func (r *mongoPlaylistTrackRepository) DeleteAll() error { return r.Delete() }
func (*mongoPlaylistTrackRepository) Reorder(int, int) error {
	return notImplemented("playlistTrack.Reorder")
}

type mongoPlayQueueRepository struct {
	ctx   context.Context
	store *MongoStore
}

type mongoPlayQueueDocument struct {
	ID        string    `bson:"id"`
	UserID    string    `bson:"userid"`
	Current   int       `bson:"current"`
	Position  int64     `bson:"position"`
	ChangedBy string    `bson:"changedby"`
	ItemIDs   []string  `bson:"itemids"`
	CreatedAt time.Time `bson:"createdat"`
	UpdatedAt time.Time `bson:"updatedat"`
}

func (r *mongoPlayQueueRepository) c() *mongo.Collection { return r.store.collection("play_queues") }

func (r *mongoPlayQueueRepository) Store(queue *model.PlayQueue, _ ...string) error {
	if queue == nil || strings.TrimSpace(queue.UserID) == "" {
		return model.ErrValidation
	}
	now := time.Now()
	if queue.CreatedAt.IsZero() {
		queue.CreatedAt = now
	}
	queue.UpdatedAt = now
	itemIDs := make([]string, 0, len(queue.Items))
	for _, item := range queue.Items {
		if item.ID != "" {
			itemIDs = append(itemIDs, item.ID)
		}
	}
	doc := mongoPlayQueueDocument{
		ID:        queue.UserID,
		UserID:    queue.UserID,
		Current:   queue.Current,
		Position:  queue.Position,
		ChangedBy: queue.ChangedBy,
		ItemIDs:   itemIDs,
		CreatedAt: queue.CreatedAt,
		UpdatedAt: queue.UpdatedAt,
	}
	_, err := r.c().ReplaceOne(r.ctx, bson.M{"userid": queue.UserID}, doc, options.Replace().SetUpsert(true))
	return err
}

func (r *mongoPlayQueueRepository) Retrieve(userID string) (*model.PlayQueue, error) {
	var doc mongoPlayQueueDocument
	err := r.c().FindOne(r.ctx, bson.M{"userid": userID}).Decode(&doc)
	if err != nil {
		return nil, mongoErr(err)
	}
	items := make(model.MediaFiles, 0, len(doc.ItemIDs))
	for _, id := range doc.ItemIDs {
		items = append(items, model.MediaFile{ID: id})
	}
	return &model.PlayQueue{
		ID:        doc.ID,
		UserID:    doc.UserID,
		Current:   doc.Current,
		Position:  doc.Position,
		ChangedBy: doc.ChangedBy,
		Items:     items,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
	}, nil
}

func (r *mongoPlayQueueRepository) RetrieveWithMediaFiles(userID string) (*model.PlayQueue, error) {
	queue, err := r.Retrieve(userID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(queue.Items))
	for _, item := range queue.Items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	mfs, err := (&mongoMediaFileRepository{ctx: r.ctx, store: r.store}).GetAll(model.QueryOptions{Filters: bsonFilter{"id": bson.M{"$in": ids}}})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]model.MediaFile, len(mfs))
	for _, mf := range mfs {
		byID[mf.ID] = mf
	}
	items := make(model.MediaFiles, 0, len(ids))
	for _, id := range ids {
		if mf, ok := byID[id]; ok {
			items = append(items, mf)
		}
	}
	queue.Items = items
	if queue.Current >= len(queue.Items) {
		queue.Current = max(len(queue.Items)-1, 0)
	}
	return queue, nil
}

func (r *mongoPlayQueueRepository) Clear(userID string) error {
	_, err := r.c().DeleteOne(r.ctx, bson.M{"userid": userID})
	return err
}

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
