package memcache

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
