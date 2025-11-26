package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
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

	// Dialer opens network connections.
	// If nil, the default net.Dialer is used.
	Dialer Dialer

	// Pool is the connection pool factory function.
	// If nil, uses the default channel-based pool (fastest).
	Pool PoolFactory

	// ServerSelector is the function used to select a server for a given key.
	// If nil, uses consistent hashing.
	ServerSelector ServerSelector

	// For testing only: function to create new connections.
	newConn ConnectionFactory
}

type ConnectionFactory func(ctx context.Context, addr netip.AddrPort) (*Connection, error)
type PoolFactory func(newConn ConnectionFactory, addr netip.AddrPort, maxSize int32) (Pool, error)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	serverSelector ServerSelector
	servers        Servers
	newPool        PoolFactory
	// For testing only: function to create new connections.
	newConn ConnectionFactory

	// pool configuration
	maxSize             int32
	maxConnLifetime     time.Duration
	maxConnIdleTime     time.Duration
	healthCheckInterval time.Duration

	pools      map[netip.Addr]Pool
	poolsMutex sync.RWMutex

	stopHealthCheck chan struct{}
	stats           *clientStatsCollector

	*clientQueries
}

var _ Querier = (*Client)(nil)

// NewClient creates a new memcache client with the given address and configuration.
// The address should be in the format "host:port".
func NewClient(servers Servers, config Config) (*Client, error) {
	if servers == nil {
		return nil, fmt.Errorf("servers cannot be nil")
	}

	if config.ServerSelector == nil {
		config.ServerSelector = ServerSelectorDefault
	}

	if config.Dialer == nil {
		config.Dialer = &net.Dialer{}
	}

	if config.Pool == nil {
		config.Pool = NewChannelPool
	}

	if config.newConn == nil {
		config.newConn = func(ctx context.Context, addr netip.Addr) (*Connection, error) {
			netConn, err := config.Dialer.DialContext(ctx, "tcp", addr.String())
			if err != nil {
				return nil, err
			}
			return &Connection{Conn: netConn, Reader: bufio.NewReader(netConn)}, nil
		}
	}

	client := &Client{
		servers:        servers,
		serverSelector: config.ServerSelector,
		newPool:        config.Pool,

		maxSize:             config.MaxSize,
		maxConnLifetime:     config.MaxConnLifetime,
		maxConnIdleTime:     config.MaxConnIdleTime,
		healthCheckInterval: config.HealthCheckInterval,

		pools:      make(map[netip.Addr]Pool),
		poolsMutex: sync.RWMutex{},

		stopHealthCheck: make(chan struct{}),
		stats:           newClientStatsCollector(),
	}

	client.clientQueries = &clientQueries{
		executor: client.execRequest,
		stats:    client.stats,
	}

	if config.HealthCheckInterval > 0 {
		go client.healthCheckLoop()
	}

	return client, nil
}

// Close closes the client and destroys all connections in the pool.
// It must be called only once.
func (c *Client) Close() {
	// Stop health check goroutine if running
	if c.healthCheckInterval > 0 {
		close(c.stopHealthCheck)
	}

	c.poolsMutex.Lock()
	for _, pool := range c.pools {
		pool.Close()
	}
	c.pools = nil
	c.poolsMutex.Unlock()
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
			for _, pool := range c.pools {
				c.checkIdleConnections(pool)
			}
		}
	}
}

// checkIdleConnections checks all idle connections and destroys those that are stale or unhealthy.
func (c *Client) checkIdleConnections(pool Pool) {
	now := time.Now()

	for _, res := range pool.AcquireAllIdle() {
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

func (c *Client) getPoolForAddr(addr netip.Addr) (Pool, error) {
	c.poolsMutex.RLock()
	pool, exists := c.pools[addr]
	c.poolsMutex.RUnlock()
	if exists {
		return pool, nil
	}

	c.poolsMutex.Lock()
	pool, exists = c.pools[addr]
	if exists {
		c.poolsMutex.Unlock()
		return pool, nil
	}

	var err error
	pool, err = c.newPool(c.newConn, addr, c.maxSize)
	if err != nil {
		c.poolsMutex.Unlock()
		return nil, err
	}

	c.pools[addr] = pool
	c.poolsMutex.Unlock()
	return pool, nil
}

// execRequest executes a single request-response cycle with proper connection management.
// It handles acquiring a connection, sending the request, reading the response, and
// releasing/destroying the connection based on error conditions.
func (c *Client) execRequest(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	servers := c.servers.List()

	server := c.serverSelector(req.Key, servers)
	pool, err := c.getPoolForAddr(server)

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

// Stats returns a snapshot of client statistics.
func (c *Client) Stats() ClientStats {
	return c.stats.snapshot()
}

// PoolStats returns a snapshot of connection pool statistics.
func (c *Client) PoolStats() []PoolStats {
	var stats []PoolStats

	c.poolsMutex.RLock()
	pools := c.pools
	c.poolsMutex.RUnlock()

	for _, pool := range pools {
		stats = append(stats, pool.Stats())
	}

	return stats
}
