package memcache

import (
	"context"

	"github.com/jackc/puddle/v2"
)

// NewPuddlePool creates a new puddle-based connection pool.
// Use this as Config.Pool to use the puddle pool implementation.
func NewPuddlePool(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error) {
	poolConfig := &puddle.Config[*Connection]{
		Constructor: constructor,
		Destructor:  func(c *Connection) { c.Close() },
		MaxSize:     maxSize,
	}

	pool, err := puddle.NewPool(poolConfig)
	if err != nil {
		return nil, err
	}

	return &puddlePool{pool: pool}, nil
}

// puddlePool wraps puddle.Pool to implement our Pool interface.
type puddlePool struct {
	pool *puddle.Pool[*Connection]
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
		CreatedConns:      0,                             // Not tracked by puddle
		DestroyedConns:    0,                             // Not tracked by puddle
		AcquireErrors:     uint64(s.CanceledAcquireCount()),
		AcquireWaitTimeNs: uint64(s.EmptyAcquireWaitTime().Nanoseconds()),
	}
}
