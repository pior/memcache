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

// Counter is the result of an arithmetic operation.
type Counter struct {
	Key   string
	Value uint64
	Found bool
}

// Config holds configuration for the memcache client connection pool.
type Config struct {
	// MaxSize is the maximum number of connections in the pool.
	// A non-positive value selects the default of 10.
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
	// receives no operations at all only shrinks while the idle-connection
	// pings are enabled (a non-negative HealthCheckInterval).
	// Zero means no limit.
	MaxConnIdleTime time.Duration

	// HealthCheckInterval controls the background maintenance pass. Each pass
	// pings idle connections and closes broken ones and those past
	// MaxConnLifetime/MaxConnIdleTime, even when no operations are flowing, and
	// reaps the pool of any server absent from the set for reapAfterMissedPasses
	// consecutive passes, so a client against a dynamic server set (e.g.
	// Kubernetes endpoints) does not accumulate pools for departed addresses.
	//
	// Zero selects a sensible default (see defaultHealthCheckInterval). A
	// negative value disables the idle-connection pings — lifetime and idle
	// limits are then enforced only when connections are checked out or returned
	// — but the maintenance loop still runs and reaps departed-server pools, so a
	// dynamic set never leaks. With pings disabled the reap cadence is the
	// magnitude of the interval when that is a sane duration, otherwise the
	// default (so a token -1 does not busy-loop). (A static Servers set has
	// nothing to reap.)
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
	// The cap cannot be disabled: a non-positive value selects a conservative
	// default (see defaultOperationTimeout), so the client is never left
	// unbounded by a configuration mistake. Set a large explicit value for
	// operations that legitimately need a long budget.
	// Recommended: 100ms-1s depending on your latency requirements.
	Timeout time.Duration

	// ConnectTimeout is the timeout for establishing new connections.
	// This includes TCP handshake and TLS handshake if applicable.
	// A non-positive value inherits the resolved Timeout, so the dial is
	// always bounded.
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
	// IsExcluded is composed with the client's exclusions for caller context
	// errors and client-side request validation; it is not replaced.
	CircuitBreakerSettings *gobreaker.Settings

	// Observer is notified around each operation for tracing and metrics.
	// If nil, no observation is performed. See the otelmemcache subpackage for
	// a ready-made OpenTelemetry adapter.
	Observer Observer
}

// defaultOperationTimeout is the default for Config.Timeout and for
// NewConnection's timeout parameter, selected by any non-positive value.
// One second is far above healthy memcached latencies
// (sub-millisecond to low milliseconds), so it never constrains a working
// server; it exists so that no configuration is ever unbounded — the stress
// soak showed that a hung-but-connected server otherwise stalls every
// operation whose context carries no deadline. Latency-sensitive deployments
// should set a much lower Timeout explicitly.
const defaultOperationTimeout = time.Second

// defaultIdleConnCheckThreshold is the default for Config.IdleConnCheckThreshold.
// One second sits well above the sub-millisecond idle gaps of a pool under load
// (so the hot path pays nothing) and well below the idle timeouts that reset a
// connection (server restart, LB/middlebox idle timeout), so genuinely idle
// connections are the ones probed.
const defaultIdleConnCheckThreshold = time.Second

// defaultHealthCheckInterval is the default for Config.HealthCheckInterval.
// The loop is on by default so that a zero-value Config still reaps
// departed-server pools and enforces lifetime/idle limits on pools no traffic
// touches; 30 seconds keeps the background cost negligible while bounding how
// long a departed server's connections can linger.
const defaultHealthCheckInterval = 30 * time.Second

// Client is a memcache client that implements the Querier interface using a connection pool.
type Client struct {
	*Commands // Embedded command operations

	servers Servers

	pools *serverPools

	config Config

	// Background maintenance loop. It always runs: departed-server pools are
	// reaped every maintInterval even when the idle-connection pings are
	// disabled (pingsEnabled false), so a dynamic server set never leaks.
	maintInterval   time.Duration
	pingsEnabled    bool
	stopHealthCheck chan struct{}
	healthCheckDone chan struct{}
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
	if config.Timeout <= 0 {
		config.Timeout = defaultOperationTimeout
	}
	if config.IdleConnCheckThreshold == 0 {
		config.IdleConnCheckThreshold = defaultIdleConnCheckThreshold
	}
	if config.ConnectTimeout <= 0 {
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

	// The background maintenance loop always runs so departed-server pools are
	// reaped even when idle-connection pings are disabled. HealthCheckInterval
	// controls the pings: a positive value pings (and reaps) at that interval;
	// zero uses the default; a negative value disables the pings but the loop
	// still reaps — at the magnitude of the interval when that is a sane
	// duration, else at the default (so a token -1 does not spin).
	maintInterval := config.HealthCheckInterval
	pingsEnabled := true
	switch {
	case config.HealthCheckInterval == 0:
		maintInterval = defaultHealthCheckInterval
	case config.HealthCheckInterval < 0:
		pingsEnabled = false
		if d := -config.HealthCheckInterval; d >= time.Millisecond {
			maintInterval = d
		} else {
			maintInterval = defaultHealthCheckInterval
		}
	}

	client := &Client{
		servers:         servers,
		pools:           newServerPools(),
		config:          config,
		maintInterval:   maintInterval,
		pingsEnabled:    pingsEnabled,
		stopHealthCheck: make(chan struct{}),
		healthCheckDone: make(chan struct{}),
	}

	// Initialize embedded Commands with execute function
	client.Commands = NewCommands(client)

	go func() {
		defer close(client.healthCheckDone)
		client.healthCheckLoop()
	}()

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
// Close stops the health-check loop, waits for any in-flight pass to
// finish, then blocks until in-flight operations return their
// connections to the pools.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		// Stop the maintenance goroutine and wait for it to exit, so no
		// pass runs concurrently with — or after — the shutdown below.
		close(c.stopHealthCheck)
		<-c.healthCheckDone

		closePools(c.pools.closeAll())
	})
}

// closePools closes the pools concurrently: each Close waits for that pool's
// checked-out connections to be returned, so closing sequentially would make
// the total wait the sum of the per-pool waits.
func closePools(pools []*ServerPool) {
	var wg sync.WaitGroup
	for _, sp := range pools {
		wg.Go(sp.Close)
	}
	wg.Wait()
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

// healthCheckLoop runs the background maintenance pass on maintInterval: it
// always reaps departed-server pools and, when pings are enabled, checks idle
// connections for health and lifecycle limits.
func (c *Client) healthCheckLoop() {
	ticker := time.NewTicker(c.maintInterval)
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

// checkAllPools reaps pools for departed servers, then — when idle-connection
// pings are enabled — runs health checks on the pools that remain, concurrently.
// Reaping runs on every pass regardless of the ping setting, so disabling the
// pings (a negative HealthCheckInterval) never turns off departed-pool
// reclamation.
//
// Concurrency bounds the pass duration to roughly one ping timeout regardless
// of fleet size: checking sequentially, a fleet with many pools of hung
// connections (e.g. a departed region) would make a pass last the sum of every
// ping timeout — minutes during which departed-pool reaping is stalled (ticker
// ticks are dropped while a pass runs) and Close blocks, since it waits for
// the in-flight pass.
func (c *Client) checkAllPools() {
	c.reapDepartedPools()

	if !c.pingsEnabled {
		return
	}

	var wg sync.WaitGroup
	for _, sp := range c.pools.snapshot() {
		wg.Go(sp.checkIdleConnections)
	}
	wg.Wait()
}

// reapDepartedPools closes and forgets the pool of any server absent from the
// set for reapAfterMissedPasses consecutive passes. Pools are created lazily
// and, without reaping, never removed: a long-lived client against a dynamic
// server set (e.g. Kubernetes endpoints churned by rolling deploys) would
// otherwise accumulate a pool — and its idle connections — for every address
// it ever routed to.
//
// An empty server set is left alone rather than treated as "every server is
// gone": a service-discovery blip that momentarily reports no servers must not
// tear down every healthy pool. Operations already fail with ErrNoServers while
// the set is empty, and the warm pools are ready when servers reappear.
//
// A reaped pool can transiently be re-created: an operation routed from a
// snapshot taken just before the server departed re-creates it via
// getPoolForServer. Such a pool is reaped again on a later pass, so the leak
// is bounded to reapAfterMissedPasses intervals.
func (c *Client) reapDepartedPools() {
	live := c.servers.List()
	if len(live) == 0 {
		return
	}

	liveByAddr := make(map[string]struct{}, len(live))
	for _, srv := range live {
		liveByAddr[srv.Address] = struct{}{}
	}

	closePools(c.pools.reapDeparted(liveByAddr))
}

// getPoolForServer returns the pool for a specific server address.
// Creates the pool lazily if it doesn't exist.
func (c *Client) getPoolForServer(addr string) (*ServerPool, error) {
	return c.pools.getOrCreate(addr, func() (*ServerPool, error) {
		return NewServerPool(addr, c.config)
	})
}

// PoolMetrics returns connection-pool metrics for all server pools.
func (c *Client) PoolMetrics() []PoolMetrics {
	pools := c.pools.snapshot()
	metrics := make([]PoolMetrics, 0, len(pools))
	for _, sp := range pools {
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
				// Stats is a multi-line response, so an error can leave the stream
				// position unknown even when the error is otherwise recoverable.
				res.Destroy()
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
