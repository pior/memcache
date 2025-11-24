//go:build puddle

package main

import (
	"github.com/pior/memcache"
)

func configurePuddlePool(cfg *memcache.Config) {
	cfg.Pool = memcache.NewPuddlePool
}
