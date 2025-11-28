package memcache

import (
	"context"

	"github.com/pior/memcache/meta"
)

// Executor provides a high-level abstraction for executing memcache requests.
// It hides the complexity of connection management, pooling, circuit breakers,
// server selection, etc. from the command layer.
//
// An Executor represents a "virtual connection" that the Commands layer uses
// to execute requests, abstracting away whether it's a single server or
// multi-server setup, whether circuit breakers are involved, etc.
type Executor interface {
	// Execute executes a memcache request and returns the response.
	// The key parameter is used for server selection in multi-server setups.
	Execute(ctx context.Context, key string, req *meta.Request) (*meta.Response, error)
}

// ConnectionProvider is a strategy for providing Executors to the Commands layer.
// Different implementations can provide different levels of resilience:
//   - SimpleProvider: Direct connection to a single server
//   - ResilientProvider: Multi-server with circuit breakers and pooling
type ConnectionProvider interface {
	// NewExecutor creates a new executor for executing requests.
	NewExecutor() Executor

	// Close closes the provider and releases all resources.
	Close() error

	// Stats returns statistics about the provider (optional, can return nil).
	Stats() interface{}
}
