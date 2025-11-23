package memcache

import (
	"context"

	"github.com/jackc/puddle/v2"
)

// puddlePool wraps puddle.Pool to implement our Pool interface.
type puddlePool struct {
	pool *puddle.Pool[*conn]
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

// NewPuddlePool creates a new puddle-based connection pool.
// Use this as Config.Pool to use the puddle pool implementation.
func NewPuddlePool(constructor func(ctx context.Context) (*conn, error), maxSize int32) (Pool, error) {
	poolConfig := &puddle.Config[*conn]{
		Constructor: constructor,
		Destructor:  func(c *conn) { c.Close() },
		MaxSize:     maxSize,
	}

	pool, err := puddle.NewPool(poolConfig)
	if err != nil {
		return nil, err
	}

	return &puddlePool{pool: pool}, nil
}
