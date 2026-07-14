package memcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

func NewServerPool(addr string, config Config) (*ServerPool, error) {
	constructor := func(ctx context.Context) (*Connection, error) {
		// Apply ConnectTimeout for connection establishment
		dialCtx := ctx
		if config.ConnectTimeout > 0 {
			var cancel context.CancelFunc
			dialCtx, cancel = context.WithTimeout(ctx, config.ConnectTimeout)
			defer cancel()
		}

		netConn, err := config.Dialer.DialContext(dialCtx, "tcp", addr)
		if err != nil {
			return nil, err
		}

		return NewConnection(netConn, config.Timeout), nil
	}

	pool, err := newPuddlePool(constructor, config.MaxSize)
	if err != nil {
		return nil, err
	}

	// Bound health check pings even when no operation timeout is configured,
	// so a dead connection cannot stall the maintenance loop.
	pingTimeout := config.Timeout
	if pingTimeout <= 0 {
		pingTimeout = healthCheckPingTimeout
	}

	// The probe's budgets mirror what live operations get: ConnectTimeout for
	// the dial (falling back to the operation timeout) and the connection's
	// own operation timeout for the ping. Every deadline involved is
	// operator-owned, so a probe timeout is the server's failure regardless
	// of the deadlines callers use — which is what makes probe outcomes
	// usable as hung-server evidence (see healthCheck).
	dialTimeout := config.ConnectTimeout
	if dialTimeout <= 0 {
		dialTimeout = config.Timeout
	}
	if dialTimeout <= 0 {
		dialTimeout = defaultOperationTimeout
	}
	probe := func() error {
		dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		conn, err := constructor(dialCtx)
		if err != nil {
			return err
		}
		defer conn.Close()
		// No context deadline: the connection's operation timeout is the
		// binding deadline, so the ping cannot outlast the operator's budget.
		return conn.Ping(context.Background())
	}

	return &ServerPool{
		addr:            addr,
		pool:            pool,
		breaker:         newBreaker(addr, config.Breaker),
		probe:           probe,
		maxConnLifetime: config.MaxConnLifetime,
		maxConnIdleTime: config.MaxConnIdleTime,
		maxSize:         config.MaxSize,
		idleConnCheck:   config.IdleConnCheckThreshold,
		pingTimeout:     pingTimeout,
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr    string
	pool    connPool
	breaker *gobreaker.CircuitBreaker[bool]

	// probe dials a fresh connection and pings the server, under operator-
	// owned deadlines only. healthCheck uses it to detect a hung server and
	// to notice its recovery.
	probe func() error

	// hung marks a server the maintenance loop confirmed unresponsive:
	// reachable and accepting connections, but not answering within the
	// operation timeout. While set, operations fail immediately with
	// ErrBreakerOpen instead of paying their full budget. Only an answered
	// maintenance probe clears it. See healthCheck.
	hung atomic.Bool

	maxConnLifetime time.Duration
	maxConnIdleTime time.Duration
	maxSize         int32
	idleConnCheck   time.Duration
	pingTimeout     time.Duration
}

// pastLimits reports whether a connection has exceeded MaxConnLifetime or
// MaxConnIdleTime. It is the single expiry policy, applied both at checkout
// (acquireHealthy) and on the idle scan (checkIdleConnections).
func (sp *ServerPool) pastLimits(res poolResource, now time.Time) bool {
	if sp.maxConnLifetime > 0 && now.Sub(res.CreationTime()) > sp.maxConnLifetime {
		return true
	}
	return sp.maxConnIdleTime > 0 && res.IdleDuration() > sp.maxConnIdleTime
}

// healthCheckPingTimeout bounds health check pings when no operation timeout
// is configured, so a dead connection cannot stall the maintenance loop.
const healthCheckPingTimeout = 5 * time.Second

// hungConfirmProbes is how many additional probes must time out, after the
// first one, before a server is marked hung. Three unanimous timeouts
// distinguish a wedged server from one slow request while keeping the
// confirmation cheap: the probes run sequentially, so a pass on a hung server
// lasts about three dial-plus-ping budgets.
const hungConfirmProbes = 2

// healthCheck is the per-pool maintenance pass: it scrubs the idle
// connections, then probes for the one failure mode live traffic cannot
// report — a hung server.
//
// The breaker counts a timeout against the server only when the operator's
// Config.Timeout was the binding deadline (see isBreakerExcluded). When every
// caller passes a tighter deadline, a hung server's timeouts are all
// attributed to the callers: live traffic produces no breaker evidence,
// nothing sheds the server, and every operation routed to it pays its full
// budget. The maintenance probe owns its deadlines, so it sees the hang
// regardless of caller behavior.
//
// The verdict is a plain marker (ServerPool.hung) rather than breaker
// evidence: the breaker's trip policy is sized for live-traffic volume
// (TripMinRequests inside TripWindow), which sporadic probes cannot supply
// honestly. Each pass costs one probe; a probe timeout is confirmed with
// hungConfirmProbes more before the marker is set, and any answered probe
// clears it. A fast failure (refused dial, reset) is not a hang and is left
// to the breaker: live traffic observes those as counted errors under any
// caller deadline.
func (sp *ServerPool) healthCheck() {
	sp.checkIdleConnections()
	if sp.breaker == nil {
		return
	}

	err := sp.probe()
	if err == nil {
		sp.hung.Store(false)
		return
	}
	if !isIOTimeout(err) || sp.hung.Load() {
		// A marked server stays marked on a timeout without re-confirming,
		// so a persistent hang costs each pass a single probe.
		return
	}

	for range hungConfirmProbes {
		err := sp.probe()
		if err == nil || !isIOTimeout(err) {
			return
		}
	}
	sp.hung.Store(true)
}

// isIOTimeout reports whether err is an I/O timeout — the hung-server
// signature, as opposed to a fast failure like a refused dial or a reset. It
// matches both a socket deadline (os.ErrDeadlineExceeded) and a dial cut off
// by the probe's context deadline (a net.Error timeout).
func isIOTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// checkIdleConnections checks all idle connections and destroys those that
// are past their limits or fail a ping.
//
// Pings run concurrently: each one can block for up to pingTimeout on a dead
// or hung server, so probing sequentially would make a pass last
// numIdle × pingTimeout. The idle connections are held (acquired) for the
// duration of the scan, so a shorter pass also means less time during which
// operations find the pool empty and have to dial or wait.
func (sp *ServerPool) checkIdleConnections() {
	now := time.Now()

	var wg sync.WaitGroup
	for _, res := range sp.pool.AcquireAllIdle() {
		if sp.pastLimits(res, now) {
			res.Destroy()
			continue
		}

		// Perform health check by sending a noop command
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), sp.pingTimeout)
			defer cancel()

			if err := res.Value().Ping(ctx); err != nil {
				res.Destroy()
				return
			}
			res.ReleaseUnused()
		})
	}
	wg.Wait()
}

// Close closes the pool, destroying its idle connections. It blocks until
// checked-out connections are returned.
func (sp *ServerPool) Close() {
	sp.pool.Close()
}

// acquireHealthy acquires a connection, discarding pooled connections that
// should no longer be used and acquiring another one instead:
//
//   - connections past MaxConnLifetime or idle past MaxConnIdleTime. Enforcing
//     the limits here makes them effective even when the maintenance loop is
//     not running (the loop remains the only thing that shrinks a pool no
//     traffic touches).
//   - connections that died while idle: a connection that sat idle at least
//     idleConnCheck is verified with a cheap non-blocking probe
//     (Connection.checkAlive); if the peer has closed or reset it, it is
//     replaced, so a rolling restart or an idle-timed-out flow doesn't surface
//     as a failed operation.
//
// Freshly established and actively cycling connections (idle below the
// threshold) are trusted without a probe, keeping the hot path syscall-free.
func (sp *ServerPool) acquireHealthy(ctx context.Context) (poolResource, error) {
	if sp.idleConnCheck <= 0 && sp.maxConnLifetime <= 0 && sp.maxConnIdleTime <= 0 {
		return sp.pool.Acquire(ctx)
	}

	// There are at most maxSize expired or dead idle connections and each
	// failed check destroys one, so the pool converges on a usable connection
	// within this many attempts. The cap only guards a pathological constructor
	// handing back expired or dead connections: on exhaustion we return the
	// last connection and let Execute surface any real error, exactly as it
	// would without this check.
	maxAttempts := int(sp.maxSize) + 1
	for attempt := 1; ; attempt++ {
		resource, err := sp.pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		if attempt >= maxAttempts {
			return resource, nil
		}

		if sp.pastLimits(resource, time.Now()) {
			resource.Destroy()
			continue
		}

		if sp.idleConnCheck > 0 && resource.IdleDuration() >= sp.idleConnCheck && resource.Value().checkAlive() != nil {
			resource.Destroy()
			continue
		}

		return resource, nil
	}
}

// release returns a connection to the pool, or destroys it if it has
// exceeded MaxConnLifetime. Enforcing the lifetime here (and not only in the
// maintenance loop) matters under sustained load: a saturated pool never has
// idle connections, so the health check alone would never recycle them.
func (sp *ServerPool) release(resource poolResource) {
	if sp.maxConnLifetime > 0 && time.Since(resource.CreationTime()) > sp.maxConnLifetime {
		resource.Destroy()
		return
	}
	resource.Release()
}

func (sp *ServerPool) Address() string {
	return sp.addr
}

// PoolMetrics contains metrics for a single server's connection pool.
type PoolMetrics struct {
	Addr    string
	Conns   ConnPoolMetrics
	Breaker BreakerStats
}

func (sp *ServerPool) Metrics() PoolMetrics {
	metrics := PoolMetrics{
		Addr:  sp.addr,
		Conns: sp.pool.Metrics(),
	}
	if sp.breaker != nil {
		counts := sp.breaker.Counts()
		metrics.Breaker = BreakerStats{
			State:                sp.breaker.State().String(),
			Hung:                 sp.hung.Load(),
			Requests:             counts.Requests,
			TotalSuccesses:       counts.TotalSuccesses,
			TotalFailures:        counts.TotalFailures,
			ConsecutiveSuccesses: counts.ConsecutiveSuccesses,
			ConsecutiveFailures:  counts.ConsecutiveFailures,
		}
	}
	return metrics
}

// Execute executes a single request-response cycle with proper connection management.
// It handles acquiring a connection, sending the request, invoking consume with the
// response while the connection is still checked out, and releasing/destroying the
// connection based on error conditions.
// The request is wrapped with the server's circuit breaker.
//
// Execution failures are returned as *OpError carrying the operation, key, and
// server address. An error returned by consume is returned unchanged: it is a
// command-level outcome, so it is not wrapped and does not count as a circuit
// breaker failure.
//
// A server marked hung by the maintenance loop rejects the operation with
// ErrBreakerOpen before the breaker or the pool is touched.
func (sp *ServerPool) Execute(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) error {
	if sp.hung.Load() {
		return sp.wrapErr(string(req.Command), req.Key, ErrBreakerOpen)
	}

	if sp.breaker == nil {
		execErr, consumeErr := sp.execRequestDirect(ctx, req, consume)
		if execErr != nil {
			return execErr
		}
		return consumeErr
	}

	var execErr, consumeErr error

	_, err := sp.breaker.Execute(func() (bool, error) {
		execErr, consumeErr = sp.execRequestDirect(ctx, req, consume)
		return execErr == nil, execErr
	})

	if err != nil {
		// Errors from execRequestDirect are already wrapped; breaker
		// rejections surface as ErrBreakerOpen and get wrapped here.
		return sp.wrapErr(string(req.Command), req.Key, mapBreakerRejection(err))
	}
	if execErr != nil {
		return execErr
	}
	return consumeErr
}

// wrapErr wraps an error with operation and server context, unless it
// already carries it.
func (sp *ServerPool) wrapErr(op, key string, err error) error {
	var opErr *OpError
	if errors.As(err, &opErr) {
		return err
	}
	return &OpError{Op: op, Key: key, Server: sp.addr, Err: err}
}

// execRequestDirect performs the actual request execution without circuit breaker.
// The connection is released (or destroyed) only after consume has run, so the
// response buffers cannot be reused by another operation while consume reads them.
//
// The two error returns keep transport health separate from command outcome:
// execErr reports execution failures (wrapped in *OpError) and feeds the circuit
// breaker; consumeErr is whatever consume returned, passed through untouched.
func (sp *ServerPool) execRequestDirect(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) (execErr, consumeErr error) {
	op := string(req.Command)

	resource, err := sp.acquireHealthy(ctx)
	if err != nil {
		// The prefix distinguishes a failure to get a connection (pool
		// saturation, dial) from an I/O failure on the wire.
		return sp.wrapErr(op, req.Key, fmt.Errorf("acquire: %w", err)), nil
	}

	conn := resource.Value()

	// Capture resp.Error and keep consume's error out of the connection error
	// path: a command-level error says nothing about the connection's health.
	var respErr error
	err = conn.Execute(ctx, req, func(resp *meta.Response) error {
		respErr = resp.Error
		consumeErr = consume(resp)
		return nil
	})
	if err != nil {
		if meta.ShouldCloseConnection(err) {
			resource.Destroy()
		} else {
			sp.release(resource)
		}
		return sp.wrapErr(op, req.Key, err), nil
	}

	// Protocol errors are reported in resp.Error rather than as Go errors;
	// some of them (e.g. CLIENT_ERROR) corrupt the protocol state and require
	// closing the connection instead of returning it to the pool.
	if respErr != nil && meta.ShouldCloseConnection(respErr) {
		resource.Destroy()
	} else {
		sp.release(resource)
	}
	return nil, consumeErr
}

// ExecuteBatch executes multiple requests in a pipeline using the NoOp marker strategy.
// Sends all requests followed by a NoOp command, then reads responses until the NoOp response.
// This leverages memcached's FIFO guarantee for optimal performance.
//
// Returns responses in the same order as requests.
// Individual request errors are captured in Response.Error (protocol errors).
// I/O errors or connection failures are returned as Go errors.
//
// The batch execution is wrapped with the circuit breaker to track success/failure.
func (sp *ServerPool) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	if sp.hung.Load() {
		return nil, sp.wrapErr(OpBatch, "", ErrBreakerOpen)
	}

	if sp.breaker == nil {
		return sp.execBatchDirect(ctx, reqs)
	}

	var responses []*meta.Response
	var execErr error

	_, err := sp.breaker.Execute(func() (bool, error) {
		responses, execErr = sp.execBatchDirect(ctx, reqs)
		return execErr == nil, execErr
	})

	if err != nil {
		return nil, sp.wrapErr(OpBatch, "", mapBreakerRejection(err))
	}
	return responses, execErr
}

// execBatchDirect performs the actual batch execution without circuit breaker.
func (sp *ServerPool) execBatchDirect(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	resource, err := sp.acquireHealthy(ctx)
	if err != nil {
		return nil, sp.wrapErr(OpBatch, "", fmt.Errorf("acquire: %w", err))
	}

	conn := resource.Value()

	responses, err := conn.ExecuteBatch(ctx, reqs)
	if err != nil {
		if meta.ShouldCloseConnection(err) {
			resource.Destroy()
		} else {
			sp.release(resource)
		}
		return nil, sp.wrapErr(OpBatch, "", err)
	}

	// A response carrying a connection-corrupting protocol error (e.g.
	// CLIENT_ERROR) means the connection cannot be safely reused.
	destroy := false
	for _, resp := range responses {
		if resp.Error != nil && meta.ShouldCloseConnection(resp.Error) {
			destroy = true
			break
		}
	}
	if destroy {
		resource.Destroy()
	} else {
		sp.release(resource)
	}
	return responses, nil
}
