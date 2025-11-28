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

type Querier interface {
	Get(ctx context.Context, key string) (Item, error)
	Set(ctx context.Context, item Item) error
	Add(ctx context.Context, item Item) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
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
	Dialer *net.Dialer

	// Pool is the connection pool factory function.
	// If nil, uses the default channel-based pool (fastest).
	// To use puddle pool (requires -tags=puddle): Pool: memcache.NewPuddlePool
	Pool func(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error)

	// SelectServer picks which server to use for a key.
	// Receives the key and current server list from Servers.List().
	// If nil, uses DefaultSelectServer (CRC32-based).
	SelectServer SelectServerFunc

	// NewCircuitBreaker creates a circuit breaker for a server.
	// Called once per server address when the pool is created.
	// If nil, a no-op circuit breaker is used.
	NewCircuitBreaker func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response]

	// for testing purposes only
	constructor func(ctx context.Context) (*Connection, error)
}

// serverPool wraps a pool with its server address.
type serverPool struct {
	addr           string
	pool           Pool
	circuitBreaker *gobreaker.CircuitBreaker[*meta.Response]
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	*Commands // Embedded command operations

	servers      Servers
	selectServer SelectServerFunc

	// Multi-pool management
	mu    sync.RWMutex
	pools map[string]*serverPool

	// Pool configuration (same for all servers)
	config Config

	// Health check management
	stopHealthCheck chan struct{}
}

var _ Querier = (*Client)(nil)

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

	if config.Pool == nil {
		config.Pool = NewChannelPool
	}

	// Use default no-op circuit breaker if none configured
	if config.NewCircuitBreaker == nil {
		config.NewCircuitBreaker = func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response] {
			return defaultCircuitBreaker
		}
	}

	client := &Client{
		servers:         servers,
		selectServer:    selectServer,
		pools:           make(map[string]*serverPool),
		config:          config,
		stopHealthCheck: make(chan struct{}),
	}

	// Create execute function for commands
	// This wraps server selection and request execution
	executeFunc := func(ctx context.Context, key string, req *meta.Request) (*meta.Response, error) {
		sp, err := client.getPoolForKey(key)
		if err != nil {
			return nil, err
		}
		return client.execRequest(ctx, sp, req)
	}

	// Initialize embedded Commands with execute function
	client.Commands = NewCommands(executeFunc)

	// Start health check goroutine if enabled
	if config.HealthCheckInterval > 0 {
		go client.healthCheckLoop()
	}

	return client, nil
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
func (c *Client) getPoolForKey(key string) (*serverPool, error) {
	addr, err := c.selectServerForKey(key)
	if err != nil {
		return nil, err
	}
	return c.getOrCreatePool(addr)
}

// getOrCreatePool gets or creates a pool for the given server address.
func (c *Client) getOrCreatePool(addr string) (*serverPool, error) {
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
	pool, cb, err := c.createPool(addr)
	if err != nil {
		return nil, err
	}

	sp = &serverPool{
		addr:           addr,
		pool:           pool,
		circuitBreaker: cb,
	}
	c.pools[addr] = sp
	return sp, nil
}

// createPool creates a new connection pool for a server
func (c *Client) createPool(addr string) (Pool, *gobreaker.CircuitBreaker[*meta.Response], error) {
	constructor := c.config.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*Connection, error) {
			netConn, err := c.config.Dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return NewConnection(netConn), nil
		}
	}

	pool, err := c.config.Pool(constructor, c.config.MaxSize)
	if err != nil {
		return nil, nil, err
	}

	cb := c.config.NewCircuitBreaker(addr)

	return pool, cb, nil
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
	pools := make([]*serverPool, 0, len(c.pools))
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
		if err := c.healthCheck(res.Value()); err != nil {
			res.Destroy()
			continue
		}

		res.ReleaseUnused()
	}
}

// healthCheck performs a simple health check on a connection using the noop command.
func (c *Client) healthCheck(conn *Connection) error {
	req := meta.NewRequest(meta.CmdNoOp, "", nil, nil)

	resp, err := conn.Send(req)
	if err != nil {
		return err
	}

	if resp.Status != meta.StatusMN {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}

	return nil
}

// execRequest executes a single request-response cycle with proper connection management.
// It handles acquiring a connection, sending the request, reading the response, and
// releasing/destroying the connection based on error conditions.
// The request is wrapped with the server's circuit breaker.
func (c *Client) execRequest(ctx context.Context, sp *serverPool, req *meta.Request) (*meta.Response, error) {
	resp, err := sp.circuitBreaker.Execute(func() (*meta.Response, error) {
		return c.execRequestDirect(ctx, sp.pool, req)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// execRequestDirect performs the actual request execution without circuit breaker.
func (c *Client) execRequestDirect(ctx context.Context, pool Pool, req *meta.Request) (*meta.Response, error) {
	resource, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	conn := resource.Value()

	resp, err := conn.Send(req)
	if err != nil {
		if meta.ShouldCloseConnection(err) {
			resource.Destroy()
		} else {
			resource.Release()
		}
		return nil, err
	}

	resource.Release()
	return resp, nil
}

// ServerPoolStats contains stats for a single server pool
type ServerPoolStats struct {
	Addr                 string
	PoolStats            PoolStats
	CircuitBreakerState  gobreaker.State
	CircuitBreakerCounts gobreaker.Counts
}

// AllPoolStats returns stats for all server pools
func (c *Client) AllPoolStats() []ServerPoolStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := make([]ServerPoolStats, 0, len(c.pools))
	for _, sp := range c.pools {
		stats = append(stats, ServerPoolStats{
			Addr:                 sp.addr,
			PoolStats:            sp.pool.Stats(),
			CircuitBreakerState:  sp.circuitBreaker.State(),
			CircuitBreakerCounts: sp.circuitBreaker.Counts(),
		})
	}
	return stats
}
