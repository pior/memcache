package memcache

import (
	"fmt"
	"hash/crc32"
)

// Servers provides the list of memcache server addresses.
// Implementations must be safe for concurrent use.
type Servers interface {
	// List returns the current list of server addresses.
	// The returned slice must not be modified by the caller.
	List() []string
}

// StaticServers is a simple implementation that returns a fixed list of server addresses.
type StaticServers struct {
	addrs []string
}

// NewStaticServers creates a new StaticServers with the given addresses.
func NewStaticServers(addrs ...string) *StaticServers {
	return &StaticServers{addrs: addrs}
}

// List returns the list of server addresses.
func (s *StaticServers) List() []string {
	return s.addrs
}

// SelectServerFunc picks which server to use for a given key.
// It receives the key and the current list of server addresses.
// Returns empty string and error if no server can be selected.
type SelectServerFunc func(key string, servers []string) (string, error)

// DefaultSelectServer uses CRC32 hash for consistent server selection.
// For a single server, it returns that server directly.
// For multiple servers, it uses CRC32 hash modulo the number of servers.
// Returns error if no servers are available.
func DefaultSelectServer(key string, servers []string) (string, error) {
	if len(servers) == 0 {
		return "", fmt.Errorf("no servers available")
	}
	if len(servers) == 1 {
		return servers[0], nil
	}
	hash := crc32.ChecksumIEEE([]byte(key))
	return servers[hash%uint32(len(servers))], nil
}
