package memcache

import (
	"github.com/zeebo/xxh3"
)

// ServerSelector picks which server owns a given key, from the current set of
// servers.
//
// It must be deterministic and based on server identity (Address) so that all
// clients agree on key placement and a change to the server set moves as few
// keys as possible. The servers slice is never empty: the single-server case is
// handled before the selector is consulted.
type ServerSelector func(key string, servers []Server) Server

// DefaultServerSelector uses Rendezvous (Highest-Random-Weight) hashing.
//
// For each server it derives a score from the key and the server Address and
// returns the highest-scoring server. Unlike modulo or jump hashing, the result
// depends only on the set of Addresses and not on their order, so reordering the
// list never remaps a key, and adding or removing a single server moves only
// ~1/N of keys. Ties (astronomically unlikely with a 64-bit hash) break on the
// lower Address to stay order-independent.
func DefaultServerSelector(key string, servers []Server) Server {
	keyHash := xxh3.HashString(key)

	best := servers[0]
	bestScore := rendezvousScore(keyHash, best.Address)
	for _, s := range servers[1:] {
		score := rendezvousScore(keyHash, s.Address)
		if score > bestScore || (score == bestScore && s.Address < best.Address) {
			best, bestScore = s, score
		}
	}
	return best
}

// rendezvousScore combines the key hash with the server address into a
// well-distributed score. The key is hashed once by the caller; mixing the
// per-address hash with a splitmix64-style finalizer decorrelates the two so
// the ranking behaves like an independent draw per (key, server) pair, without
// allocating a combined string.
func rendezvousScore(keyHash uint64, addr string) uint64 {
	h := keyHash ^ xxh3.HashString(addr)
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

// staticSelector is used in tests to always select a specific server.
func staticSelector(index int) ServerSelector {
	return func(key string, servers []Server) Server {
		return servers[index%len(servers)]
	}
}
