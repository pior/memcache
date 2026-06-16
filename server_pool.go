package memcache

import (
	"context"
	"errors"
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

	pool, err := config.NewPool(constructor, config.MaxSize)
	if err != nil {
		return nil, err
	}

	var breaker *gobreaker.CircuitBreaker[bool]
	if config.CircuitBreakerSettings != nil {
		settings := *config.CircuitBreakerSettings
		settings.Name = addr

		breaker = gobreaker.NewCircuitBreaker[bool](settings)
	}

	return &ServerPool{
		addr:            addr,
		pool:            pool,
		circuitBreaker:  breaker,
		maxConnLifetime: config.MaxConnLifetime,
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr            string
	pool            Pool
	circuitBreaker  *gobreaker.CircuitBreaker[bool]
	maxConnLifetime time.Duration
}

// release returns a connection to the pool, or destroys it if it has
// exceeded MaxConnLifetime. Enforcing the lifetime here (and not only in the
// health check loop) matters under sustained load: a saturated pool never has
// idle connections, so the health check alone would never recycle them.
func (sp *ServerPool) release(resource Resource) {
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
	Addr           string
	Conns          ConnPoolMetrics
	CircuitBreaker CircuitBreakerStats
}

// CircuitBreakerStats is a snapshot of a server's circuit breaker, decoupled
// from the underlying gobreaker types. When no circuit breaker is configured,
// State is empty and the counts are zero.
type CircuitBreakerStats struct {
	State                string // "", "closed", "open" or "half-open"
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

func (sp *ServerPool) Metrics() PoolMetrics {
	metrics := PoolMetrics{
		Addr:  sp.addr,
		Conns: sp.pool.Metrics(),
	}
	if sp.circuitBreaker != nil {
		counts := sp.circuitBreaker.Counts()
		metrics.CircuitBreaker = CircuitBreakerStats{
			State:                sp.circuitBreaker.State().String(),
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
// It handles acquiring a connection, sending the request, reading the response, and
// releasing/destroying the connection based on error conditions.
// The request is wrapped with the server's circuit breaker.
//
// Failures are returned as *OpError carrying the operation, key, and server address.
func (sp *ServerPool) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	if sp.circuitBreaker == nil {
		return sp.execRequestDirect(ctx, req)
	}

	var resp *meta.Response
	var execErr error

	_, err := sp.circuitBreaker.Execute(func() (bool, error) {
		resp, execErr = sp.execRequestDirect(ctx, req)
		return execErr == nil, breakerError(execErr)
	})

	if err != nil {
		// Errors from execRequestDirect are already wrapped; breaker state
		// errors (open, too many requests) are not.
		return nil, sp.wrapErr(string(req.Command), req.Key, err)
	}
	return resp, execErr
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

// breakerError filters out errors that don't indicate server trouble, so they
// don't count as failures and trip the circuit breaker: a caller canceling its
// context or passing an invalid key says nothing about the server's health.
func breakerError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	var invalidKey *meta.InvalidKeyError
	if errors.As(err, &invalidKey) {
		return nil
	}
	return err
}

// execRequestDirect performs the actual request execution without circuit breaker.
func (sp *ServerPool) execRequestDirect(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	op := string(req.Command)

	resource, err := sp.pool.Acquire(ctx)
	if err != nil {
		return nil, sp.wrapErr(op, req.Key, err)
	}

	conn := resource.Value()

	resp, err := conn.Execute(ctx, req)
	if err != nil {
		if meta.ShouldCloseConnection(err) {
			resource.Destroy()
		} else {
			sp.release(resource)
		}
		return nil, sp.wrapErr(op, req.Key, err)
	}

	// Protocol errors are reported in resp.Error rather than as Go errors;
	// some of them (e.g. CLIENT_ERROR) corrupt the protocol state and require
	// closing the connection instead of returning it to the pool.
	if resp.Error != nil && meta.ShouldCloseConnection(resp.Error) {
		resource.Destroy()
	} else {
		sp.release(resource)
	}
	return resp, nil
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

	if sp.circuitBreaker == nil {
		return sp.execBatchDirect(ctx, reqs)
	}

	var responses []*meta.Response
	var execErr error

	_, err := sp.circuitBreaker.Execute(func() (bool, error) {
		responses, execErr = sp.execBatchDirect(ctx, reqs)
		return execErr == nil, breakerError(execErr)
	})

	if err != nil {
		return nil, sp.wrapErr(OpBatch, "", err)
	}
	return responses, execErr
}

// execBatchDirect performs the actual batch execution without circuit breaker.
func (sp *ServerPool) execBatchDirect(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	resource, err := sp.pool.Acquire(ctx)
	if err != nil {
		return nil, sp.wrapErr(OpBatch, "", err)
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
