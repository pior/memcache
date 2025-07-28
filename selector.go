package memcache

import (
	"context"
	"errors"
	"hash/crc32"
	"sort"
	"sync"
)

var (
	ErrNoServersAvailable = errors.New("memcache: no servers available")
)

// ServerSelector is an interface for selecting servers for a given key
type ServerSelector interface {
	SelectServer(key string) (ConnectionPool, error)
	AddServer(addr string, pool ConnectionPool)
	RemoveServer(addr string)
	GetServers() []ConnectionPool
	Ping(ctx context.Context) error
	Stats() []PoolStats
	Close() error
}

// ConsistentHashSelector implements consistent hashing for server selection
type ConsistentHashSelector struct {
	mu           sync.RWMutex
	servers      map[string]ConnectionPool
	ring         []uint32
	ringServers  map[uint32]string
	virtualNodes int
}

// NewConsistentHashSelector creates a new consistent hash selector
func NewConsistentHashSelector() *ConsistentHashSelector {
	return &ConsistentHashSelector{
		servers:      make(map[string]ConnectionPool),
		ringServers:  make(map[uint32]string),
		virtualNodes: 150, // Default number of virtual nodes per server
	}
}

// NewConsistentHashSelectorWithVirtualNodes creates a new consistent hash selector with custom virtual nodes
func NewConsistentHashSelectorWithVirtualNodes(virtualNodes int) *ConsistentHashSelector {
	return &ConsistentHashSelector{
		servers:      make(map[string]ConnectionPool),
		ringServers:  make(map[uint32]string),
		virtualNodes: virtualNodes,
	}
}

// NewConsistentHashSelectorWithPools creates a new consistent hash selector with the given servers
func NewConsistentHashSelectorWithPools(servers []string, poolConfig *PoolConfig, virtualNodes int) (*ConsistentHashSelector, error) {
	selector := &ConsistentHashSelector{
		servers:      make(map[string]ConnectionPool),
		ringServers:  make(map[uint32]string),
		virtualNodes: virtualNodes,
	}

	// Create pools for each server
	for _, addr := range servers {
		pool, err := NewPool(addr, poolConfig)
		if err != nil {
			// Close any pools we've already created
			selector.Close()
			return nil, err
		}
		selector.AddServer(addr, pool)
	}

	return selector, nil
}

// SelectServer selects a server for the given key using consistent hashing
func (s *ConsistentHashSelector) SelectServer(key string) (ConnectionPool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.servers) == 0 {
		return nil, ErrNoServersAvailable
	}

	if len(s.servers) == 1 {
		// Only one server, return it
		for _, pool := range s.servers {
			return pool, nil
		}
	}

	hash := crc32.ChecksumIEEE([]byte(key))

	// Find the first server on the ring at or after this hash
	idx := sort.Search(len(s.ring), func(i int) bool {
		return s.ring[i] >= hash
	})

	// If we're past the end of the ring, wrap around to the first
	if idx == len(s.ring) {
		idx = 0
	}

	serverAddr := s.ringServers[s.ring[idx]]
	pool, exists := s.servers[serverAddr]
	if !exists {
		return nil, ErrNoServersAvailable
	}

	return pool, nil
}

// AddServer adds a server to the selector
func (s *ConsistentHashSelector) AddServer(addr string, pool ConnectionPool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.servers[addr] = pool
	s.rebuildRing()
}

// RemoveServer removes a server from the selector
func (s *ConsistentHashSelector) RemoveServer(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.servers, addr)
	s.rebuildRing()
}

// GetServers returns all servers
func (s *ConsistentHashSelector) GetServers() []ConnectionPool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pools := make([]ConnectionPool, 0, len(s.servers))
	for _, pool := range s.servers {
		pools = append(pools, pool)
	}
	return pools
}

// Ping checks connectivity to all servers
func (s *ConsistentHashSelector) Ping(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var lastErr error
	for _, pool := range s.servers {
		err := pool.With(func(conn *Connection) error {
			return conn.Ping(ctx)
		})
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Stats returns statistics from all servers
func (s *ConsistentHashSelector) Stats() []PoolStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make([]PoolStats, 0, len(s.servers))
	for _, pool := range s.servers {
		stats = append(stats, pool.Stats())
	}
	return stats
}

// Close closes all server pools
func (s *ConsistentHashSelector) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastErr error
	for _, pool := range s.servers {
		if err := pool.Close(); err != nil {
			lastErr = err
		}
	}

	s.servers = make(map[string]ConnectionPool)
	s.ring = nil
	s.ringServers = make(map[uint32]string)

	return lastErr
}

// rebuildRing rebuilds the consistent hash ring (must be called with write lock)
func (s *ConsistentHashSelector) rebuildRing() {
	s.ring = nil
	s.ringServers = make(map[uint32]string)

	for addr := range s.servers {
		for i := 0; i < s.virtualNodes; i++ {
			virtualKey := addr + ":" + string(rune(i))
			hash := crc32.ChecksumIEEE([]byte(virtualKey))
			s.ring = append(s.ring, hash)
			s.ringServers[hash] = addr
		}
	}

	sort.Slice(s.ring, func(i, j int) bool {
		return s.ring[i] < s.ring[j]
	})
}
