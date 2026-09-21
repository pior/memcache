package memcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pior/memcache/meta"
)

// Dialer establishes the network connections used by the client's pools.
// *net.Dialer satisfies this interface.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Config holds the configuration of a memcache client. Every field is
// optional: the zero value selects the field's documented default.
//
// The client keeps one connection pool and one circuit breaker per server, so
// every limit below applies per server, not client-wide: against ten servers,
// a MaxConnsPerServer of 10 allows a hundred connections in total.
//
// "Default" is not one thing, so read each field's own documentation:
//
//   - MaxConnsPerServer, OperationTimeout, MaintenanceInterval,
//     IdleConnCheckAfter and the Breaker policy take their Default* constant;
//   - DialTimeout inherits the resolved OperationTimeout;
//   - MaxConnLifetime and MaxConnIdleTime mean no limit.
//
// Turning something off is equally field-specific: IdleConnCheckAfter is
// disabled by a negative value and Breaker by Breaker.Enabled, while
// OperationTimeout cannot be disabled at all — the client is never left
// unbounded by a configuration mistake.
type Config struct {
	// MaxConnsPerServer is the maximum number of connections the client opens
	// to a single server. It is therefore also the number of operations that
	// can be in flight to that server at once: past it, callers wait for a
	// connection to come back.
	//
	// That wait is bounded only by the caller's context (see the Timeouts
	// section in the package documentation), so a pool sized below peak
	// concurrency plus deadline-less contexts queues waiters without shedding.
	// Size it against peak per-server concurrency, and pass contexts with
	// deadlines.
	// The default is DefaultMaxConnsPerServer (10).
	MaxConnsPerServer int

	// MaxConnLifetime is the maximum duration a connection can be reused.
	// Enforced when a connection is checked out of the pool, when it is
	// returned after an operation, and by the maintenance loop for idle
	// connections.
	//
	// Useful values are seconds to minutes. Below a second, connections are
	// recycled faster than they are useful and most operations pay a fresh
	// dial — which collapses throughput when the dial includes a TLS
	// handshake.
	// The default is no limit.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the maximum duration a connection can be idle before
	// being closed. Enforced when a connection is checked out of the pool and
	// by the maintenance loop.
	//
	// Note that checkout-time enforcement needs traffic to act: a pool that
	// receives no operations at all only shrinks when the background
	// maintenance loop prunes it. That loop always runs; see
	// MaintenanceInterval for how its period affects how promptly this happens.
	//
	// Set it below MaxConnLifetime, or it never fires: the lifetime limit
	// always preempts it.
	// The default is no limit.
	MaxConnIdleTime time.Duration

	// MaintenanceInterval is how often the background maintenance loop runs.
	// The loop always runs; this only tunes its period.
	// The default is DefaultMaintenanceInterval (30s).
	//
	// Each pass, for every pool:
	//
	//   - pings idle connections and closes any that are broken or past
	//     MaxConnLifetime/MaxConnIdleTime, so limits are enforced even on a pool
	//     no operations are flowing through (checkout/return also enforce them,
	//     but that needs traffic to act);
	//   - reaps the pool of any server absent from the server set for
	//     reapAfterMissedPasses consecutive passes, so a client against a
	//     dynamic server set (e.g. Kubernetes endpoints) does not accumulate a
	//     pool — with its idle connections and its circuit breaker — for every
	//     address it ever routed to.
	//
	// The interval is the resolution of this maintenance, not a deadline, so a
	// longer period trades promptness for a bit less background work: an idle
	// connection a server or middlebox dropped is not noticed until the next
	// pass (or the next operation routed to it); a connection past its
	// lifetime/idle limit on an otherwise idle pool is pruned that much later;
	// and a departed server's pool — its connections and breaker — survives up
	// to reapAfterMissedPasses of these intervals before being reclaimed. Pick
	// the period against how fast your server set churns and how long you can
	// tolerate a dropped idle connection lingering; the default is a good
	// starting point. A period long enough to matter against a churning set
	// (minutes) lets departed pools pile up in the meantime, so prefer the
	// default there rather than disabling maintenance by stretching the period.
	MaintenanceInterval time.Duration

	// IdleConnCheckAfter controls the on-acquire liveness check: when a
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
	// The default is DefaultIdleConnCheckAfter (1s); a negative value disables
	// the check.
	IdleConnCheckAfter time.Duration

	// OperationTimeout bounds the read/write of a single memcache operation.
	// It acts as an upper bound on every operation: the effective deadline is
	// the earlier of the context deadline and now+OperationTimeout. A context
	// deadline sooner than OperationTimeout still wins; a later one (or a
	// context with no deadline) is capped at OperationTimeout. This ensures a
	// long-lived context (e.g. a request- or job-scoped one) cannot leave an
	// operation unbounded, so a hung-but-connected server fails fast instead of
	// stalling the client.
	//
	// OperationTimeout bounds socket I/O only. It does not bound waiting for a
	// connection from a saturated pool: that wait is bounded solely by the
	// caller's context, so pass contexts with deadlines (see the Timeouts
	// section in the package documentation).
	//
	// The cap cannot be disabled, so the client is never left unbounded by a
	// configuration mistake. Set a large explicit value for operations that
	// legitimately need a long budget; 100ms-1s suits most latency
	// requirements.
	//
	// With Breaker enabled, OperationTimeout must be the binding deadline for
	// a hung server's timeouts to be attributed to that server and open its
	// breaker; callers whose own deadline is at or below it own the timeout
	// instead. See BreakerConfig.
	// The default is DefaultOperationTimeout (1s).
	OperationTimeout time.Duration

	// DialTimeout bounds establishing a new connection, including the TCP
	// handshake and the TLS handshake if applicable. Set it higher than
	// OperationTimeout when TLS connections take longer to establish.
	// The default is the resolved OperationTimeout, so the dial is always
	// bounded.
	DialTimeout time.Duration

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

	// Breaker configures the circuit breaker guarding each server: when a
	// server's recent failure ratio trips it, operations to that server fail
	// fast with ErrBreakerOpen instead of tying up connections. Disabled
	// unless Breaker.Enabled is true. See BreakerConfig for the policy knobs
	// and their defaults.
	Breaker BreakerConfig

	// Observer is notified around each operation for tracing and metrics.
	// If nil, no observation is performed. See the otelmemcache subpackage for
	// a ready-made OpenTelemetry adapter.
	Observer Observer
}

// Defaults for Config. A field left at zero (or negative, where the field's
// documentation does not give a negative value its own meaning) gets its
// default.
const (
	// DefaultMaxConnsPerServer comfortably serves a typical application's
	// concurrency against a sub-millisecond backend while keeping the
	// per-server socket footprint small; deployments with high per-server
	// concurrency should size it explicitly.
	DefaultMaxConnsPerServer = 10

	// DefaultOperationTimeout is far above healthy memcached latencies
	// (sub-millisecond to low milliseconds), so it never constrains a working
	// server; it exists so that no configuration is ever unbounded — the
	// stress soak showed that a hung-but-connected server otherwise stalls
	// every operation whose context carries no deadline. Latency-sensitive
	// deployments should set a much lower OperationTimeout explicitly.
	DefaultOperationTimeout = time.Second

	// DefaultIdleConnCheckAfter sits well above the sub-millisecond idle gaps
	// of a pool under load (so the hot path pays nothing) and well below the
	// idle timeouts that reset a connection (server restart, LB/middlebox idle
	// timeout), so genuinely idle connections are the ones probed.
	DefaultIdleConnCheckAfter = time.Second

	// DefaultMaintenanceInterval keeps the background cost negligible while
	// bounding how long a departed server's connections can linger. The loop
	// always runs, so a zero-value Config already reaps departed-server pools
	// and enforces lifetime/idle limits on pools no traffic touches.
	DefaultMaintenanceInterval = 30 * time.Second
)

// setDefaults replaces every zero-value field with its default. It is
// idempotent, so a config that already went through it (as the one Client
// hands to NewServerPool) is left unchanged.
func (c *Config) setDefaults() {
	if c.MaxConnsPerServer <= 0 {
		c.MaxConnsPerServer = DefaultMaxConnsPerServer
	}
	if c.OperationTimeout <= 0 {
		c.OperationTimeout = DefaultOperationTimeout
	}
	if c.IdleConnCheckAfter == 0 {
		c.IdleConnCheckAfter = DefaultIdleConnCheckAfter
	}
	if c.MaintenanceInterval <= 0 {
		c.MaintenanceInterval = DefaultMaintenanceInterval
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = c.OperationTimeout
	}
	if c.ServerSelector == nil {
		c.ServerSelector = StableServerSelector
	}
	if c.Dialer == nil {
		c.Dialer = &net.Dialer{}
	}
	if c.Observer == nil {
		c.Observer = noopObserver{}
	}
}

// Client is a memcache client that implements the Querier interface using a
// connection pool.
//
// It embeds [Commands], so the operations of [Querier] — Get, Set, Delete,
// Increment, MultiGet, … — are called directly on the client. The embedded
// value is not exported: a client's command surface is fixed at construction.
type Client struct {
	*commands // Single-key and pipelined multi-key operations, from [Commands]

	servers Servers

	pools *serverPools

	config Config

	// Background maintenance loop. It always runs (see MaintenanceInterval):
	// stopMaintenance signals it to stop, maintenanceDone is closed when it has.
	stopMaintenance chan struct{}
	maintenanceDone chan struct{}
	closeOnce       sync.Once
}

var _ Querier = (*Client)(nil)
var _ Executor = (*Client)(nil)

// NewClient creates a new memcache client with the given servers and configuration.
// For a single server, use: NewClient(StaticServers("host:port"), config)
func NewClient(servers Servers, config Config) *Client {
	if servers == nil {
		servers = StaticServers()
	}
	config.setDefaults()

	client := &Client{
		servers:         servers,
		pools:           newServerPools(),
		config:          config,
		stopMaintenance: make(chan struct{}),
		maintenanceDone: make(chan struct{}),
	}

	client.commands = NewCommands(client)

	// The background maintenance loop always runs: it reaps departed-server
	// pools and enforces lifetime/idle limits on pools no traffic touches.
	go func() {
		defer close(client.maintenanceDone)
		client.maintenanceLoop()
	}()

	return client
}

// Execute implements the Executor interface with automatic server routing.
// See [ResponseFunc] for the contract: the response is only valid during the
// fn call.
func (c *Client) Execute(ctx context.Context, req *meta.Request, fn ResponseFunc) (err error) {
	addr, err := c.selectServerForKey(req.Key)
	if err != nil {
		return err
	}

	// The observer runs after the response is released, so capture the fields
	// it needs (both safe to retain, unlike the response's buffers).
	var code meta.StatusType
	var respErr error

	name := opName(req)
	ctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: name, Address: addr, Key: req.Key})
	defer func() {
		op.End(OpResult{
			Status: statusOf(name, code, err),
			Code:   string(code),
			Err:    observedError(respErr, err),
		})
	}()

	sp, err := c.getPoolForServer(addr)
	if err != nil {
		return err
	}
	return sp.Execute(ctx, req, func(resp *meta.Response) {
		code = resp.Status
		respErr = resp.Error
		fn(resp)
	})
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
			return nil, errors.New("memcache: quiet flag is not supported in ExecuteBatch: responses are matched to requests by position")
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

	for _, b := range serverBatches {
		wg.Go(func() {
			bctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: OpBatch, Address: b.serverAddr, Requests: len(b.reqs)})
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
					Op:      OpBatch,
					Address: b.serverAddr,
					Err:     fmt.Errorf("received %d responses for %d requests", len(responses), len(b.indices)),
				}
				errChan <- observedErr
				return
			}

			for i, resp := range responses {
				results[b.indices[i]] = resp
			}
		})
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
// Close stops the maintenance loop, waits for any in-flight pass to
// finish, then blocks until in-flight operations return their
// connections to the pools.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		// Stop the maintenance goroutine and wait for it to exit, so no
		// pass runs concurrently with — or after — the shutdown below.
		close(c.stopMaintenance)
		<-c.maintenanceDone

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
// Uses the configured ServerSelector with the current server list.
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
		return "", errors.New("memcache: server selector returned an empty address")
	}
	return server.Address, nil
}

// maintenanceLoop is the always-on background loop: every
// MaintenanceInterval it reaps departed-server pools and checks idle
// connections for health and lifecycle limits. It runs until Close.
func (c *Client) maintenanceLoop() {
	ticker := time.NewTicker(c.config.MaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopMaintenance:
			return
		case <-ticker.C:
			c.runMaintenancePass()
		}
	}
}

// runMaintenancePass reaps pools for departed servers, then runs health checks on
// the pools that remain, concurrently.
//
// Concurrency bounds the pass duration to roughly one ping timeout regardless
// of fleet size: checking sequentially, a fleet with many pools of hung
// connections (e.g. a departed region) would make a pass last the sum of every
// ping timeout — minutes during which departed-pool reaping is stalled (ticker
// ticks are dropped while a pass runs) and Close blocks, since it waits for
// the in-flight pass.
func (c *Client) runMaintenancePass() {
	c.reapDepartedPools()

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
	Address string            // Server address
	Stats   map[string]string // Server statistics (name -> value)
	Error   error             // Error if stats request failed
}

// FlushAll invalidates all items on every currently configured server.
// Servers are flushed concurrently; a failure on any server is preserved and
// all failures are combined with errors.Join.
func (c *Client) FlushAll(ctx context.Context) error {
	servers := c.servers.List()
	if len(servers) == 0 {
		return ErrNoServers
	}

	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Go(func() {
			sctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: OpFlushAll, Address: server.Address})
			defer func() { op.End(OpResult{Err: errs[i]}) }()

			errs[i] = c.flushServer(sctx, server.Address)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// flushServer runs flush_all on one server. An error reply or an unexpected
// status is returned as an *OpError, so a joined FlushAll error names the
// failing servers.
func (c *Client) flushServer(ctx context.Context, addr string) error {
	sp, err := c.getPoolForServer(addr)
	if err != nil {
		return err
	}

	var outcome error
	err = sp.Execute(ctx, meta.NewRequest(meta.CmdFlushAll, "", nil), func(resp *meta.Response) {
		switch {
		case resp.HasError():
			outcome = resp.Error
		case resp.Status != meta.StatusOK:
			outcome = fmt.Errorf("unexpected flush_all status: %s", resp.Status)
		}
	})
	if err != nil {
		return err
	}
	if outcome != nil {
		return sp.wrapErr(OpFlushAll, "", outcome)
	}
	return nil
}

// Stats retrieves statistics from all memcache servers.
// Sends a stats request to each server and collects the responses.
// Returns a slice of ServerStats, one per server.
// Individual server errors are returned in ServerStats.Error, not as a Go error.
//
// args are the sub-command tokens sent after "stats", space-separated and
// verbatim: none for the general statistics, one for a named sub-command
// ("items", "slabs", "settings"), several for the ones that take parameters
// ("cachedump", "1", "100").
func (c *Client) Stats(ctx context.Context, args ...string) ([]ServerStats, error) {
	servers := c.servers.List()
	if len(servers) == 0 {
		return nil, ErrNoServers
	}

	// Collect stats from each server concurrently
	results := make([]ServerStats, len(servers))
	var wg sync.WaitGroup

	for i, srv := range servers {
		wg.Go(func() {
			result := &results[i]
			result.Address = srv.Address

			sctx, op := c.config.Observer.StartOp(ctx, OpInfo{Op: OpStats, Address: srv.Address})
			defer func() { op.End(OpResult{Err: result.Error}) }()

			sp, err := c.getPoolForServer(srv.Address)
			if err != nil {
				result.Error = err
				return
			}

			result.Stats, result.Error = sp.ExecuteStats(sctx, args...)
		})
	}

	wg.Wait()
	return results, nil
}
