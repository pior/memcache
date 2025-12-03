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

	return &ServerPool{
		addr:           addr,
		pool:           pool,
		circuitBreaker: config.NewCircuitBreaker(addr),
	}, nil
}

// ServerPool wraps a pool, a circuit breaker with its server address.
type ServerPool struct {
	addr           string
	pool           Pool
	circuitBreaker *gobreaker.CircuitBreaker[*meta.Response]
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

	return sp.circuitBreaker.Execute(func() (*meta.Response, error) {
		return sp.execRequestDirect(ctx, req)
	})
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
// Note: Circuit breaker state is checked but not used to wrap the batch execution,
// since the circuit breaker is typed for single responses. If the circuit is open,
// this will return an error immediately.
func (sp *ServerPool) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	// Check circuit breaker state (if present) but don't wrap execution
	// since circuit breaker is typed for single Response, not batch
	if sp.circuitBreaker != nil && sp.circuitBreaker.State() == gobreaker.StateOpen {
		return nil, gobreaker.ErrOpenState
	}

	return sp.execBatchDirect(ctx, reqs)
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
