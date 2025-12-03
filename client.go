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

	// Dialer is the net.Dialer used to create new connections.
	// If nil, the default net.Dialer is used.
	Dialer Dialer

	// NewPool is the connection pool factory function.
	// If nil, uses the default channel-based pool (fastest).
	// To use puddle pool (requires -tags=puddle): NewPool: memcache.NewPuddlePool
	NewPool func(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error)

	// SelectServer picks which server to use for a key.
	// Receives the key and current server list from Servers.List().
	// If nil, uses DefaultSelectServer (CRC32-based).
	SelectServer SelectServerFunc

	// NewCircuitBreaker creates a circuit breaker for a server.
	// Called once per server address when the pool is created.
	// If nil, a no-op circuit breaker is used.
	NewCircuitBreaker func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response]
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

// NewClient creates a new memcache client with the given servers and configuration.
// For a single server, use: NewClient(NewStaticServers("host:port"), config)
func NewClient(servers Servers, config Config) (*Client, error) {
	selectServer := config.SelectServer
	if selectServer == nil {
		selectServer = DefaultSelectServer
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
		config.NewPool = NewChannelPool
	}

	if config.NewCircuitBreaker == nil {
		config.NewCircuitBreaker = func(string) *gobreaker.CircuitBreaker[*meta.Response] {
			return nil
		}
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

// MultiGet retrieves multiple items from memcache in a single pipelined request per server.
// Keys are automatically grouped by server and requests are sent concurrently.
// Returns items in the same order as the input keys.
// Missing keys have Found=false in the returned Item.
func (c *Client) MultiGet(ctx context.Context, keys []string) ([]Item, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Group keys by server
	type serverBatch struct {
		serverAddr string
		keys       []string
		indices    []int // original indices in keys slice
	}

	serverBatches := make(map[string]*serverBatch)
	for i, key := range keys {
		addr, err := c.selectServerForKey(key)
		if err != nil {
			return nil, err
		}

		batch, exists := serverBatches[addr]
		if !exists {
			batch = &serverBatch{
				serverAddr: addr,
				keys:       make([]string, 0),
				indices:    make([]int, 0),
			}
			serverBatches[addr] = batch
		}
		batch.keys = append(batch.keys, key)
		batch.indices = append(batch.indices, i)
	}

	// Prepare result slice
	results := make([]Item, len(keys))

	// Execute batches concurrently per server
	var wg sync.WaitGroup
	errChan := make(chan error, len(serverBatches))

	for _, batch := range serverBatches {
		wg.Add(1)
		go func(b *serverBatch) {
			defer wg.Done()

			// Get or create pool for this server
			sp, err := c.getPoolForServer(b.serverAddr)
			if err != nil {
				errChan <- err
				return
			}

			// Build requests
			reqs := make([]*meta.Request, len(b.keys))
			for i, key := range b.keys {
				reqs[i] = meta.NewRequest(meta.CmdGet, key, nil, []meta.Flag{{Type: meta.FlagReturnValue}})
			}

			// Execute batch
			responses, err := sp.ExecuteBatch(ctx, reqs)
			if err != nil {
				errChan <- err
				return
			}

			// Process responses and place in correct position
			for i, resp := range responses {
				if i >= len(b.keys) {
					break // Safety check
				}

				originalIdx := b.indices[i]
				key := b.keys[i]

				if resp.HasError() {
					errChan <- resp.Error
					return
				}

				if resp.IsMiss() {
					results[originalIdx] = Item{Key: key, Found: false}
				} else if resp.IsSuccess() {
					results[originalIdx] = Item{
						Key:   key,
						Value: resp.Data,
						Found: true,
					}
				} else {
					errChan <- fmt.Errorf("unexpected response status for key %s: %s", key, resp.Status)
					return
				}
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

// getPoolForServer is like getPoolForKey but takes a server address directly
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
