package memcache

import (
	"context"
	"time"
)

// Resource represents a connection resource from the pool.
type Resource interface {
	// Value returns the underlying connection.
	Value() *conn

	// Release returns the connection to the pool for reuse.
	Release()

	// ReleaseUnused returns the connection to the pool without marking it as used.
	// Used for health checks that don't actually use the connection.
	ReleaseUnused()

	// Destroy closes the connection and removes it from the pool.
	Destroy()

	// CreationTime returns when the connection was created.
	CreationTime() time.Time

	// IdleDuration returns how long the connection has been idle.
	IdleDuration() time.Duration
}

// Pool manages a pool of connections.
type Pool interface {
	// Acquire gets a connection from the pool, creating one if necessary.
	// Blocks until a connection is available or context is canceled.
	Acquire(ctx context.Context) (Resource, error)

	// AcquireAllIdle acquires all idle connections from the pool.
	// Used for health checks and maintenance.
	AcquireAllIdle() []Resource

	// Close closes the pool and all connections.
	Close()

	// Stats returns a snapshot of pool statistics.
	Stats() PoolStats
}
