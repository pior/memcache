package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/jackc/puddle/v2"
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

	// for testing purposes only
	constructor func(ctx context.Context) (*conn, error)
}

// conn wraps a network connection with a buffered reader for efficient response parsing.
type conn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *conn) send(req *meta.Request) (*meta.Response, error) {
	if err := meta.WriteRequest(c, req); err != nil {
		return nil, err
	}

	resp, err := meta.ReadResponse(c.reader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	addr                string
	maxConnLifetime     time.Duration
	maxConnIdleTime     time.Duration
	healthCheckInterval time.Duration

	pool            *puddle.Pool[*conn]
	stopHealthCheck chan struct{}
}

var _ Querier = (*Client)(nil)

// NewClient creates a new memcache client with the given address and configuration.
// The address should be in the format "host:port".
func NewClient(addr string, config Config) (*Client, error) {
	dialer := config.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}

	constructor := config.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*conn, error) {
			netConn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return &conn{
				Conn:   netConn,
				reader: bufio.NewReader(netConn),
			}, nil
		}
	}

	poolConfig := &puddle.Config[*conn]{
		Constructor: constructor,
		Destructor:  func(c *conn) { c.Close() },
		MaxSize:     config.MaxSize,
	}

	pool, err := puddle.NewPool(poolConfig)
	if err != nil {
		return nil, err
	}

	client := &Client{
		addr:                addr,
		pool:                pool,
		maxConnLifetime:     config.MaxConnLifetime,
		maxConnIdleTime:     config.MaxConnIdleTime,
		healthCheckInterval: config.HealthCheckInterval,
		stopHealthCheck:     make(chan struct{}),
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
func (c *Client) healthCheck(conn *conn) error {
	req := meta.NewRequest(meta.CmdNoOp, "", nil, nil)

	resp, err := conn.send(req)
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
		return nil, err
	}

	conn := resource.Value()

	resp, err := conn.send(req)
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

// Get retrieves a single item from memcache.
func (c *Client) Get(ctx context.Context, key string) (Item, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil, []meta.Flag{{Type: meta.FlagReturnValue}})
	resp, err := c.execRequest(ctx, req)
	if err != nil {
		return Item{}, err
	}

	if resp.IsMiss() {
		return Item{Key: key, Found: false}, nil
	}

	if resp.HasError() {
		return Item{}, resp.Error
	}

	if !resp.IsSuccess() {
		return Item{}, fmt.Errorf("unexpected response status: %s", resp.Status)
	}

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
		return resp.Error
	}

	if !resp.IsSuccess() {
		return fmt.Errorf("set failed with status: %s", resp.Status)
	}

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
		return resp.Error
	}

	if resp.IsNotStored() {
		return fmt.Errorf("key already exists")
	}

	if !resp.IsSuccess() {
		return fmt.Errorf("add failed with status: %s", resp.Status)
	}

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
		return resp.Error
	}

	// Delete is successful even if key doesn't exist
	if resp.Status != meta.StatusHD && resp.Status != meta.StatusNF {
		return fmt.Errorf("delete failed with status: %s", resp.Status)
	}

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
		return 0, resp.Error
	}

	if !resp.IsSuccess() {
		return 0, fmt.Errorf("increment failed with status: %s", resp.Status)
	}

	// Parse the returned value
	if !resp.HasValue() {
		return 0, fmt.Errorf("increment response missing value")
	}

	value, err := strconv.ParseInt(string(resp.Data), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse increment result: %w", err)
	}

	return value, nil
}
