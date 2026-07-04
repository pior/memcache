package memcache

import "sync"

// reapAfterMissedPasses is the number of consecutive health-check passes a
// server must be absent from the set before its pool is reaped. Reaping on the
// first absent pass would let a single flawed discovery response — a List()
// momentarily missing a live server — destroy a healthy warm pool; the grace
// also keeps the race with in-flight operations (which hold a pool fetched
// from a snapshot taken moments earlier) vanishingly rare.
const reapAfterMissedPasses = 2

// serverPools owns the per-address ServerPool map and its lifecycle: lazy
// creation, grace-period reaping of departed servers, and shutdown. Methods
// are safe for concurrent use and never close a pool themselves: they return
// the pools to close so the caller does it outside the lock (puddle's Close
// blocks until in-flight connections are returned, and holding the lock
// across that would stall every other pool operation).
type serverPools struct {
	mu     sync.RWMutex
	byAddr map[string]*poolEntry
	closed bool
}

// poolEntry pairs a pool with its reaping state.
type poolEntry struct {
	sp *ServerPool
	// missedPasses counts the consecutive reapDeparted calls that did not
	// list the entry's address as live; the pool is reaped when it reaches
	// reapAfterMissedPasses.
	missedPasses int
}

func newServerPools() *serverPools {
	return &serverPools{byAddr: make(map[string]*poolEntry)}
}

// getOrCreate returns the pool for addr, lazily creating it with create.
func (p *serverPools) getOrCreate(addr string, create func() (*ServerPool, error)) (*ServerPool, error) {
	// Fast path: read lock
	p.mu.RLock()
	entry, exists := p.byAddr[addr]
	p.mu.RUnlock()
	if exists {
		return entry.sp, nil
	}

	// Slow path: write lock and create
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrClientClosed
	}

	// Double-check after acquiring write lock
	if entry, exists := p.byAddr[addr]; exists {
		return entry.sp, nil
	}

	sp, err := create()
	if err != nil {
		return nil, err
	}
	p.byAddr[addr] = &poolEntry{sp: sp}
	return sp, nil
}

// snapshot returns the current pools, so callers iterate without the lock.
func (p *serverPools) snapshot() []*ServerPool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pools := make([]*ServerPool, 0, len(p.byAddr))
	for _, entry := range p.byAddr {
		pools = append(pools, entry.sp)
	}
	return pools
}

// reapDeparted forgets and returns the pools whose address has been absent
// from live for reapAfterMissedPasses consecutive calls; an address's counter
// resets as soon as it reappears.
func (p *serverPools) reapDeparted(live map[string]struct{}) []*ServerPool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// A caller-driven pass can still arrive after the client closed: leave
	// shutdown to closeAll rather than racing it on the same pools.
	if p.closed {
		return nil
	}

	var departed []*ServerPool
	for addr, entry := range p.byAddr {
		if _, ok := live[addr]; ok {
			entry.missedPasses = 0
			continue
		}
		entry.missedPasses++
		if entry.missedPasses >= reapAfterMissedPasses {
			delete(p.byAddr, addr)
			departed = append(departed, entry.sp)
		}
	}
	return departed
}

// closeAll marks the set closed — subsequent getOrCreate calls fail with
// ErrClientClosed — and returns every pool for the caller to close.
func (p *serverPools) closeAll() []*ServerPool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	pools := make([]*ServerPool, 0, len(p.byAddr))
	for _, entry := range p.byAddr {
		pools = append(pools, entry.sp)
	}
	return pools
}
