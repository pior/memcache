package memcache

import (
	"context"
	"sync"
	"time"

	"github.com/pior/memcache/internal/coarsetime"
)

// NewChannelPool creates a new channel-based connection pool.
// This is an alternative pool implementation, optimized for performance.
func NewChannelPool(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error) {
	return &channelPool{
		constructor: constructor,
		maxSize:     maxSize,
		resources:   make(chan *channelResource, maxSize),
	}, nil
}

// channelResource implements Resource for channel pool.
type channelResource struct {
	conn         *Connection
	pool         *channelPool
	creationTime time.Time
	lastUsedTime time.Time
}

func (r *channelResource) Value() *Connection {
	return r.conn
}

func (r *channelResource) Release() {
	r.lastUsedTime = coarsetime.Now()
	r.pool.put(r)
}

func (r *channelResource) ReleaseUnused() {
	// Don't update lastUsedTime for health checks
	r.pool.put(r)
}

func (r *channelResource) Destroy() {
	r.conn.Close()
	r.pool.removeResource()
}

func (r *channelResource) CreationTime() time.Time {
	return r.creationTime
}

func (r *channelResource) IdleDuration() time.Duration {
	return time.Since(r.lastUsedTime)
}

// channelPool is a simple, allocation-optimized connection pool using Go channels.
type channelPool struct {
	constructor func(ctx context.Context) (*Connection, error)
	maxSize     int32

	mu        sync.Mutex
	resources chan *channelResource
	size      int32
	closed    bool

	stats poolStatsCollector
}

func (p *channelPool) Acquire(ctx context.Context) (Resource, error) {
	p.stats.recordAcquire()

	// Try to get an idle connection from the pool first
	select {
	case res := <-p.resources:
		p.stats.recordAcquireFromIdle()
		return res, nil
	default:
		// No idle connection, create new one if under limit
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.stats.recordAcquireError()
		return nil, context.Canceled
	}

	// Check if we can create a new connection
	if p.size < p.maxSize {
		p.size++
		p.mu.Unlock()

		conn, err := p.constructor(ctx)
		if err != nil {
			p.mu.Lock()
			p.size--
			p.mu.Unlock()
			p.stats.recordAcquireError()
			return nil, err
		}

		p.stats.recordCreate()
		p.stats.recordActivate() // New connection goes straight to active

		now := coarsetime.Now()
		return &channelResource{
			conn:         conn,
			pool:         p,
			creationTime: now,
			lastUsedTime: now,
		}, nil
	}
	p.mu.Unlock()

	// Pool is full, wait for a connection to be released
	waitStart := coarsetime.Now()
	select {
	case res := <-p.resources:
		p.stats.recordAcquireWait(time.Since(waitStart))
		p.stats.recordAcquireFromIdle()
		return res, nil
	case <-ctx.Done():
		p.stats.recordAcquireError()
		return nil, ctx.Err()
	}
}

func (p *channelPool) put(res *channelResource) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		res.conn.Close()
		return
	}
	p.mu.Unlock()

	select {
	case p.resources <- res:
		// Successfully returned to pool
		p.stats.recordRelease()
	default:
		// Pool channel is full, close this connection
		res.conn.Close()
		p.removeResource()
	}
}

func (p *channelPool) removeResource() {
	p.mu.Lock()
	p.size--
	p.mu.Unlock()
	p.stats.recordDestroy()
}

func (p *channelPool) AcquireAllIdle() []Resource {
	var idle []Resource

	// Drain all idle connections from the channel
	for {
		select {
		case res := <-p.resources:
			idle = append(idle, res)
		default:
			return idle
		}
	}
}

func (p *channelPool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	// Close all idle connections
	close(p.resources)
	for res := range p.resources {
		res.conn.Close()
	}
}

// Stats returns a snapshot of pool statistics.
func (p *channelPool) Stats() PoolStats {
	return p.stats.snapshot()
}
