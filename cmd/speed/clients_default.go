//go:build !puddle

package main

import (
	"log"

	"github.com/pior/memcache"
)

func configurePuddlePool(cfg *memcache.Config) {
	log.Fatal("Puddle pool support not compiled in. Rebuild with -tags=puddle to enable.")
}
