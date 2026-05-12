//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"

	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/utils"
)

func keyTo32Bytes(input string) []byte {
	data := sha256.Sum256([]byte(input))
	return data[0:]
}

func main() {
	key := keyTo32Bytes(consts.DefaultEncryptionKey)
	encrypted, err := utils.Encrypt(context.Background(), key, "N3verGuESSTHIS")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(encrypted)
}
