package memcache

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pior/memcache/meta"
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
	// If nil, no circuit breaker is used.
	NewCircuitBreaker func(serverAddr string) CircuitBreaker

	// for testing purposes only
	constructor func(ctx context.Context) (*Connection, error)
}

// serverPool wraps a pool with its server address.
type serverPool struct {
	addr           string
	pool           Pool
	circuitBreaker CircuitBreaker // nil if not configured
}

// poolConfig holds the pool configuration extracted from Config.
type poolConfig struct {
	maxSize             int32
	maxConnLifetime     time.Duration
	maxConnIdleTime     time.Duration
	healthCheckInterval time.Duration
	dialer              *net.Dialer
	poolFactory         func(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error)
	newCircuitBreaker   func(serverAddr string) CircuitBreaker         // nil if not configured
	constructor         func(ctx context.Context) (*Connection, error) // for testing
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	servers      Servers
	selectServer SelectServerFunc

	// Multi-pool management
	mu    sync.RWMutex
	pools map[string]*serverPool

	// Pool configuration (same for all servers)
	poolConfig poolConfig

	// Health check management
	stopHealthCheck chan struct{}

	stats *clientStatsCollector
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

	// Set up pool configuration
	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}

	poolFactory := config.Pool
	if poolFactory == nil {
		poolFactory = NewChannelPool
	}

	poolCfg := poolConfig{
		maxSize:             config.MaxSize,
		maxConnLifetime:     config.MaxConnLifetime,
		maxConnIdleTime:     config.MaxConnIdleTime,
		healthCheckInterval: config.HealthCheckInterval,
		dialer:              dialer,
		poolFactory:         poolFactory,
		newCircuitBreaker:   config.NewCircuitBreaker,
		constructor:         config.constructor,
	}

	client := &Client{
		servers:         servers,
		selectServer:    selectServer,
		pools:           make(map[string]*serverPool),
		poolConfig:      poolCfg,
		stopHealthCheck: make(chan struct{}),
		stats:           newClientStatsCollector(),
	}

	// Start health check goroutine if enabled
	if config.HealthCheckInterval > 0 {
		go client.healthCheckLoop()
	}

	return client, nil
}

// Close closes the client and destroys all connections in all pools.
func (c *Client) Close() {
	// Stop health check goroutine if running
	if c.poolConfig.healthCheckInterval > 0 {
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
func (c *Client) createPool(addr string) (Pool, CircuitBreaker, error) {
	constructor := c.poolConfig.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*Connection, error) {
			netConn, err := c.poolConfig.dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return NewConnection(netConn), nil
		}
	}

	pool, err := c.poolConfig.poolFactory(constructor, c.poolConfig.maxSize)
	if err != nil {
		return nil, nil, err
	}

	var cb CircuitBreaker
	if c.poolConfig.newCircuitBreaker != nil {
		cb = c.poolConfig.newCircuitBreaker(addr)
	}

	return pool, cb, nil
}

// healthCheckLoop periodically checks idle connections for health and lifecycle limits.
func (c *Client) healthCheckLoop() {
	ticker := time.NewTicker(c.poolConfig.healthCheckInterval)
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
		if c.poolConfig.maxConnLifetime > 0 && now.Sub(res.CreationTime()) > c.poolConfig.maxConnLifetime {
			res.Destroy()
			continue
		}

		// Check max idle time
		if c.poolConfig.maxConnIdleTime > 0 && res.IdleDuration() > c.poolConfig.maxConnIdleTime {
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
// If a circuit breaker is configured for the server pool, the request is wrapped with it.
func (c *Client) execRequest(ctx context.Context, sp *serverPool, req *meta.Request) (*meta.Response, error) {
	// If circuit breaker is configured, wrap the request
	if sp.circuitBreaker != nil {
		resp, err := sp.circuitBreaker.Execute(func() (*meta.Response, error) {
			return c.execRequestDirect(ctx, sp.pool, req)
		})
		if err != nil {
			c.stats.recordError()
			return nil, err
		}
		return resp, nil
	}

	// No circuit breaker, execute directly
	return c.execRequestDirect(ctx, sp.pool, req)
}

// execRequestDirect performs the actual request execution without circuit breaker.
func (c *Client) execRequestDirect(ctx context.Context, pool Pool, req *meta.Request) (*meta.Response, error) {
	resource, err := pool.Acquire(ctx)
	if err != nil {
		c.stats.recordError()
		return nil, err
	}

	conn := resource.Value()

	resp, err := conn.Send(req)
	if err != nil {
		c.stats.recordError()
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

// Get retrieves a single item from memcache.
func (c *Client) Get(ctx context.Context, key string) (Item, error) {
	sp, err := c.getPoolForKey(key)
	if err != nil {
		c.stats.recordError()
		return Item{}, err
	}

	req := meta.NewRequest(meta.CmdGet, key, nil, []meta.Flag{{Type: meta.FlagReturnValue}})
	resp, err := c.execRequest(ctx, sp, req)
	if err != nil {
		return Item{}, err
	}

	if resp.IsMiss() {
		c.stats.recordGet(false)
		return Item{Key: key, Found: false}, nil
	}

	if resp.HasError() {
		c.stats.recordError()
		return Item{}, resp.Error
	}

	if !resp.IsSuccess() {
		c.stats.recordError()
		return Item{}, fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	c.stats.recordGet(true)
	return Item{
		Key:   key,
		Value: resp.Data,
		Found: true,
	}, nil
}

// Set stores an item in memcache.
func (c *Client) Set(ctx context.Context, item Item) error {
	sp, err := c.getPoolForKey(item.Key)
	if err != nil {
		c.stats.recordError()
		return err
	}

	// Build flags - mode is Set by default, no need to specify
	var flags []meta.Flag

	// Add TTL flag if specified, otherwise use no expiration
	if item.TTL > 0 {
		flags = []meta.Flag{meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds()))}
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.execRequest(ctx, sp, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		c.stats.recordError()
		return resp.Error
	}

	if !resp.IsSuccess() {
		c.stats.recordError()
		return fmt.Errorf("set failed with status: %s", resp.Status)
	}

	c.stats.recordSet()
	return nil
}

// Add stores an item in memcache only if the key doesn't already exist.
func (c *Client) Add(ctx context.Context, item Item) error {
	sp, err := c.getPoolForKey(item.Key)
	if err != nil {
		c.stats.recordError()
		return err
	}

	// Build flags
	flags := []meta.Flag{
		{Type: meta.FlagMode, Token: string(meta.ModeAdd)},
	}

	if item.TTL > 0 {
		flags = append(flags, meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds())))
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.execRequest(ctx, sp, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		c.stats.recordError()
		return resp.Error
	}

	if resp.IsNotStored() {
		c.stats.recordError()
		return fmt.Errorf("key already exists")
	}

	if !resp.IsSuccess() {
		c.stats.recordError()
		return fmt.Errorf("add failed with status: %s", resp.Status)
	}

	c.stats.recordAdd()
	return nil
}

// Delete removes an item from memcache.
func (c *Client) Delete(ctx context.Context, key string) error {
	sp, err := c.getPoolForKey(key)
	if err != nil {
		c.stats.recordError()
		return err
	}

	req := meta.NewRequest(meta.CmdDelete, key, nil, nil)
	resp, err := c.execRequest(ctx, sp, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		c.stats.recordError()
		return resp.Error
	}

	// Delete is successful even if key doesn't exist
	if resp.Status != meta.StatusHD && resp.Status != meta.StatusNF {
		c.stats.recordError()
		return fmt.Errorf("delete failed with status: %s", resp.Status)
	}

	c.stats.recordDelete()
	return nil
}

// Increment increments a counter key by the given delta.
// Creates the key with the delta value if it doesn't exist.
// This uses auto-vivify (N flag) with initial value (J flag) set to the delta,
// so the returned value is correct even on first call.
// TTL of 0 means infinite TTL.
func (c *Client) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	sp, err := c.getPoolForKey(key)
	if err != nil {
		c.stats.recordError()
		return 0, err
	}

	// Build flags for increment/decrement with auto-vivify
	var flags []meta.Flag

	// Calculate TTL in seconds for vivify flag
	ttlSeconds := int64(0)
	if ttl > 0 {
		ttlSeconds = int64(ttl.Seconds())
	}

	if delta >= 0 {
		// Positive delta - use increment mode (default)
		flags = []meta.Flag{
			{Type: meta.FlagReturnValue},
			{Type: meta.FlagDelta, Token: strconv.FormatInt(delta, 10)},
			{Type: meta.FlagInitialValue, Token: strconv.FormatInt(delta, 10)}, // Initialize to delta on creation
			{Type: meta.FlagVivify, Token: strconv.FormatInt(ttlSeconds, 10)},  // Auto-create with specified TTL
		}
	} else {
		// Negative delta - use decrement mode with absolute value
		// For decrement, initialize to 0 since we can't have negative counters
		flags = []meta.Flag{
			{Type: meta.FlagReturnValue},
			{Type: meta.FlagDelta, Token: strconv.FormatInt(-delta, 10)}, // Use absolute value
			{Type: meta.FlagMode, Token: string(meta.ModeDecrement)},
			{Type: meta.FlagInitialValue, Token: "0"},                         // Initialize to 0 on creation
			{Type: meta.FlagVivify, Token: strconv.FormatInt(ttlSeconds, 10)}, // Auto-create with specified TTL
		}
	}

	// Add TTL flag to update TTL on existing keys if TTL > 0
	if ttl > 0 {
		flags = append(flags, meta.Flag{Type: meta.FlagTTL, Token: strconv.FormatInt(ttlSeconds, 10)})
	}

	req := meta.NewRequest(meta.CmdArithmetic, key, nil, flags)
	resp, err := c.execRequest(ctx, sp, req)
	if err != nil {
		return 0, err
	}

	if resp.HasError() {
		c.stats.recordError()
		return 0, resp.Error
	}

	if !resp.IsSuccess() {
		c.stats.recordError()
		return 0, fmt.Errorf("increment failed with status: %s", resp.Status)
	}

	// Parse the returned value
	if !resp.HasValue() {
		c.stats.recordError()
		return 0, fmt.Errorf("increment response missing value")
	}

	value, err := strconv.ParseInt(string(resp.Data), 10, 64)
	if err != nil {
		c.stats.recordError()
		return 0, fmt.Errorf("failed to parse increment result: %w", err)
	}

	c.stats.recordIncrement()
	return value, nil
}

// Stats returns a snapshot of client statistics.
func (c *Client) Stats() ClientStats {
	return c.stats.snapshot()
}

// ServerPoolStats contains stats for a single server pool
type ServerPoolStats struct {
	Addr                string
	PoolStats           PoolStats
	CircuitBreakerState CircuitBreakerState
}

// AllPoolStats returns stats for all server pools
func (c *Client) AllPoolStats() []ServerPoolStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := make([]ServerPoolStats, 0, len(c.pools))
	for _, sp := range c.pools {
		s := ServerPoolStats{
			Addr:      sp.addr,
			PoolStats: sp.pool.Stats(),
		}
		if sp.circuitBreaker != nil {
			s.CircuitBreakerState = sp.circuitBreaker.State()
		}
		stats = append(stats, s)
	}
	return stats
}
