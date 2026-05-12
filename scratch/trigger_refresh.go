//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/persistence"
)

func main() {
	// Initialize config
	conf.Init()
	// Override MongoDB URI if needed (it should be in .env)

	ds := persistence.New()
	ctx := context.Background()

	fmt.Println("Refreshing Artist Stats...")
	count, err := ds.Artist(ctx).RefreshStats(true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Done! Updated %d artists.\n", count)

	os.Exit(0)
}
