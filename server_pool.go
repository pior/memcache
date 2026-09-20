package memcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// NewServerPool creates the connection pool and circuit breaker for one
// server. Zero-value config fields get the same defaults NewClient applies.
func NewServerPool(addr string, config Config) (*ServerPool, error) {
	config.setDefaults()

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

	return &ServerPool{
		addr:            addr,
		pool:            pool,
		breaker:         newBreaker(addr, config.Breaker),
		maxConnLifetime: config.MaxConnLifetime,
		maxConnIdleTime: config.MaxConnIdleTime,
		maxSize:         config.MaxSize,
		idleConnCheck:   config.IdleConnCheckThreshold,
		pingTimeout:     config.Timeout,
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr            string
	pool            connPool
	breaker         *gobreaker.CircuitBreaker[bool]
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
// It handles acquiring a connection, sending the request, calling fn with the
// response while the connection is still checked out, and releasing/destroying the
// connection based on error conditions.
// The request is wrapped with the server's circuit breaker.
//
// Execution failures are returned as *OpError carrying the operation, key, and
// server address. Protocol errors carried by the response (resp.Error) are not
// errors here: they are the command's outcome, left to fn, and do not count as
// circuit breaker failures. A reply the command cannot produce is an execution
// failure (a *meta.ParseError, see Connection.Execute): the connection is
// destroyed and fn is not called.
func (sp *ServerPool) Execute(ctx context.Context, req *meta.Request, fn ResponseFunc) error {
	if sp.breaker == nil {
		return sp.execRequestDirect(ctx, req, fn)
	}

	_, err := sp.breaker.Execute(func() (bool, error) {
		err := sp.execRequestDirect(ctx, req, fn)
		return err == nil, err
	})
	if err != nil {
		// Errors from execRequestDirect are already wrapped; breaker
		// rejections surface as ErrBreakerOpen and get wrapped here.
		return sp.wrapErr(string(req.Command), req.Key, mapBreakerRejection(err))
	}
	return nil
}

// wrapErr wraps an error with operation and server context, unless it
// already carries it.
func (sp *ServerPool) wrapErr(op, key string, err error) error {
	if _, ok := errors.AsType[*OpError](err); ok {
		return err
	}
	return &OpError{Op: op, Key: key, Server: sp.addr, Err: err}
}

// execRequestDirect performs the actual request execution without circuit breaker.
// The connection is released (or destroyed) only after fn has run, so the
// response buffers cannot be reused by another operation while fn reads them.
// Execution failures are returned wrapped in *OpError.
func (sp *ServerPool) execRequestDirect(ctx context.Context, req *meta.Request, fn ResponseFunc) error {
	op := string(req.Command)

	resource, err := sp.acquireHealthy(ctx)
	if err != nil {
		// The prefix distinguishes a failure to get a connection (pool
		// saturation, dial) from an I/O failure on the wire.
		return sp.wrapErr(op, req.Key, fmt.Errorf("acquire: %w", err))
	}

	// connErr is what decides whether the connection can be reused: the
	// transport error if there was one, else the protocol error carried by
	// the response (some, e.g. CLIENT_ERROR, corrupt the protocol state).
	// nil means reusable. finished stays false only if fn panics: the
	// connection is then destroyed rather than leaking the pool slot, and
	// the panic propagates.
	var connErr error
	finished := false
	defer func() {
		if !finished || meta.ShouldCloseConnection(connErr) {
			resource.Destroy()
			return
		}
		sp.release(resource)
	}()

	var respErr error
	err = resource.Value().Execute(ctx, req, func(resp *meta.Response) {
		respErr = resp.Error
		fn(resp)
	})
	connErr = err
	if err == nil {
		connErr = respErr
	}
	finished = true

	if err != nil {
		return sp.wrapErr(op, req.Key, err)
	}
	return nil
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
