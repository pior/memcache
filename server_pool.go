package memcache

import (
	"context"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

func NewServerPool(addr string, config Config) (*ServerPool, error) {
	constructor := func(ctx context.Context) (*Connection, error) {
		netConn, err := config.Dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return NewConnection(netConn), nil
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
		addr:           addr,
		pool:           pool,
		circuitBreaker: breaker,
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr           string
	pool           Pool
	circuitBreaker *gobreaker.CircuitBreaker[bool]
}

func (sp *ServerPool) Address() string {
	return sp.addr
}

// ServerPoolStats contains stats for a single server pool
type ServerPoolStats struct {
	Addr                 string
	PoolStats            PoolStats
	CircuitBreakerState  gobreaker.State
	CircuitBreakerCounts gobreaker.Counts
}

func (sp *ServerPool) Stats() ServerPoolStats {
	stats := ServerPoolStats{
		Addr:      sp.addr,
		PoolStats: sp.pool.Stats(),
	}
	if sp.circuitBreaker != nil {
		stats.CircuitBreakerState = sp.circuitBreaker.State()
		stats.CircuitBreakerCounts = sp.circuitBreaker.Counts()
	}
	return stats
}

// Execute executes a single request-response cycle with proper connection management.
// It handles acquiring a connection, sending the request, reading the response, and
// releasing/destroying the connection based on error conditions.
// The request is wrapped with the server's circuit breaker.
func (sp *ServerPool) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	if sp.circuitBreaker == nil {
		return sp.execRequestDirect(ctx, req)
	}

	var resp *meta.Response
	var execErr error

	_, err := sp.circuitBreaker.Execute(func() (bool, error) {
		resp, execErr = sp.execRequestDirect(ctx, req)
		return execErr == nil, execErr
	})

	if err != nil {
		return nil, err
	}
	return resp, execErr
}

// execRequestDirect performs the actual request execution without circuit breaker.
func (sp *ServerPool) execRequestDirect(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	resource, err := sp.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	conn := resource.Value()

	resp, err := conn.Send(req)
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
		return execErr == nil, execErr
	})

	if err != nil {
		return nil, err
	}
	return responses, execErr
}

// execBatchDirect performs the actual batch execution without circuit breaker.
func (sp *ServerPool) execBatchDirect(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	resource, err := sp.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	conn := resource.Value()

	// Write all requests
	for _, req := range reqs {
		if err := meta.WriteRequest(conn.Writer, req); err != nil {
			resource.Destroy()
			return nil, err
		}
	}

	// Write NoOp marker to signal end of batch
	noopReq := meta.NewRequest(meta.CmdNoOp, "", nil, nil)
	if err := meta.WriteRequest(conn.Writer, noopReq); err != nil {
		resource.Destroy()
		return nil, err
	}

	// Flush all writes
	if err := conn.Writer.Flush(); err != nil {
		resource.Destroy()
		return nil, err
	}

	// Read responses until NoOp
	// ReadResponseBatch(r, 0, true) reads until StatusMN (NoOp marker)
	responses, err := meta.ReadResponseBatch(conn.Reader, 0, true)
	if err != nil {
		if meta.ShouldCloseConnection(err) {
			resource.Destroy()
		} else {
			resource.Release()
		}
		return nil, err
	}

	// Remove the NoOp response from the end
	if len(responses) > 0 && responses[len(responses)-1].Status == meta.StatusMN {
		responses = responses[:len(responses)-1]
	}

	resource.Release()
	return responses, nil
}
