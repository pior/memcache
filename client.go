package memcache

import (
	"context"
	"fmt"
	"net"
	"strconv"
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

	// for testing purposes only
	constructor func(ctx context.Context) (*Connection, error)
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	servers             Servers
	selectServer        SelectServerFunc
	maxConnLifetime     time.Duration
	maxConnIdleTime     time.Duration
	healthCheckInterval time.Duration

	pool            Pool
	stopHealthCheck chan struct{}
	stats           *clientStatsCollector
}

var _ Querier = (*Client)(nil)

// NewClient creates a new memcache client with the given servers and configuration.
// For a single server, use: NewClient(NewStaticServers("host:port"), config)
func NewClient(servers Servers, config Config) (*Client, error) {
	selectServer := config.SelectServer
	if selectServer == nil {
		selectServer = DefaultSelectServer
	}

	// For now, we use the first server for the single pool (PR1).
	// In PR2, we'll create pools per server dynamically.
	serverList := servers.List()
	if len(serverList) == 0 {
		return nil, fmt.Errorf("no servers provided")
	}
	addr := serverList[0]

	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}

	constructor := config.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*Connection, error) {
			netConn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return NewConnection(netConn), nil
		}
	}

	// Create pool using provided factory or default to channel pool
	poolFactory := config.Pool
	if poolFactory == nil {
		poolFactory = NewChannelPool
	}

	pool, err := poolFactory(constructor, config.MaxSize)
	if err != nil {
		return nil, err
	}

	client := &Client{
		servers:             servers,
		selectServer:        selectServer,
		pool:                pool,
		maxConnLifetime:     config.MaxConnLifetime,
		maxConnIdleTime:     config.MaxConnIdleTime,
		healthCheckInterval: config.HealthCheckInterval,
		stopHealthCheck:     make(chan struct{}),
		stats:               newClientStatsCollector(),
	}

	// Start health check goroutine if enabled
	if config.HealthCheckInterval > 0 {
		go client.healthCheckLoop()
	}

	return client, nil
}

// Close closes the client and destroys all connections in the pool.
func (c *Client) Close() {
	// Stop health check goroutine if running
	if c.healthCheckInterval > 0 {
		close(c.stopHealthCheck)
	}
	c.pool.Close()
}

// selectServerForKey picks the server address for a given key.
// Uses the configured SelectServer function with the current server list.
func (c *Client) selectServerForKey(key string) (string, error) {
	servers := c.servers.List()
	return c.selectServer(key, servers)
}

// healthCheckLoop periodically checks idle connections for health and lifecycle limits.
func (c *Client) healthCheckLoop() {
	ticker := time.NewTicker(c.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopHealthCheck:
			return
		case <-ticker.C:
			c.checkIdleConnections()
		}
	}
}

// checkIdleConnections checks all idle connections and destroys those that are stale or unhealthy.
func (c *Client) checkIdleConnections() {
	now := time.Now()

	for _, res := range c.pool.AcquireAllIdle() {
		// Check max connection lifetime
		if c.maxConnLifetime > 0 && now.Sub(res.CreationTime()) > c.maxConnLifetime {
			res.Destroy()
			continue
		}

		// Check max idle time
		if c.maxConnIdleTime > 0 && res.IdleDuration() > c.maxConnIdleTime {
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
func (c *Client) execRequest(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	resource, err := c.pool.Acquire(ctx)
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
	req := meta.NewRequest(meta.CmdGet, key, nil, []meta.Flag{{Type: meta.FlagReturnValue}})
	resp, err := c.execRequest(ctx, req)
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
	// Build flags - mode is Set by default, no need to specify
	var flags []meta.Flag

	// Add TTL flag if specified, otherwise use no expiration
	if item.TTL > 0 {
		flags = []meta.Flag{meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds()))}
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.execRequest(ctx, req)
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
	// Build flags
	flags := []meta.Flag{
		{Type: meta.FlagMode, Token: string(meta.ModeAdd)},
	}

	if item.TTL > 0 {
		flags = append(flags, meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds())))
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.execRequest(ctx, req)
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
	req := meta.NewRequest(meta.CmdDelete, key, nil, nil)
	resp, err := c.execRequest(ctx, req)
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
	resp, err := c.execRequest(ctx, req)
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

// PoolStats returns a snapshot of connection pool statistics.
func (c *Client) PoolStats() PoolStats {
	return c.pool.Stats()
}
