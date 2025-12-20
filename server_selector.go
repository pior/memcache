package memcache

import (
	"github.com/pior/memcache/internal"
	"github.com/zeebo/xxh3"
)

// ServerSelector picks which server to use for a given key.
// It receives the key and the current list of server addresses.
// Returns empty string and error if no server can be selected.
type ServerSelector func(key string, serverCount int) int

// JumpSelectServer uses Jump Hash for consistent server selection.
// Jump Hash provides better distribution and fewer key movements when servers are added/removed.
// For a single server, it returns that server directly.
// Returns error if no servers are available.
func DefaultServerSelector(key string, serverCount int) int {
	return internal.JumpHash(xxh3.HashString(key), serverCount)
}

// staticSelector is used in tests to always select a specific server.
func staticSelector(index int) ServerSelector {
	return func(key string, serverCount int) int {
		return index % serverCount
	}
}
