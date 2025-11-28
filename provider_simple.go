package memcache

import (
	"context"
	"fmt"
	"net"

	"github.com/pior/memcache/meta"
)

var defaultDialer = net.Dialer{}

// SimpleProvider provides a straightforward executor for a single memcache server.
// It uses connection pooling but without circuit breakers or server selection complexity.
//
// This is ideal for:
//   - Single-server setups
//   - Development/testing
//   - Situations where you want minimal overhead
type SimpleProvider struct {
	pool Pool
}

// NewSimpleProvider creates a simple connection provider for a single server.
func NewSimpleProvider(addr string, config Config) (*SimpleProvider, error) {
	// Apply defaults
	if config.Dialer == nil {
		config.Dialer = &defaultDialer
	}
	if config.Pool == nil {
		config.Pool = NewChannelPool
	}

	// Create connection constructor
	constructor := config.constructor
	if constructor == nil {
		constructor = func(ctx context.Context) (*Connection, error) {
			netConn, err := config.Dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return NewConnection(netConn), nil
		}
	}

	// Create pool
	pool, err := config.Pool(constructor, config.MaxSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}

	return &SimpleProvider{
		pool: pool,
	}, nil
}

// NewExecutor creates a new executor.
func (p *SimpleProvider) NewExecutor() Executor {
	return &simpleExecutor{pool: p.pool}
}

// Close closes the provider and all pooled connections.
func (p *SimpleProvider) Close() error {
	p.pool.Close()
	return nil
}

// Stats returns pool statistics.
func (p *SimpleProvider) Stats() interface{} {
	return p.pool.Stats()
}

// simpleExecutor is a straightforward executor that uses a connection pool.
type simpleExecutor struct {
	pool Pool
}

// Execute executes a request using a pooled connection.
// The key parameter is ignored for single-server setups.
func (e *simpleExecutor) Execute(ctx context.Context, key string, req *meta.Request) (*meta.Response, error) {
	// Acquire connection from pool
	res, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	// Execute request
	resp, err := res.Value().Send(req)
	if err != nil {
		res.Destroy()
		return nil, err
	}

	// Return connection to pool
	res.Release()
	return resp, nil
}
