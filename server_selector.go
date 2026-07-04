package memcache

import (
	"github.com/pior/memcache/internal"
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

// OrderedServerSelector maps a key to a server by its position in the list,
// using Jump Hash. It hashes the key once, so it is the cheaper selector, but
// the result depends on the order and count of servers: inserting or removing a
// server anywhere but the end remaps a large fraction of keys. Use it only for a
// static, stably ordered server list. NewClient defaults to
// StableServerSelector instead; set Config.ServerSelector explicitly to opt in.
func OrderedServerSelector(key string, servers []Server) Server {
	return servers[internal.JumpHash(xxh3.HashString(key), len(servers))]
}

// StableServerSelector maps a key to a server by server identity, using
// Rendezvous (Highest-Random-Weight) hashing. It is membership-stable: the
// result depends only on the set of Addresses and not on their order, so
// reordering the list never remaps a key, and adding or removing a single
// server moves only ~1/N of keys. This costs one hash per server per call. Ties
// (astronomically unlikely with a 64-bit hash) break on the lower Address to
// stay order-independent.
func StableServerSelector(key string, servers []Server) Server {
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
