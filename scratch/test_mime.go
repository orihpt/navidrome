//go:build ignore

package main

import (
	"fmt"
	"mime"
)

func main() {
	fmt.Printf("flac: '%s'\n", mime.TypeByExtension(".flac"))
	fmt.Printf("FLAC: '%s'\n", mime.TypeByExtension(".FLAC"))
	fmt.Printf("mp3: '%s'\n", mime.TypeByExtension(".mp3"))
}
