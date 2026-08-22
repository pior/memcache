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

	// The probe budget mirrors what live operations get: ConnectTimeout for
	// the dial when configured (falling back to the operation timeout), and
	// the connection's own operation timeout for the ping. The probe owns
	// every deadline involved, so its timeouts are always attributed to the
	// server — which is what makes probe outcomes usable as breaker evidence.
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
			if errors.Is(err, context.DeadlineExceeded) {
				// Every dial deadline here (the probe budget, ConnectTimeout)
				// is operator-owned, so recast the timeout in a form the
				// breaker counts: isBreakerExcluded excludes context
				// deadlines as caller-owned.
				return fmt.Errorf("probe dial timeout: %w", os.ErrDeadlineExceeded)
			}
			return err
		}
		defer conn.Close()
		// No context deadline: the connection's operation timeout is the
		// binding deadline, so a ping timeout is the server's failure.
		return conn.Ping(context.Background())
	}

	return &ServerPool{
		addr:            addr,
		pool:            pool,
		breaker:         newBreaker(addr, config.Breaker),
		probe:           probe,
		hungProbes:      min(config.Breaker.withDefaults().TripMinRequests, maxHungProbes),
		maxConnLifetime: config.MaxConnLifetime,
		maxConnIdleTime: config.MaxConnIdleTime,
		maxSize:         config.MaxSize,
		idleConnCheck:   config.IdleConnCheckThreshold,
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr    string
	pool    connPool
	breaker *gobreaker.CircuitBreaker[bool]

	// probe dials a fresh connection and pings the server, with the
	// operator's budgets as the only deadlines; its timeouts are therefore
	// always server-attributed. Used by healthCheck to detect and confirm a
	// hung server.
	probe func() error
	// hungProbes is the size of the confirmHungServer burst: the breaker's
	// effective TripMinRequests, capped at maxHungProbes.
	hungProbes uint32

	maxConnLifetime time.Duration
	maxConnIdleTime time.Duration
	maxSize         int32
	idleConnCheck   time.Duration
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

// healthCheck is the per-pool maintenance pass: it scrubs the idle
// connections, then hunts for the one failure mode live traffic cannot
// report — a hung server.
//
// The breaker counts a timeout against the server only when the operator's
// Config.Timeout was the binding deadline (see isBreakerExcluded). When every
// caller passes a tighter deadline, a hung server's timeouts are all
// attributed to the callers: live traffic produces no breaker evidence,
// nothing sheds the server, and every operation routed to it pays its full
// budget. The maintenance pass owns its deadlines, so its probes see the hang
// regardless of caller behavior.
//
// Evidence is gathered in escalating steps, so a healthy server costs at most
// one spare probe per pass:
//
//  1. An idle ping was answered: the server responds, done.
//  2. No idle ping ran — the usual state of a hung server's pool, whose
//     connections all die with their operations — or the idle connections
//     failed without timing out (e.g. reset by a server restart): probe once
//     on a fresh connection. The probe goes through the breaker, so in the
//     half-open state it doubles as the recovery check — closing the breaker
//     once the server answers again, without waiting for live traffic — and
//     while the breaker is open it is rejected without dialing.
//  3. An idle ping or the probe timed out: the server looks hung — confirm
//     with enough probes for the trip policy to act (confirmHungServer).
func (sp *ServerPool) healthCheck() {
	sawSuccess, sawTimeout := sp.checkIdleConnections()
	if sp.breaker == nil || sawSuccess {
		return
	}
	if !sawTimeout && !isIOTimeout(sp.reportedProbe()) {
		return
	}
	sp.confirmHungServer()
}

// reportedProbe runs one probe through the breaker, so the outcome is
// recorded and the breaker's state gates it: while open the probe is
// rejected (returning ErrOpenState, which isIOTimeout does not mistake for a
// hang) and in half-open it is one of the recovery checks.
func (sp *ServerPool) reportedProbe() error {
	_, err := sp.breaker.Execute(func() (bool, error) {
		err := sp.probe()
		return err == nil, err
	})
	return err
}

// checkIdleConnections checks all idle connections and destroys those that
// are past their limits or fail a ping. Each ping is bounded by the
// connection's operation timeout (Config.Timeout).
//
// Pings run concurrently: each one can block for a full operation timeout on
// a dead or hung server, so probing sequentially would make a pass last
// numIdle × timeout. The idle connections are held (acquired) for the
// duration of the scan, so a shorter pass also means less time during which
// operations find the pool empty and have to dial or wait.
//
// The returned flags summarize the scan as hung-server evidence for
// healthCheck: sawSuccess reports that at least one ping was answered (the
// server responds, it cannot be hung), sawTimeout that at least one ping
// timed out — the hung-server signature. A connection killed while it sat
// idle fails fast with a reset or EOF instead, which says nothing about the
// server and raises neither flag.
func (sp *ServerPool) checkIdleConnections() (sawSuccess, sawTimeout bool) {
	now := time.Now()

	var success, timeout atomic.Bool
	var wg sync.WaitGroup
	for _, res := range sp.pool.AcquireAllIdle() {
		if sp.pastLimits(res, now) {
			res.Destroy()
			continue
		}

		// Perform health check by sending a noop command
		wg.Go(func() {
			if err := res.Value().Ping(context.Background()); err != nil {
				if isIOTimeout(err) {
					timeout.Store(true)
				}
				res.Destroy()
				return
			}
			success.Store(true)
			res.ReleaseUnused()
		})
	}
	wg.Wait()

	return success.Load(), timeout.Load()
}

// maxHungProbes caps how many connections confirmHungServer opens at once.
// A TripMinRequests above the cap keeps the trip policy in charge: the
// confirmation burst alone can then never trip the breaker, matching a
// policy that explicitly demands more evidence than a burst provides.
const maxHungProbes = 32

// confirmHungServer probes the server on fresh connections, concurrently,
// and reports every outcome to the breaker. The burst is sized to the trip
// policy's TripMinRequests: on a truly hung server the probe failures alone
// satisfy the volume requirement within one pass (they land within one
// operation timeout of each other, well inside TripWindow) and the breaker
// opens; on a false alarm the recorded successes are just as honest. While
// the breaker is already open the probes are rejected without dialing, so a
// server that stays hung costs each subsequent pass an idle scan and nothing
// else; in the half-open state the first probe is the recovery check,
// reopening the breaker while the hang persists.
func (sp *ServerPool) confirmHungServer() {
	var wg sync.WaitGroup
	for range sp.hungProbes {
		wg.Go(func() {
			_ = sp.reportedProbe()
		})
	}
	wg.Wait()
}

// isIOTimeout reports whether err is an I/O timeout — the hung-server
// signature, as opposed to a fast failure like a refused dial or a reset.
// Caller-attributed timeouts (context.DeadlineExceeded) do not occur here:
// every deadline on the maintenance path is operator-owned.
func isIOTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
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
func (sp *ServerPool) Execute(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) error {
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
