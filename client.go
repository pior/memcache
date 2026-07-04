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

// Dialer establishes the network connections used by the client's pools.
// *net.Dialer satisfies this interface.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type Item struct {
	Key   string
	Value []byte
	TTL   TTL
	Found bool // indicates whether the key was found in cache
}

// Config holds configuration for the memcache client connection pool.
type Config struct {
	// MaxSize is the maximum number of connections in the pool.
	// Default: 10
	// Required: must be > 0.
	MaxSize int32

	// MaxConnLifetime is the maximum duration a connection can be reused.
	// Enforced when a connection is checked out of the pool, when it is
	// returned after an operation, and by the health check loop for idle
	// connections.
	// Zero means no limit.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the maximum duration a connection can be idle before
	// being closed. Enforced when a connection is checked out of the pool and
	// by the health check loop.
	//
	// Note that checkout-time enforcement needs traffic to act: a pool that
	// receives no operations at all only shrinks when the health check loop is
	// running (HealthCheckInterval > 0).
	// Zero means no limit.
	MaxConnIdleTime time.Duration

	// HealthCheckInterval is how often to proactively check idle connections:
	// each pass pings idle connections and closes broken ones and those past
	// MaxConnLifetime/MaxConnIdleTime, even when no operations are flowing.
	// Zero disables the loop; lifetime and idle limits are then only enforced
	// when connections are checked out or returned.
	HealthCheckInterval time.Duration

	// IdleConnCheckThreshold controls the on-acquire liveness check: when a
	// connection that has been idle at least this long is checked out of the
	// pool, it is verified with a cheap non-blocking probe and transparently
	// replaced if the server, a load balancer, or a middlebox closed it while
	// it sat idle. This keeps a rolling restart or an idle-timed-out flow from
	// surfacing as a burst of failed operations on the next request.
	//
	// Freshly established and actively cycling connections (idle less than the
	// threshold) are trusted without a syscall, so the hot path is unaffected.
	// The probe cannot see through TLS, so TLS connections are always trusted
	// and rely on the next operation to detect a dead peer.
	//
	// Zero selects a sensible default (see defaultIdleConnCheckThreshold); a
	// negative value disables the check.
	IdleConnCheckThreshold time.Duration

	// Timeout is the per-operation timeout for memcache operations (read/write).
	// It acts as an upper bound on every operation: the effective deadline is the
	// earlier of the context deadline and now+Timeout. A context deadline sooner
	// than Timeout still wins; a later one (or a context with no deadline) is
	// capped at Timeout. This ensures a long-lived context (e.g. a request- or
	// job-scoped one) cannot leave an operation unbounded, so a hung-but-connected
	// server fails fast instead of stalling the client.
	//
	// Timeout bounds socket I/O only. It does not bound waiting for a
	// connection from a saturated pool: that wait is bounded solely by the
	// caller's context, so pass contexts with deadlines (see the Timeouts
	// section in the package documentation).
	//
	// Zero selects a conservative default (see defaultOperationTimeout); a
	// negative value disables the cap so the operation is bounded only by the
	// context (not recommended: a hung-but-connected server then stalls every
	// operation whose context has no deadline).
	// Recommended: 100ms-1s depending on your latency requirements.
	Timeout time.Duration

	// ConnectTimeout is the timeout for establishing new connections.
	// This includes TCP handshake and TLS handshake if applicable.
	// If zero, uses Timeout value.
	// Set this higher than Timeout if TLS connections take longer to establish.
	ConnectTimeout time.Duration

	// Dialer is used to create new connections. If nil, a default
	// net.Dialer is used.
	//
	// To connect over TLS (memcached running with --enable-ssl), set a
	// *tls.Dialer, which satisfies this interface:
	//
	//	Dialer: &tls.Dialer{Config: &tls.Config{RootCAs: pool}}
	//
	// A single *tls.Dialer serves a whole multi-server set: crypto/tls
	// derives ServerName from each dial address when Config.ServerName is
	// empty, so every server is verified against its own hostname. To tune
	// the underlying TCP connection (timeouts, keep-alive) as well, set
	// tls.Dialer.NetDialer. For per-server TLS settings, supply a Dialer that
	// selects its config based on the address.
	Dialer Dialer

	// ServerSelector picks which server to use for a key.
	// It receives the key and current server set and returns the selected server.
	// The default implementation uses Rendezvous Hash for membership-stable selection.
	ServerSelector ServerSelector

	// CircuitBreakerSettings configures the circuit breaker for each server pool.
	// If nil, no circuit breaker is used.
	// The Name field in the settings will be overridden with the server address.
	CircuitBreakerSettings *gobreaker.Settings

	// Observer is notified around each operation for tracing and metrics.
	// If nil, no observation is performed. See the otelmemcache subpackage for
	// a ready-made OpenTelemetry adapter.
	Observer Observer
}

// defaultOperationTimeout is the default for Config.Timeout. One second is far
// above healthy memcached latencies (sub-millisecond to low milliseconds), so
// it never constrains a working server; it exists so that the default
// configuration is never unbounded — the stress soak showed that a
// hung-but-connected server otherwise stalls every operation whose context
// carries no deadline. Latency-sensitive deployments should set a much lower
// Timeout explicitly.
const defaultOperationTimeout = time.Second

// defaultIdleConnCheckThreshold is the default for Config.IdleConnCheckThreshold.
// One second sits well above the sub-millisecond idle gaps of a pool under load
// (so the hot path pays nothing) and well below the idle timeouts that reset a
// connection (server restart, LB/middlebox idle timeout), so genuinely idle
// connections are the ones probed.
const defaultIdleConnCheckThreshold = time.Second

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	*Commands // Embedded command operations

	servers Servers

	// Multi-pool management
	mu     sync.RWMutex
	pools  map[string]*ServerPool
	closed bool

	config Config

	// Health check management
	stopHealthCheck chan struct{}
	closeOnce       sync.Once
}

var _ Querier = (*Client)(nil)
var _ Executor = (*Client)(nil)
var _ BatchExecutor = (*Client)(nil)

// NewClient creates a new memcache client with the given servers and configuration.
// For a single server, use: NewClient(StaticServers("host:port"), config)
func NewClient(servers Servers, config Config) *Client {
	if servers == nil {
		servers = StaticServers()
	}

	if config.MaxSize <= 0 {
		config.MaxSize = 10
	}
	if config.Timeout == 0 {
		config.Timeout = defaultOperationTimeout
	}
	if config.IdleConnCheckThreshold == 0 {
		config.IdleConnCheckThreshold = defaultIdleConnCheckThreshold
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = config.Timeout
	}
	if config.ServerSelector == nil {
		config.ServerSelector = StableServerSelector
	}
	if config.Dialer == nil {
		config.Dialer = &net.Dialer{}
	}
	if config.Observer == nil {
		config.Observer = noopObserver{}
	}

	client := &Client{
		servers:         servers,
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

	return client
}

// Execute implements the Executor interface with automatic server routing.
// See Executor for the consume contract: the response is only valid during the
// consume call.
func (c *Client) Execute(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) (err error) {
	addr, err := c.selectServerForKey(req.Key)
	if err != nil {
		return err
	}

	// The observer runs after the response is released, so capture the fields
	// it needs (both safe to retain, unlike the response's buffers).
	var status meta.StatusType
	var respErr error

	ctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: string(req.Command), Server: addr, Key: req.Key})
	defer func() {
		op.End(OpResult{
			Result: resultOf(req.Command, status, err),
			Status: string(status),
			Err:    observedError(respErr, err),
		})
	}()

	sp, err := c.getPoolForServer(addr)
	if err != nil {
		return err
	}
	err = sp.Execute(ctx, req, func(resp *meta.Response) error {
		status = resp.Status
		respErr = resp.Error
		return consume(resp)
	})
	return err
}

// ExecuteBatch executes multiple requests with automatic server routing.
// Requests are grouped by server and executed concurrently using pipelined requests.
// Returns responses in the same order as requests.
//
// Responses are matched to requests by position, which requires every request
// to produce a response: requests using the quiet flag are rejected. Use
// Connection.ExecuteBatch directly for quiet pipelining.
//
// If any server batch fails, an error is returned and the responses are
// discarded, including those from servers that succeeded.
func (c *Client) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	for _, req := range reqs {
		if req.HasFlag(meta.FlagQuiet) {
			return nil, fmt.Errorf("memcache: quiet flag is not supported in ExecuteBatch: responses are matched to requests by position")
		}
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
			batch = &serverBatch{serverAddr: addr}
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

			bctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: OpBatch, Server: b.serverAddr, Requests: len(b.reqs)})
			var observedErr error
			defer func() { op.End(OpResult{Err: observedErr}) }()

			// Get pool for this server
			sp, err := c.getPoolForServer(b.serverAddr)
			if err != nil {
				observedErr = err
				errChan <- err
				return
			}

			// Execute batch using ServerPool.ExecuteBatch
			responses, err := sp.ExecuteBatch(bctx, b.reqs)
			observedErr = observedBatchError(responses, err)
			if err != nil {
				errChan <- err
				return
			}

			// Without quiet flags, Connection.ExecuteBatch guarantees one
			// response per request; this is a defensive check so a bug can
			// never surface as nil responses to the caller.
			if len(responses) != len(b.indices) {
				observedErr = &OpError{
					Op:     OpBatch,
					Server: b.serverAddr,
					Err:    fmt.Errorf("received %d responses for %d requests", len(responses), len(b.indices)),
				}
				errChan <- observedErr
				return
			}

			for i, resp := range responses {
				results[b.indices[i]] = resp
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
// It is safe to call multiple times. Operations issued after Close fail.
// Close blocks until in-flight operations return their connections to
// the pools.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		// Stop health check goroutine if running
		if c.config.HealthCheckInterval > 0 {
			close(c.stopHealthCheck)
		}

		// Mark the client closed and snapshot the pools while holding the lock,
		// then release it before waiting for checked-out resources to return.
		c.mu.Lock()
		c.closed = true
		pools := make([]*ServerPool, 0, len(c.pools))
		for _, sp := range c.pools {
			pools = append(pools, sp)
		}
		c.mu.Unlock()

		// Close the pools concurrently: each Close waits for that pool's
		// checked-out connections, so closing sequentially would make the
		// total shutdown time the sum of the per-pool waits.
		var wg sync.WaitGroup
		for _, sp := range pools {
			wg.Go(sp.pool.Close)
		}
		wg.Wait()
	})
}

// selectServerForKey picks the server address for a given key.
// Uses the configured SelectServer function with the current server list.
func (c *Client) selectServerForKey(key string) (string, error) {
	servers := c.servers.List()
	if len(servers) == 0 {
		return "", ErrNoServers
	}
	if len(servers) == 1 {
		return servers[0].Address, nil
	}

	server := c.config.ServerSelector(key, servers)
	if server.Address == "" {
		return "", fmt.Errorf("memcache: server selector returned an empty address")
	}
	return server.Address, nil
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

// healthCheckPingTimeout bounds health check pings when no operation timeout
// is configured, so a dead connection cannot stall the health check loop.
const healthCheckPingTimeout = 5 * time.Second

// checkPoolConnections checks all idle connections in a pool and destroys those that are stale or unhealthy.
func (c *Client) checkPoolConnections(pool connPool) {
	now := time.Now()

	pingTimeout := c.config.Timeout
	if pingTimeout <= 0 {
		pingTimeout = healthCheckPingTimeout
	}

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
		err := func() error {
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			defer cancel()
			return res.Value().Ping(ctx)
		}()
		if err != nil {
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

	if c.closed {
		return nil, ErrClientClosed
	}

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

// PoolMetrics returns connection-pool metrics for all server pools.
func (c *Client) PoolMetrics() []PoolMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	metrics := make([]PoolMetrics, 0, len(c.pools))
	for _, sp := range c.pools {
		metrics = append(metrics, sp.Metrics())
	}
	return metrics
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
		return nil, ErrNoServers
	}

	// Collect stats from each server concurrently
	results := make([]ServerStats, len(servers))
	var wg sync.WaitGroup
	wg.Add(len(servers))

	for i, srv := range servers {
		go func(idx int, serverAddr string) {
			defer wg.Done()

			results[idx].Addr = serverAddr

			sctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: OpStats, Server: serverAddr})
			defer func() { op.End(OpResult{Err: results[idx].Error}) }()

			// Get pool for this server
			sp, err := c.getPoolForServer(serverAddr)
			if err != nil {
				results[idx].Error = err
				return
			}

			// Acquire connection
			res, err := sp.acquireHealthy(sctx)
			if err != nil {
				results[idx].Error = sp.wrapErr(OpStats, "", fmt.Errorf("acquire: %w", err))
				return
			}

			conn := res.Value()

			// Execute stats command
			stats, err := conn.ExecuteStats(sctx, args...)
			if err != nil {
				if meta.ShouldCloseConnection(err) {
					res.Destroy()
				} else {
					sp.release(res)
				}
				results[idx].Error = sp.wrapErr(OpStats, "", err)
				return
			}

			results[idx].Stats = stats
			sp.release(res)
		}(i, srv.Address)
	}

	wg.Wait()
	return results, nil
}
