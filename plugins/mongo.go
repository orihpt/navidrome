package plugins

import (
	"context"
	"sync"
	"time"

	"github.com/navidrome/navidrome/conf"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

var pluginMongo struct {
	once sync.Once
	db   *mongo.Database
	err  error
}

func pluginMongoDB(ctx context.Context) (*mongo.Database, error) {
	pluginMongo.once.Do(func() {
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		client, err := mongo.Connect(options.Client().ApplyURI(conf.Server.MongoDBURI))
		if err != nil {
			pluginMongo.err = err
			return
		}
		if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
			pluginMongo.err = err
			return
		}
		pluginMongo.db = client.Database(conf.Server.MongoDBDatabase)
	})
	return pluginMongo.db, pluginMongo.err
}
