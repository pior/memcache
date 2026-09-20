package memcache

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/jackc/puddle/v2"
)

// newPuddlePool creates the connection pool, backed by jackc/puddle.
func newPuddlePool(constructor func(ctx context.Context) (*Connection, error), maxSize int) (connPool, error) {
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
		MaxSize: int32(maxSize),
	}

	pool, err := puddle.NewPool(poolConfig)
	if err != nil {
		return nil, err
	}
	p.pool = pool
	return p, nil
}

// puddlePool wraps puddle.Pool to implement the connPool interface.
type puddlePool struct {
	pool           *puddle.Pool[*Connection]
	createdConns   atomic.Int64
	destroyedConns atomic.Int64
}

func (p *puddlePool) Acquire(ctx context.Context) (poolResource, error) {
	res, err := p.pool.Acquire(ctx)
	if err != nil {
		if errors.Is(err, puddle.ErrClosedPool) {
			return nil, ErrPoolClosed
		}
		return nil, err
	}
	return res, nil
}

func (p *puddlePool) AcquireAllIdle() []poolResource {
	puddleResources := p.pool.AcquireAllIdle()
	resources := make([]poolResource, len(puddleResources))
	for i, res := range puddleResources {
		resources[i] = res
	}
	return resources
}

func (p *puddlePool) Close() {
	p.pool.Close()
}

// Metrics returns a snapshot of pool statistics by converting puddle's stats to our format.
func (p *puddlePool) Metrics() ConnPoolMetrics {
	s := p.pool.Stat()

	// Map puddle stats to our ConnPoolMetrics structure
	// Note: Puddle tracks similar metrics but with different semantics
	return ConnPoolMetrics{
		TotalConns:          int(s.TotalResources()),
		IdleConns:           int(s.IdleResources()),
		ActiveConns:         int(s.AcquiredResources()),
		AcquireCount:        s.AcquireCount(),
		AcquireWaitCount:    s.EmptyAcquireCount(), // Acquires that had to wait (pool was empty)
		CreatedConns:        p.createdConns.Load(),
		DestroyedConns:      p.destroyedConns.Load(),
		AcquireErrors:       s.CanceledAcquireCount(),
		AcquireWaitDuration: s.EmptyAcquireWaitTime(),
	}
}
