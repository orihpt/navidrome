package plugins

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/plugins/host"
	"github.com/navidrome/navidrome/utils/slice"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	defaultMaxKVStoreSize = 1 * 1024 * 1024
	maxKeyLength          = 256
	cleanupInterval       = 1 * time.Hour
)

type kvstoreServiceImpl struct {
	pluginName string
	collection *mongo.Collection
	maxSize    int64
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

type kvRecord struct {
	Plugin    string     `bson:"plugin"`
	Key       string     `bson:"key"`
	Value     []byte     `bson:"value"`
	Size      int64      `bson:"size"`
	CreatedAt time.Time  `bson:"createdAt"`
	UpdatedAt time.Time  `bson:"updatedAt"`
	ExpiresAt *time.Time `bson:"expiresAt,omitempty"`
}

func newKVStoreService(ctx context.Context, pluginName string, perm *KVStorePermission) (*kvstoreServiceImpl, error) {
	maxSize := int64(defaultMaxKVStoreSize)
	if perm != nil && perm.MaxSize != nil && *perm.MaxSize != "" {
		parsed, err := humanize.ParseBytes(*perm.MaxSize)
		if err != nil {
			return nil, fmt.Errorf("invalid maxSize %q: %w", *perm.MaxSize, err)
		}
		maxSize = int64(parsed)
	}
	db, err := pluginMongoDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting plugin kvstore to MongoDB: %w", err)
	}
	collection := db.Collection("plugin_kvstore")
	_, err = collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "plugin", Value: 1}, {Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}},
	})
	if err != nil {
		return nil, err
	}
	cleanupCtx, cancel := context.WithCancel(ctx)
	svc := &kvstoreServiceImpl{pluginName: pluginName, collection: collection, maxSize: maxSize, cancel: cancel}
	svc.wg.Add(1)
	go svc.cleanupLoop(cleanupCtx)
	log.Debug(ctx, "Initialized plugin kvstore", "plugin", pluginName, "backend", "mongo", "maxSize", humanize.Bytes(uint64(maxSize)))
	return svc, nil
}

func (s *kvstoreServiceImpl) liveFilter(extra bson.M) bson.M {
	filter := bson.M{"plugin": s.pluginName, "$or": []bson.M{{"expiresAt": bson.M{"$exists": false}}, {"expiresAt": bson.M{"$gte": time.Now()}}}}
	for k, v := range extra {
		filter[k] = v
	}
	return filter
}

func (s *kvstoreServiceImpl) storageUsed(ctx context.Context) (int64, error) {
	cur, err := s.collection.Aggregate(ctx, []bson.M{
		{"$match": s.liveFilter(bson.M{})},
		{"$group": bson.M{"_id": nil, "size": bson.M{"$sum": "$size"}}},
	})
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	if cur.Next(ctx) {
		var out struct {
			Size int64 `bson:"size"`
		}
		if err := cur.Decode(&out); err != nil {
			return 0, err
		}
		return out.Size, nil
	}
	return 0, cur.Err()
}

func (s *kvstoreServiceImpl) checkStorageLimit(ctx context.Context, delta int64) error {
	if delta <= 0 {
		return nil
	}
	used, err := s.storageUsed(ctx)
	if err != nil {
		return err
	}
	if used+delta > s.maxSize {
		return fmt.Errorf("storage limit exceeded: would use %s of %s allowed", humanize.Bytes(uint64(used+delta)), humanize.Bytes(uint64(s.maxSize)))
	}
	return nil
}

func (s *kvstoreServiceImpl) setValue(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}
	if len(key) > maxKeyLength {
		return fmt.Errorf("key exceeds maximum length of %d bytes", maxKeyLength)
	}
	var existing kvRecord
	err := s.collection.FindOne(ctx, s.liveFilter(bson.M{"key": key})).Decode(&existing)
	if err != nil && err != mongo.ErrNoDocuments {
		return err
	}
	if err := s.checkStorageLimit(ctx, int64(len(value))-existing.Size); err != nil {
		return err
	}
	now := time.Now()
	var expiresAt *time.Time
	if ttlSeconds > 0 {
		t := now.Add(time.Duration(ttlSeconds) * time.Second)
		expiresAt = &t
	}
	update := bson.M{"$set": bson.M{"plugin": s.pluginName, "key": key, "value": value, "size": int64(len(value)), "updatedAt": now, "expiresAt": expiresAt}, "$setOnInsert": bson.M{"createdAt": now}}
	_, err = s.collection.UpdateOne(ctx, bson.M{"plugin": s.pluginName, "key": key}, update, options.UpdateOne().SetUpsert(true))
	return err
}

func (s *kvstoreServiceImpl) Set(ctx context.Context, key string, value []byte) error {
	return s.setValue(ctx, key, value, 0)
}

func (s *kvstoreServiceImpl) SetWithTTL(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	if ttlSeconds <= 0 {
		return fmt.Errorf("ttlSeconds must be greater than 0")
	}
	return s.setValue(ctx, key, value, ttlSeconds)
}

func (s *kvstoreServiceImpl) Get(ctx context.Context, key string) ([]byte, bool, error) {
	var rec kvRecord
	err := s.collection.FindOne(ctx, s.liveFilter(bson.M{"key": key})).Decode(&rec)
	if err == mongo.ErrNoDocuments {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return rec.Value, true, nil
}

func (s *kvstoreServiceImpl) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	result := map[string][]byte{}
	for chunk := range slice.CollectChunks(slices.Values(keys), 200) {
		cur, err := s.collection.Find(ctx, s.liveFilter(bson.M{"key": bson.M{"$in": chunk}}))
		if err != nil {
			return nil, err
		}
		for cur.Next(ctx) {
			var rec kvRecord
			if err := cur.Decode(&rec); err != nil {
				cur.Close(ctx)
				return nil, err
			}
			result[rec.Key] = rec.Value
		}
		if err := cur.Close(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *kvstoreServiceImpl) Delete(ctx context.Context, key string) error {
	_, err := s.collection.DeleteOne(ctx, bson.M{"plugin": s.pluginName, "key": key})
	return err
}

func (s *kvstoreServiceImpl) Has(ctx context.Context, key string) (bool, error) {
	n, err := s.collection.CountDocuments(ctx, s.liveFilter(bson.M{"key": key}))
	return n > 0, err
}

func (s *kvstoreServiceImpl) List(ctx context.Context, prefix string) ([]string, error) {
	filter := s.liveFilter(bson.M{})
	if prefix != "" {
		filter["key"] = bson.M{"$regex": "^" + regexpQuote(prefix)}
	}
	cur, err := s.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "key", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var keys []string
	for cur.Next(ctx) {
		var rec kvRecord
		if err := cur.Decode(&rec); err != nil {
			return nil, err
		}
		keys = append(keys, rec.Key)
	}
	return keys, cur.Err()
}

func (s *kvstoreServiceImpl) DeleteByPrefix(ctx context.Context, prefix string) (int64, error) {
	if prefix == "" {
		return 0, fmt.Errorf("prefix cannot be empty")
	}
	res, err := s.collection.DeleteMany(ctx, bson.M{"plugin": s.pluginName, "key": bson.M{"$regex": "^" + regexpQuote(prefix)}})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

func (s *kvstoreServiceImpl) GetStorageUsed(ctx context.Context) (int64, error) {
	return s.storageUsed(ctx)
}

func (s *kvstoreServiceImpl) cleanupLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpired(ctx)
		}
	}
}

func (s *kvstoreServiceImpl) cleanupExpired(ctx context.Context) {
	res, err := s.collection.DeleteMany(ctx, bson.M{"plugin": s.pluginName, "expiresAt": bson.M{"$lt": time.Now()}})
	if err != nil {
		log.Error(ctx, "KVStore cleanup failed", "plugin", s.pluginName, err)
		return
	}
	if res.DeletedCount > 0 {
		log.Debug(ctx, "KVStore cleanup completed", "plugin", s.pluginName, "deletedKeys", res.DeletedCount)
	}
}

func (s *kvstoreServiceImpl) Close() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

func regexpQuote(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`, `+`, `\+`, `*`, `\*`, `?`, `\?`, `(`, `\(`, `)`, `\)`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `^`, `\^`, `$`, `\$`, `|`, `\|`).Replace(prefix)
}

var _ host.KVStoreService = (*kvstoreServiceImpl)(nil)
