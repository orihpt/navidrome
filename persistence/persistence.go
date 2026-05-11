package persistence

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/deluan/rest"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/id"
	"github.com/navidrome/navidrome/utils"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type MongoStore struct {
	client *mongo.Client
	db     *mongo.Database
}

var mongoStore *MongoStore

func New() model.DataStore {
	if mongoStore != nil {
		return mongoStore
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(conf.Server.MongoDBURI))
	if err != nil {
		log.Fatal(ctx, "Error connecting to MongoDB", "uri", conf.Server.MongoDBURI, err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		log.Fatal(ctx, "Error pinging MongoDB", "uri", conf.Server.MongoDBURI, err)
	}
	mongoStore = &MongoStore{client: client, db: client.Database(conf.Server.MongoDBDatabase)}
	if err := mongoStore.ensureCollections(ctx); err != nil {
		log.Fatal(ctx, "Error preparing MongoDB collections", err)
	}
	log.Info(ctx, "Connected to MongoDB", "database", conf.Server.MongoDBDatabase)
	return mongoStore
}

func (s *MongoStore) ensureCollections(ctx context.Context) error {
	required := []string{
		"albums", "artist_requests", "artists", "follows", "folders", "genres", "libraries",
		"media_files", "play_queues", "playlist_tracks", "playlists", "plugin_kvstore", "plugin_queues", "plugin_tasks", "plugins", "properties",
		"radios", "scrobble_buffer", "scrobbles", "shares", "tags", "transcodings", "user_props", "users",
	}
	existing, err := s.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range existing {
		seen[name] = true
	}
	for _, name := range required {
		if !seen[name] {
			if err := s.db.CreateCollection(ctx, name); err != nil {
				return err
			}
		}
	}
	indexes := map[string][]mongo.IndexModel{
		"users": {
			{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
			{Keys: bson.D{{Key: "username_lc", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
			{Keys: bson.D{{Key: "isadmin", Value: 1}}},
		},
		"libraries": {
			{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		"properties": {
			{Keys: bson.D{{Key: "_id", Value: 1}}},
		},
		"media_files": {
			{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
			{Keys: bson.D{{Key: "path", Value: 1}}},
			{Keys: bson.D{{Key: "albumid", Value: 1}}},
			{Keys: bson.D{{Key: "artistid", Value: 1}}},
		},
		"albums": {
			{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		},
		"artists": {
			{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		},
	}
	for collection, models := range indexes {
		if len(models) == 0 {
			continue
		}
		if _, err := s.collection(collection).Indexes().CreateMany(ctx, models); err != nil {
			return err
		}
	}
	return nil
}

func Close(ctx context.Context) {
	if mongoStore == nil {
		return
	}
	if err := mongoStore.client.Disconnect(context.WithoutCancel(ctx)); err != nil {
		log.Error(ctx, "Error closing MongoDB", err)
	}
	mongoStore = nil
}

func (s *MongoStore) collection(name string) *mongo.Collection { return s.db.Collection(name) }
func (s *MongoStore) Library(ctx context.Context) model.LibraryRepository {
	return &mongoLibraryRepository{ctx: ctx, store: s}
}
func (s *MongoStore) Folder(ctx context.Context) model.FolderRepository {
	return &mongoFolderRepository{ctx: ctx, store: s}
}
func (s *MongoStore) Album(ctx context.Context) model.AlbumRepository {
	return &mongoAlbumRepository{ctx: ctx, store: s}
}
func (s *MongoStore) Artist(ctx context.Context) model.ArtistRepository {
	return &mongoArtistRepository{ctx: ctx, store: s}
}
func (s *MongoStore) MediaFile(ctx context.Context) model.MediaFileRepository {
	return &mongoMediaFileRepository{ctx: ctx, store: s}
}
func (s *MongoStore) Genre(ctx context.Context) model.GenreRepository {
	return &mongoGenreRepository{ctx: ctx}
}
func (s *MongoStore) Tag(ctx context.Context) model.TagRepository {
	return &mongoTagRepository{ctx: ctx}
}
func (s *MongoStore) Playlist(ctx context.Context) model.PlaylistRepository {
	return &mongoPlaylistRepository{ctx: ctx, store: s}
}
func (s *MongoStore) PlayQueue(ctx context.Context) model.PlayQueueRepository {
	return &mongoPlayQueueRepository{ctx: ctx}
}
func (s *MongoStore) Transcoding(ctx context.Context) model.TranscodingRepository {
	return &mongoTranscodingRepository{ctx: ctx}
}
func (s *MongoStore) Player(ctx context.Context) model.PlayerRepository {
	return &mongoPlayerRepository{ctx: ctx}
}
func (s *MongoStore) Radio(ctx context.Context) model.RadioRepository {
	return &mongoRadioRepository{ctx: ctx, store: s}
}
func (s *MongoStore) Share(ctx context.Context) model.ShareRepository {
	return &mongoShareRepository{ctx: ctx}
}
func (s *MongoStore) Property(ctx context.Context) model.PropertyRepository {
	return &mongoPropertyRepository{ctx: ctx, store: s}
}
func (s *MongoStore) User(ctx context.Context) model.UserRepository {
	return &mongoUserRepository{ctx: ctx, store: s}
}
func (s *MongoStore) UserProps(ctx context.Context) model.UserPropsRepository {
	return &mongoUserPropsRepository{ctx: ctx, store: s}
}
func (s *MongoStore) ScrobbleBuffer(ctx context.Context) model.ScrobbleBufferRepository {
	return &mongoScrobbleBufferRepository{ctx: ctx}
}
func (s *MongoStore) Scrobble(ctx context.Context) model.ScrobbleRepository {
	return &mongoScrobbleRepository{ctx: ctx}
}
func (s *MongoStore) Plugin(ctx context.Context) model.PluginRepository {
	return &mongoPluginRepository{ctx: ctx, store: s}
}
func (s *MongoStore) ArtistRequest(ctx context.Context) model.ArtistRequestRepository {
	return &mongoArtistRequestRepository{ctx: ctx}
}
func (s *MongoStore) Resource(ctx context.Context, m any) model.ResourceRepository {
	switch m.(type) {
	case model.User:
		return s.User(ctx).(model.ResourceRepository)
	case model.Playlist:
		return s.Playlist(ctx).(model.ResourceRepository)
	case model.MediaFile:
		return s.MediaFile(ctx).(model.ResourceRepository)
	case model.Album:
		return s.Album(ctx).(model.ResourceRepository)
	case model.Artist:
		return s.Artist(ctx).(model.ResourceRepository)
	case model.Radio:
		return s.Radio(ctx).(model.ResourceRepository)
	case model.Plugin:
		return s.Plugin(ctx).(model.ResourceRepository)
	}
	return &mongoResourceRepository{name: "unsupported"}
}
func (s *MongoStore) WithTx(block func(tx model.DataStore) error, _ ...string) error { return block(s) }
func (s *MongoStore) WithTxImmediate(block func(tx model.DataStore) error, scope ...string) error {
	return s.WithTx(block, scope...)
}
func (s *MongoStore) GC(ctx context.Context, _ ...int) error {
	log.Debug(ctx, "MongoDB garbage collection is not implemented yet")
	return nil
}

func notImplemented(name string) error {
	return errors.New("mongo repository method not implemented: " + name)
}
func mongoErr(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.ErrNotFound
	}
	return err
}

func keyTo32Bytes(input string) []byte {
	data := sha256.Sum256([]byte(input))
	return data[:]
}

type mongoProperty struct {
	ID    string `bson:"_id"`
	Value string `bson:"value"`
}
type mongoPropertyRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoPropertyRepository) Put(id, value string) error {
	_, err := r.store.collection("properties").UpdateOne(r.ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"value": value}}, options.UpdateOne().SetUpsert(true))
	return err
}
func (r *mongoPropertyRepository) Get(id string) (string, error) {
	var p mongoProperty
	err := r.store.collection("properties").FindOne(r.ctx, bson.M{"_id": id}).Decode(&p)
	return p.Value, mongoErr(err)
}
func (r *mongoPropertyRepository) Delete(id string) error {
	_, err := r.store.collection("properties").DeleteOne(r.ctx, bson.M{"_id": id})
	return err
}
func (r *mongoPropertyRepository) DefaultGet(id, def string) (string, error) {
	v, err := r.Get(id)
	if errors.Is(err, model.ErrNotFound) {
		return def, nil
	}
	return v, err
}

type mongoUserRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoUserRepository) c() *mongo.Collection { return r.store.collection("users") }
func (r *mongoUserRepository) CountAll(...model.QueryOptions) (int64, error) {
	return r.c().CountDocuments(r.ctx, bson.M{})
}
func (r *mongoUserRepository) Delete(uid string) error {
	_, err := r.c().DeleteOne(r.ctx, bson.M{"id": uid})
	return err
}
func (r *mongoUserRepository) Get(uid string) (*model.User, error) {
	var u model.User
	err := r.c().FindOne(r.ctx, bson.M{"id": uid}).Decode(&u)
	return &u, mongoErr(err)
}
func (r *mongoUserRepository) GetAll(...model.QueryOptions) (model.Users, error) {
	cur, err := r.c().Find(r.ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(r.ctx)
	var out model.Users
	for cur.Next(r.ctx) {
		var u model.User
		if err := cur.Decode(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, cur.Err()
}
func (r *mongoUserRepository) Put(u *model.User) error {
	if u.ID == "" {
		u.ID = id.NewRandom()
	}
	now := time.Now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	if u.NewPassword != "" {
		enc, err := utils.Encrypt(r.ctx, keyTo32Bytes(consts.DefaultEncryptionKey), u.NewPassword)
		if err != nil {
			return err
		}
		u.Password = enc
		u.NewPassword = ""
	}
	_, err := r.c().ReplaceOne(r.ctx, bson.M{"id": u.ID}, u, options.Replace().SetUpsert(true))
	return err
}
func (r *mongoUserRepository) UpdateLastLoginAt(uid string) error {
	now := time.Now()
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": uid}, bson.M{"$set": bson.M{"lastloginat": &now}})
	return err
}
func (r *mongoUserRepository) UpdateLastAccessAt(uid string) error {
	now := time.Now()
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": uid}, bson.M{"$set": bson.M{"lastaccessat": &now}})
	return err
}
func (r *mongoUserRepository) FindFirstAdmin() (*model.User, error) {
	var u model.User
	err := r.c().FindOne(r.ctx, bson.M{"isadmin": true}).Decode(&u)
	return &u, mongoErr(err)
}
func (r *mongoUserRepository) FindByUsername(username string) (*model.User, error) {
	var u model.User
	err := r.c().FindOne(r.ctx, bson.M{"username": bson.M{"$regex": "^" + username + "$", "$options": "i"}}).Decode(&u)
	return &u, mongoErr(err)
}
func (r *mongoUserRepository) FindByUsernameWithPassword(username string) (*model.User, error) {
	u, err := r.FindByUsername(username)
	if err != nil {
		return nil, err
	}
	if u.Password != "" {
		if plain, err := utils.Decrypt(r.ctx, keyTo32Bytes(consts.DefaultEncryptionKey), u.Password); err == nil {
			u.Password = plain
		}
	}
	return u, nil
}
func (r *mongoUserRepository) GetUserLibraries(string) (model.Libraries, error) {
	return (&mongoLibraryRepository{ctx: r.ctx, store: r.store}).GetAll()
}
func (r *mongoUserRepository) SetUserLibraries(string, []int) error { return nil }
func (r *mongoUserRepository) Search(query string, _ ...model.QueryOptions) (model.Users, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return r.GetAll()
	}
	cur, err := r.c().Find(r.ctx, bson.M{"$or": []bson.M{{"username": bson.M{"$regex": query, "$options": "i"}}, {"name": bson.M{"$regex": query, "$options": "i"}}}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(r.ctx)
	var out model.Users
	for cur.Next(r.ctx) {
		var u model.User
		if err := cur.Decode(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, cur.Err()
}
func (r *mongoUserRepository) Follow(string, string) error               { return nil }
func (r *mongoUserRepository) Unfollow(string, string) error             { return nil }
func (r *mongoUserRepository) GetFollowers(string) (model.Users, error)  { return nil, nil }
func (r *mongoUserRepository) GetFollowing(string) (model.Users, error)  { return nil, nil }
func (r *mongoUserRepository) IsFollowing(string, string) (bool, error)  { return false, nil }
func (r *mongoUserRepository) Count(...rest.QueryOptions) (int64, error) { return r.CountAll() }
func (r *mongoUserRepository) Read(uid string) (any, error)              { return r.Get(uid) }
func (r *mongoUserRepository) ReadAll(...rest.QueryOptions) (any, error) { return r.GetAll() }
func (r *mongoUserRepository) EntityName() string                        { return "user" }
func (r *mongoUserRepository) NewInstance() any                          { return &model.User{} }
func (r *mongoUserRepository) Save(entity any) (string, error) {
	u := entity.(*model.User)
	return u.ID, r.Put(u)
}
func (r *mongoUserRepository) Update(uid string, entity any, _ ...string) error {
	u := entity.(*model.User)
	u.ID = uid
	return r.Put(u)
}

type mongoLibraryRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoLibraryRepository) c() *mongo.Collection { return r.store.collection("libraries") }
func (r *mongoLibraryRepository) defaultLibrary() model.Library {
	now := time.Now()
	return model.Library{ID: model.DefaultLibraryID, Name: model.DefaultLibraryName, Path: conf.Server.MusicFolder, DefaultNewUsers: true, CreatedAt: now, UpdatedAt: now}
}
func (r *mongoLibraryRepository) Get(lid int) (*model.Library, error) {
	var l model.Library
	err := r.c().FindOne(r.ctx, bson.M{"id": lid}).Decode(&l)
	return &l, mongoErr(err)
}
func (r *mongoLibraryRepository) GetPath(lid int) (string, error) {
	l, err := r.Get(lid)
	if err != nil {
		return "", err
	}
	return l.Path, nil
}
func (r *mongoLibraryRepository) GetAll(...model.QueryOptions) (model.Libraries, error) {
	cur, err := r.c().Find(r.ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(r.ctx)
	var out model.Libraries
	for cur.Next(r.ctx) {
		var l model.Library
		if err := cur.Decode(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		l := r.defaultLibrary()
		_ = r.Put(&l)
		out = append(out, l)
	}
	return out, cur.Err()
}
func (r *mongoLibraryRepository) CountAll(...model.QueryOptions) (int64, error) {
	return r.c().CountDocuments(r.ctx, bson.M{})
}
func (r *mongoLibraryRepository) Put(l *model.Library) error {
	if l.ID == 0 {
		l.ID = model.DefaultLibraryID
	}
	_, err := r.c().ReplaceOne(r.ctx, bson.M{"id": l.ID}, l, options.Replace().SetUpsert(true))
	return err
}
func (r *mongoLibraryRepository) Delete(lid int) error {
	_, err := r.c().DeleteOne(r.ctx, bson.M{"id": lid})
	return err
}
func (r *mongoLibraryRepository) StoreMusicFolder() error                            { l := r.defaultLibrary(); return r.Put(&l) }
func (r *mongoLibraryRepository) AddArtist(int, string) error                        { return nil }
func (r *mongoLibraryRepository) GetUsersWithLibraryAccess(int) (model.Users, error) { return nil, nil }
func (r *mongoLibraryRepository) ScanBegin(lid int, full bool) error {
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": lid}, bson.M{"$set": bson.M{"fullscaninprogress": full, "lastscanstartedat": time.Now()}}, options.UpdateOne().SetUpsert(true))
	return err
}
func (r *mongoLibraryRepository) ScanEnd(lid int) error {
	_, err := r.c().UpdateOne(r.ctx, bson.M{"id": lid}, bson.M{"$set": bson.M{"fullscaninprogress": false, "lastscanat": time.Now()}})
	return err
}
func (r *mongoLibraryRepository) ScanInProgress() (bool, error) {
	libs, err := r.GetAll()
	if err != nil {
		return false, err
	}
	for _, l := range libs {
		if l.FullScanInProgress {
			return true, nil
		}
	}
	return false, nil
}
func (r *mongoLibraryRepository) RefreshStats(int) error { return nil }

type mongoUserPropsRepository struct {
	ctx   context.Context
	store *MongoStore
}

func (r *mongoUserPropsRepository) key(user, key string) string { return user + ":" + key }
func (r *mongoUserPropsRepository) Put(user, key, value string) error {
	return (&mongoPropertyRepository{ctx: r.ctx, store: r.store}).Put(r.key(user, key), value)
}
func (r *mongoUserPropsRepository) Get(user, key string) (string, error) {
	return (&mongoPropertyRepository{ctx: r.ctx, store: r.store}).Get(r.key(user, key))
}
func (r *mongoUserPropsRepository) Delete(user, key string) error {
	return (&mongoPropertyRepository{ctx: r.ctx, store: r.store}).Delete(r.key(user, key))
}
func (r *mongoUserPropsRepository) DefaultGet(user, key, def string) (string, error) {
	return (&mongoPropertyRepository{ctx: r.ctx, store: r.store}).DefaultGet(r.key(user, key), def)
}

type mongoResourceRepository struct{ name string }

func (r *mongoResourceRepository) Count(...rest.QueryOptions) (int64, error) { return 0, nil }
func (r *mongoResourceRepository) Read(string) (any, error)                  { return nil, rest.ErrNotFound }
func (r *mongoResourceRepository) ReadAll(...rest.QueryOptions) (any, error) { return []any{}, nil }
func (r *mongoResourceRepository) EntityName() string                        { return r.name }
func (r *mongoResourceRepository) NewInstance() any                          { return map[string]any{} }
func (r *mongoResourceRepository) Save(any) (string, error) {
	return "", notImplemented(r.name + ".Save")
}
func (r *mongoResourceRepository) Update(string, any, ...string) error {
	return notImplemented(r.name + ".Update")
}
func (r *mongoResourceRepository) Delete(string) error { return notImplemented(r.name + ".Delete") }

type mongoGenreRepository struct{ ctx context.Context }

func (*mongoGenreRepository) GetAll(...model.QueryOptions) (model.Genres, error) { return nil, nil }

type mongoTagRepository struct{ ctx context.Context }

func (*mongoTagRepository) Add(int, ...model.Tag) error { return nil }
func (*mongoTagRepository) UpdateCounts() error         { return nil }

type mongoTranscodingRepository struct{ ctx context.Context }

func (*mongoTranscodingRepository) Get(string) (*model.Transcoding, error) {
	return nil, model.ErrNotFound
}
func (*mongoTranscodingRepository) CountAll(...model.QueryOptions) (int64, error) { return 0, nil }
func (*mongoTranscodingRepository) Put(*model.Transcoding) error                  { return nil }
func (*mongoTranscodingRepository) FindByFormat(string) (*model.Transcoding, error) {
	return nil, model.ErrNotFound
}

type mongoPlayerRepository struct{ ctx context.Context }

func (*mongoPlayerRepository) Get(string) (*model.Player, error) { return nil, model.ErrNotFound }
func (*mongoPlayerRepository) FindMatch(string, string, string) (*model.Player, error) {
	return nil, model.ErrNotFound
}
func (*mongoPlayerRepository) Put(*model.Player) error                       { return nil }
func (*mongoPlayerRepository) CountAll(...model.QueryOptions) (int64, error) { return 0, nil }
func (*mongoPlayerRepository) CountByClient(...model.QueryOptions) (map[string]int64, error) {
	return map[string]int64{}, nil
}

type mongoScrobbleRepository struct{ ctx context.Context }

func (*mongoScrobbleRepository) RecordScrobble(string, time.Time) error { return nil }
func (*mongoScrobbleRepository) GetRecentlyPlayed(string, int) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoScrobbleRepository) GetCommunityRecentlyPlayed(int) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoScrobbleRepository) GetCommunityMostPlayed(int) (model.MediaFiles, error) {
	return nil, nil
}
func (*mongoScrobbleRepository) GetFollowingRecentlyPlayed(string, int) (model.MediaFiles, error) {
	return nil, nil
}

type mongoScrobbleBufferRepository struct{ ctx context.Context }

func (*mongoScrobbleBufferRepository) UserIDs(string) ([]string, error)                { return nil, nil }
func (*mongoScrobbleBufferRepository) Enqueue(string, string, string, time.Time) error { return nil }
func (*mongoScrobbleBufferRepository) Next(string, string) (*model.ScrobbleEntry, error) {
	return nil, model.ErrNotFound
}
func (*mongoScrobbleBufferRepository) Dequeue(*model.ScrobbleEntry) error { return nil }
func (*mongoScrobbleBufferRepository) Length() (int64, error)             { return 0, nil }

type mongoArtistRequestRepository struct{ ctx context.Context }

func (*mongoArtistRequestRepository) GetAll(string) (model.ArtistRequests, error) { return nil, nil }
func (*mongoArtistRequestRepository) Create(string, string, string) (*model.ArtistRequest, error) {
	return nil, notImplemented("artistRequest.Create")
}
func (*mongoArtistRequestRepository) ToggleVote(string, string) error         { return nil }
func (*mongoArtistRequestRepository) Delete(string) error                     { return nil }
func (*mongoArtistRequestRepository) UpdateName(string, string, string) error { return nil }
func (*mongoArtistRequestRepository) Move(string, string) error               { return nil }

func emptyAlbumCursor() model.AlbumCursor {
	return model.AlbumCursor(func(yield func(model.Album, error) bool) {})
}
func emptyFolderCursor() model.FolderCursor {
	return model.FolderCursor(func(yield func(model.Folder, error) bool) {})
}
func emptyMediaCursor() model.MediaFileCursor {
	return model.MediaFileCursor(func(yield func(model.MediaFile, error) bool) {})
}
