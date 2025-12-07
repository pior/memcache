package memcache

import (
	"context"
	"sync/atomic"

	"github.com/jackc/puddle/v2"
)

// NewPuddlePool creates a new puddle-based connection pool.
// This is the default pool implementation.
func NewPuddlePool(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error) {
	p := &puddlePool{}

	poolConfig := &puddle.Config[*Connection]{
		Constructor: func(ctx context.Context) (*Connection, error) {
			conn, err := constructor(ctx)
			if err == nil {
				p.createdConns.Add(1)
			}
			return conn, err
		},
		Destructor: func(c *Connection) {
			p.destroyedConns.Add(1)
			_ = c.Close()
		},
		MaxSize: maxSize,
	}

	pool, err := puddle.NewPool(poolConfig)
	if err != nil {
		return nil, err
	}
	p.pool = pool
	return p, nil
}

// puddlePool wraps puddle.Pool to implement our Pool interface.
type puddlePool struct {
	pool           *puddle.Pool[*Connection]
	createdConns   atomic.Int64
	destroyedConns atomic.Int64
}

func (p *puddlePool) Acquire(ctx context.Context) (Resource, error) {
	return p.pool.Acquire(ctx)
}

func (p *puddlePool) AcquireAllIdle() []Resource {
	puddleResources := p.pool.AcquireAllIdle()
	resources := make([]Resource, len(puddleResources))
	for i, res := range puddleResources {
		resources[i] = res
	}
	return resources
}

func (p *puddlePool) Close() {
	p.pool.Close()
}

// Stats returns a snapshot of pool statistics by converting puddle's stats to our format.
func (p *puddlePool) Stats() PoolStats {
	s := p.pool.Stat()

	// Map puddle stats to our PoolStats structure
	// Note: Puddle tracks similar metrics but with different semantics
	return PoolStats{
		TotalConns:        s.TotalResources(),
		IdleConns:         s.IdleResources(),
		ActiveConns:       s.AcquiredResources(),
		AcquireCount:      uint64(s.AcquireCount()),
		AcquireWaitCount:  uint64(s.EmptyAcquireCount()), // Acquires that had to wait (pool was empty)
		CreatedConns:      uint64(p.createdConns.Load()),
		DestroyedConns:    uint64(p.destroyedConns.Load()),
		AcquireErrors:     uint64(s.CanceledAcquireCount()),
		AcquireWaitTimeNs: uint64(s.EmptyAcquireWaitTime().Nanoseconds()),
	}
}
