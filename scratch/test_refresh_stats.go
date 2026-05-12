//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	db := client.Database("waves_music")

	// Aggregate media_files to get stats per artist
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"missing": false}}},
		{{Key: "$group", Value: bson.M{
			"_id":        "$artistid",
			"songCount":  bson.M{"$sum": 1},
			"albumCount": bson.M{"$addToSet": "$albumid"},
			"size":       bson.M{"$sum": "$size"},
		}}},
	}

	cursor, err := db.Collection("media_files").Aggregate(ctx, pipeline)
	if err != nil {
		log.Fatal(err)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var result bson.M
		if err := cursor.Decode(&result); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Artist ID: %v, Songs: %v, Albums: %v, Size: %v\n",
			result["_id"], result["songCount"], len(result["albumCount"].(bson.A)), result["size"])
	}
}
