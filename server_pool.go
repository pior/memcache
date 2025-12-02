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
