package memcache

// ClientV2 is the new frontend API that composes a ConnectionProvider with Commands.
// It provides a clean separation between connection management strategy and command operations.
type ClientV2 struct {
	*Commands
	provider ConnectionProvider
}

// NewClientV2 creates a new client with the given connection provider.
// This allows you to choose between SimpleProvider and ResilientProvider
// depending on your needs.
//
// Example with SimpleProvider:
//
//	provider, _ := memcache.NewSimpleProvider("localhost:11211", config)
//	client := memcache.NewClientV2(provider)
//	defer client.Close()
//
// Example with ResilientProvider:
//
//	servers := memcache.NewStaticServers("server1:11211", "server2:11211")
//	provider, _ := memcache.NewResilientProvider(servers, resilientConfig)
//	client := memcache.NewClientV2(provider)
//	defer client.Close()
func NewClientV2(provider ConnectionProvider) *ClientV2 {
	return &ClientV2{
		Commands: NewCommands(provider),
		provider: provider,
	}
}

// Close closes the client and releases all resources.
func (c *ClientV2) Close() error {
	return c.provider.Close()
}

// Stats returns statistics from the connection provider.
// The type of stats returned depends on the provider implementation:
//   - SimpleProvider: PoolStats
//   - ResilientProvider: []ServerPoolStats
func (c *ClientV2) Stats() interface{} {
	return c.provider.Stats()
}

// AllPoolStats returns stats for all server pools (resilient provider only).
// Returns nil for other provider types.
func (c *ClientV2) AllPoolStats() []ServerPoolStats {
	if stats, ok := c.provider.Stats().([]ServerPoolStats); ok {
		return stats
	}
	return nil
}
