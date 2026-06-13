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
		done:        make(chan struct{}),
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
//
// The resources channel is never closed: closing it would race with concurrent
// sends in put(). Close drains it under the closed flag instead, and put()
// closes connections directly once the pool is closed.
type channelPool struct {
	constructor func(ctx context.Context) (*Connection, error)
	maxSize     int32

	mu        sync.Mutex
	resources chan *channelResource
	size      int32
	closed    bool
	done      chan struct{} // closed when the pool is closed, unblocks waiting Acquires

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
		return nil, ErrPoolClosed
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
	case <-p.done:
		p.stats.recordAcquireError()
		return nil, ErrPoolClosed
	case <-ctx.Done():
		p.stats.recordAcquireError()
		return nil, ctx.Err()
	}
}

func (p *channelPool) put(res *channelResource) {
	// The send must happen under the lock: a check-then-send without it races
	// with Close, which would leave the connection stranded in the channel.
	p.mu.Lock()
	if p.closed {
		p.size--
		p.mu.Unlock()
		res.conn.Close()
		p.stats.recordDestroyActive()
		return
	}

	select {
	case p.resources <- res:
		// Successfully returned to pool
		p.mu.Unlock()
		p.stats.recordRelease()
	default:
		// Pool channel is full, close this connection
		p.size--
		p.mu.Unlock()
		res.conn.Close()
		p.stats.recordDestroyActive()
	}
}

func (p *channelPool) removeResource() {
	p.mu.Lock()
	p.size--
	p.mu.Unlock()
	p.stats.recordDestroyActive()
}

func (p *channelPool) AcquireAllIdle() []Resource {
	var idle []Resource

	// Drain all idle connections from the channel. The drained resources are
	// in use until released or destroyed: account them as active.
	for {
		select {
		case res := <-p.resources:
			p.stats.recordAcquireFromIdle()
			idle = append(idle, res)
		default:
			return idle
		}
	}
}

func (p *channelPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.done)

	// Drain and close all idle connections. The closed flag guarantees no new
	// sends to the channel, so draining until empty is complete.
	for {
		select {
		case res := <-p.resources:
			p.size--
			res.conn.Close()
			p.stats.recordDestroyIdle()
		default:
			p.mu.Unlock()
			return
		}
	}
}

// Stats returns a snapshot of pool statistics.
func (p *channelPool) Stats() PoolStats {
	return p.stats.snapshot()
}
