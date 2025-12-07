package memcache

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// NoTTL represents an infinite TTL (no expiration).
// Use this constant when you want items to persist indefinitely in memcache.
const NoTTL = 0

type Item struct {
	Key   string
	Value []byte
	TTL   time.Duration
	Found bool // indicates whether the key was found in cache
}

// Config holds configuration for the memcache client connection pool.
type Config struct {
	// MaxSize is the maximum number of connections in the pool.
	// Required: must be > 0.
	MaxSize int32

	// MaxConnLifetime is the maximum duration a connection can be reused.
	// Zero means no limit.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the maximum duration a connection can be idle before being closed.
	// Zero means no limit.
	MaxConnIdleTime time.Duration

	// HealthCheckInterval is how often to check idle connections for health.
	// Zero disables health checks.
	HealthCheckInterval time.Duration

	// Timeout is the default timeout for memcache operations (read/write).
	// This is used when the context passed to Execute/ExecuteBatch has no deadline.
	// Zero means no timeout (not recommended for production).
	// Recommended: 100ms-1s depending on your latency requirements.
	Timeout time.Duration

	// ConnectTimeout is the timeout for establishing new connections.
	// This includes TCP handshake and TLS handshake if applicable.
	// If zero, uses Timeout value.
	// Set this higher than Timeout if TLS connections take longer to establish.
	ConnectTimeout time.Duration

	// Dialer is the net.Dialer used to create new connections.
	// If nil, the default net.Dialer is used.
	Dialer Dialer

	// NewPool is the connection pool factory function.
	// If nil, uses the default puddle-based pool.
	// To use channel pool: NewPool: memcache.NewChannelPool
	NewPool func(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error)

	// SelectServer picks which server to use for a key.
	// Receives the key and current server list from Servers.List().
	// If nil, uses JumpSelectServer (Jump Hash-based).
	// Alternative: DefaultSelectServer (CRC32-based, ~20ns faster).
	SelectServer SelectServerFunc

	// CircuitBreakerSettings configures the circuit breaker for each server pool.
	// If nil, no circuit breaker is used.
	// The Name field in the settings will be overridden with the server address.
	CircuitBreakerSettings *gobreaker.Settings
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	*Commands // Embedded command operations

	servers      Servers
	selectServer SelectServerFunc

	// Multi-pool management
	mu    sync.RWMutex
	pools map[string]*ServerPool

	// Pool configuration (same for all servers)
	config Config

	// Health check management
	stopHealthCheck chan struct{}
}

var _ Querier = (*Client)(nil)
var _ Executor = (*Client)(nil)
var _ BatchExecutor = (*Client)(nil)

// NewClient creates a new memcache client with the given servers and configuration.
// For a single server, use: NewClient(NewStaticServers("host:port"), config)
func NewClient(servers Servers, config Config) (*Client, error) {
	selectServer := config.SelectServer
	if selectServer == nil {
		selectServer = JumpSelectServer
	}

	// Validate servers
	serverList := servers.List()
	if len(serverList) == 0 {
		return nil, fmt.Errorf("no servers provided")
	}

	// Apply defaults to config (config is passed by value so we can mutate it)
	if config.Dialer == nil {
		config.Dialer = &net.Dialer{}
	}

	if config.NewPool == nil {
		config.NewPool = NewPuddlePool
	}

	client := &Client{
		servers:         servers,
		selectServer:    selectServer,
		pools:           make(map[string]*ServerPool),
		config:          config,
		stopHealthCheck: make(chan struct{}),
	}

	// Initialize embedded Commands with execute function
	client.Commands = NewCommands(client)

	// Start health check goroutine if enabled
	if config.HealthCheckInterval > 0 {
		go client.healthCheckLoop()
	}

	return client, nil
}

func (c *Client) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	sp, err := c.getPoolForKey(req.Key)
	if err != nil {
		return nil, err
	}
	return sp.Execute(ctx, req)
}

// ExecuteBatch executes multiple requests with automatic server routing.
// Requests are grouped by server and executed concurrently using pipelined requests.
// Returns responses in the same order as requests.
func (c *Client) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	// Group requests by server
	type serverBatch struct {
		serverAddr string
		reqs       []*meta.Request
		indices    []int // original indices in reqs slice
	}

	serverBatches := make(map[string]*serverBatch)
	for i, req := range reqs {
		addr, err := c.selectServerForKey(req.Key)
		if err != nil {
			return nil, err
		}

		batch, exists := serverBatches[addr]
		if !exists {
			batch = &serverBatch{
				serverAddr: addr,
				reqs:       make([]*meta.Request, 0),
				indices:    make([]int, 0),
			}
			serverBatches[addr] = batch
		}
		batch.reqs = append(batch.reqs, req)
		batch.indices = append(batch.indices, i)
	}

	// Prepare result slice
	results := make([]*meta.Response, len(reqs))

	// Execute batches concurrently per server
	var wg sync.WaitGroup
	errChan := make(chan error, len(serverBatches))

	for _, batch := range serverBatches {
		wg.Add(1)
		go func(b *serverBatch) {
			defer wg.Done()

			// Get pool for this server
			sp, err := c.getPoolForServer(b.serverAddr)
			if err != nil {
				errChan <- err
				return
			}

			// Execute batch using ServerPool.ExecuteBatch
			responses, err := sp.ExecuteBatch(ctx, b.reqs)
			if err != nil {
				errChan <- err
				return
			}

			// Place responses in correct positions
			for i, resp := range responses {
				if i >= len(b.indices) {
					break // Safety check
				}
				originalIdx := b.indices[i]
				results[originalIdx] = resp
			}
		}(batch)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	if err := <-errChan; err != nil {
		return nil, err
	}

	return results, nil
}

// Close closes the client and destroys all connections in all pools.
func (c *Client) Close() {
	// Stop health check goroutine if running
	if c.config.HealthCheckInterval > 0 {
		close(c.stopHealthCheck)
	}

	// Close all pools
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sp := range c.pools {
		sp.pool.Close()
	}
}

// selectServerForKey picks the server address for a given key.
// Uses the configured SelectServer function with the current server list.
func (c *Client) selectServerForKey(key string) (string, error) {
	servers := c.servers.List()
	return c.selectServer(key, servers)
}

// getPoolForKey returns the pool for the server that should handle this key.
// Creates pool lazily if it doesn't exist.
func (c *Client) getPoolForKey(key string) (*ServerPool, error) {
	addr, err := c.selectServerForKey(key)
	if err != nil {
		return nil, err
	}

	// Fast path: read lock
	c.mu.RLock()
	sp, exists := c.pools[addr]
	c.mu.RUnlock()
	if exists {
		return sp, nil
	}

	// Slow path: write lock and create
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if sp, exists := c.pools[addr]; exists {
		return sp, nil
	}

	// Create new pool
	sp, err = NewServerPool(addr, c.config)
	if err != nil {
		return nil, err
	}

	c.pools[addr] = sp
	return sp, nil
}

// healthCheckLoop periodically checks idle connections for health and lifecycle limits.
func (c *Client) healthCheckLoop() {
	ticker := time.NewTicker(c.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopHealthCheck:
			return
		case <-ticker.C:
			c.checkAllPools()
		}
	}
}

// checkAllPools runs health checks on all existing pools
func (c *Client) checkAllPools() {
	c.mu.RLock()
	pools := make([]*ServerPool, 0, len(c.pools))
	for _, sp := range c.pools {
		pools = append(pools, sp)
	}
	c.mu.RUnlock()

	for _, sp := range pools {
		c.checkPoolConnections(sp.pool)
	}
}

// checkPoolConnections checks all idle connections in a pool and destroys those that are stale or unhealthy.
func (c *Client) checkPoolConnections(pool Pool) {
	now := time.Now()

	for _, res := range pool.AcquireAllIdle() {
		// Check max connection lifetime
		if c.config.MaxConnLifetime > 0 && now.Sub(res.CreationTime()) > c.config.MaxConnLifetime {
			res.Destroy()
			continue
		}

		// Check max idle time
		if c.config.MaxConnIdleTime > 0 && res.IdleDuration() > c.config.MaxConnIdleTime {
			res.Destroy()
			continue
		}

		// Perform health check by sending a noop command
		if err := res.Value().Ping(); err != nil {
			res.Destroy()
			continue
		}

		res.ReleaseUnused()
	}
}

// getPoolForServer returns the pool for a specific server address.
// Creates the pool lazily if it doesn't exist.
func (c *Client) getPoolForServer(addr string) (*ServerPool, error) {
	// Fast path: read lock
	c.mu.RLock()
	sp, exists := c.pools[addr]
	c.mu.RUnlock()
	if exists {
		return sp, nil
	}

	// Slow path: write lock and create
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if sp, exists := c.pools[addr]; exists {
		return sp, nil
	}

	// Create new pool
	sp, err := NewServerPool(addr, c.config)
	if err != nil {
		return nil, err
	}

	c.pools[addr] = sp
	return sp, nil
}

// AllPoolStats returns stats for all server pools
func (c *Client) AllPoolStats() []ServerPoolStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := make([]ServerPoolStats, 0, len(c.pools))
	for _, sp := range c.pools {
		stats = append(stats, sp.Stats())
	}
	return stats
}

// ServerStats contains statistics from a single memcache server.
type ServerStats struct {
	Addr  string            // Server address
	Stats map[string]string // Server statistics (name -> value)
	Error error             // Error if stats request failed
}

// Stats retrieves statistics from all memcache servers.
// Sends a stats request to each server and collects the responses.
// Returns a slice of ServerStats, one per server.
// Individual server errors are returned in ServerStats.Error, not as a Go error.
func (c *Client) Stats(ctx context.Context, args ...string) ([]ServerStats, error) {
	servers := c.servers.List()
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers available")
	}

	// Collect stats from each server concurrently
	results := make([]ServerStats, len(servers))
	var wg sync.WaitGroup
	wg.Add(len(servers))

	for i, addr := range servers {
		go func(idx int, serverAddr string) {
			defer wg.Done()

			results[idx].Addr = serverAddr

			// Get pool for this server
			c.mu.RLock()
			sp, exists := c.pools[serverAddr]
			c.mu.RUnlock()

			if !exists {
				// Create pool for this server
				c.mu.Lock()
				if sp, exists = c.pools[serverAddr]; !exists {
					var err error
					sp, err = NewServerPool(serverAddr, c.config)
					if err != nil {
						results[idx].Error = err
						c.mu.Unlock()
						return
					}
					c.pools[serverAddr] = sp
				}
				c.mu.Unlock()
			}

			// Acquire connection
			res, err := sp.pool.Acquire(ctx)
			if err != nil {
				results[idx].Error = err
				return
			}

			conn := res.Value()

			// Execute stats command
			stats, err := conn.ExecuteStats(ctx, args...)
			if err != nil {
				if meta.ShouldCloseConnection(err) {
					res.Destroy()
				} else {
					res.Release()
				}
				results[idx].Error = err
				return
			}

			results[idx].Stats = stats
			res.Release()
		}(i, addr)
	}

	wg.Wait()
	return results, nil
}
