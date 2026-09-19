package consistenthash

import "github.com/zeebo/xxh3"

// RendezvousScore scores a (key, node) pair for Rendezvous
// (Highest-Random-Weight) hashing: the node with the highest score owns the
// key. The key is hashed once by the caller; mixing the per-node hash with a
// splitmix64-style finalizer decorrelates the two so the ranking behaves like
// an independent draw per (key, node) pair, without allocating a combined
// string.
func RendezvousScore(keyHash uint64, node string) uint64 {
	h := keyHash ^ xxh3.HashString(node)
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}
