package memcache

import (
	"sync/atomic"
	"time"
)

// PoolStats contains statistics about a connection pool.
// All fields are safe for concurrent access.
//
// For Prometheus integration, expose these as:
//   - Gauges: TotalConns, IdleConns, ActiveConns
//   - Counters: AcquireCount, AcquireWaitCount, CreatedConns, DestroyedConns, AcquireErrors
//   - Histogram: AcquireWaitDuration (use AcquireWaitCount and AcquireWaitDuration to calculate)
type PoolStats struct {
	// Current state (gauges)
	TotalConns  int32 // Total connections in pool (active + idle)
	IdleConns   int32 // Idle connections available
	ActiveConns int32 // Connections currently in use

	// Lifetime counters
	AcquireCount      uint64 // Total acquire attempts
	AcquireWaitCount  uint64 // Acquires that had to wait
	CreatedConns      uint64 // Total connections created
	DestroyedConns    uint64 // Total connections destroyed
	AcquireErrors     uint64 // Failed acquire attempts
	AcquireWaitTimeNs uint64 // Total nanoseconds spent waiting (for average calculation)
}

// AverageWaitTime returns the average duration spent waiting for connections.
// Returns 0 if no waits occurred.
func (s *PoolStats) AverageWaitTime() time.Duration {
	count := atomic.LoadUint64(&s.AcquireWaitCount)
	if count == 0 {
		return 0
	}
	total := atomic.LoadUint64(&s.AcquireWaitTimeNs)
	return time.Duration(total / count)
}

// ClientStats contains statistics about client operations.
// All fields are safe for concurrent access.
//
// For Prometheus integration, expose these as:
//   - Counters: Gets, Sets, Deletes, Increments, Errors (with operation label)
//   - Counters: CacheHits, CacheMisses
type ClientStats struct {
	// Operation counters
	Gets       uint64 // Total Get operations
	Sets       uint64 // Total Set operations
	Adds       uint64 // Total Add operations
	Deletes    uint64 // Total Delete operations
	Increments uint64 // Total Increment operations

	// Result counters
	CacheHits   uint64 // Successful Get operations (key found)
	CacheMisses uint64 // Failed Get operations (key not found)
	Errors      uint64 // Total errors across all operations

	// Connection management
	ConnectionsDestroyed uint64 // Connections destroyed due to errors
}

// HitRate returns the cache hit rate as a value between 0 and 1.
// Returns 0 if no Get operations have been performed.
func (s *ClientStats) HitRate() float64 {
	hits := atomic.LoadUint64(&s.CacheHits)
	misses := atomic.LoadUint64(&s.CacheMisses)
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// poolStatsCollector provides internal methods for updating pool stats.
// Not exported - pools update their own stats.
type poolStatsCollector struct {
	stats *PoolStats
}

func newPoolStatsCollector() *poolStatsCollector {
	return &poolStatsCollector{
		stats: &PoolStats{},
	}
}

func (c *poolStatsCollector) recordAcquire() {
	atomic.AddUint64(&c.stats.AcquireCount, 1)
}

func (c *poolStatsCollector) recordAcquireWait(duration time.Duration) {
	atomic.AddUint64(&c.stats.AcquireWaitCount, 1)
	atomic.AddUint64(&c.stats.AcquireWaitTimeNs, uint64(duration.Nanoseconds()))
}

func (c *poolStatsCollector) recordCreate() {
	atomic.AddUint64(&c.stats.CreatedConns, 1)
	atomic.AddInt32(&c.stats.TotalConns, 1)
}

func (c *poolStatsCollector) recordDestroy() {
	atomic.AddUint64(&c.stats.DestroyedConns, 1)
	atomic.AddInt32(&c.stats.TotalConns, -1)
}

func (c *poolStatsCollector) recordAcquireError() {
	atomic.AddUint64(&c.stats.AcquireErrors, 1)
}

func (c *poolStatsCollector) recordAcquireFromIdle() {
	atomic.AddInt32(&c.stats.IdleConns, -1)
	atomic.AddInt32(&c.stats.ActiveConns, 1)
}

func (c *poolStatsCollector) recordActivate() {
	atomic.AddInt32(&c.stats.ActiveConns, 1)
}

func (c *poolStatsCollector) recordRelease() {
	atomic.AddInt32(&c.stats.IdleConns, 1)
	atomic.AddInt32(&c.stats.ActiveConns, -1)
}

func (c *poolStatsCollector) snapshot() PoolStats {
	return PoolStats{
		TotalConns:        atomic.LoadInt32(&c.stats.TotalConns),
		IdleConns:         atomic.LoadInt32(&c.stats.IdleConns),
		ActiveConns:       atomic.LoadInt32(&c.stats.ActiveConns),
		AcquireCount:      atomic.LoadUint64(&c.stats.AcquireCount),
		AcquireWaitCount:  atomic.LoadUint64(&c.stats.AcquireWaitCount),
		CreatedConns:      atomic.LoadUint64(&c.stats.CreatedConns),
		DestroyedConns:    atomic.LoadUint64(&c.stats.DestroyedConns),
		AcquireErrors:     atomic.LoadUint64(&c.stats.AcquireErrors),
		AcquireWaitTimeNs: atomic.LoadUint64(&c.stats.AcquireWaitTimeNs),
	}
}

// clientStatsCollector provides internal methods for updating client stats.
// Not exported - client updates its own stats.
type clientStatsCollector struct {
	stats *ClientStats
}

func newClientStatsCollector() *clientStatsCollector {
	return &clientStatsCollector{
		stats: &ClientStats{},
	}
}

func (c *clientStatsCollector) recordGet(found bool) {
	atomic.AddUint64(&c.stats.Gets, 1)
	if found {
		atomic.AddUint64(&c.stats.CacheHits, 1)
	} else {
		atomic.AddUint64(&c.stats.CacheMisses, 1)
	}
}

func (c *clientStatsCollector) recordSet() {
	atomic.AddUint64(&c.stats.Sets, 1)
}

func (c *clientStatsCollector) recordAdd() {
	atomic.AddUint64(&c.stats.Adds, 1)
}

func (c *clientStatsCollector) recordDelete() {
	atomic.AddUint64(&c.stats.Deletes, 1)
}

func (c *clientStatsCollector) recordIncrement() {
	atomic.AddUint64(&c.stats.Increments, 1)
}

func (c *clientStatsCollector) recordError() {
	atomic.AddUint64(&c.stats.Errors, 1)
}

func (c *clientStatsCollector) recordConnectionDestroyed() {
	atomic.AddUint64(&c.stats.ConnectionsDestroyed, 1)
}

func (c *clientStatsCollector) snapshot() ClientStats {
	return ClientStats{
		Gets:                 atomic.LoadUint64(&c.stats.Gets),
		Sets:                 atomic.LoadUint64(&c.stats.Sets),
		Adds:                 atomic.LoadUint64(&c.stats.Adds),
		Deletes:              atomic.LoadUint64(&c.stats.Deletes),
		Increments:           atomic.LoadUint64(&c.stats.Increments),
		CacheHits:            atomic.LoadUint64(&c.stats.CacheHits),
		CacheMisses:          atomic.LoadUint64(&c.stats.CacheMisses),
		Errors:               atomic.LoadUint64(&c.stats.Errors),
		ConnectionsDestroyed: atomic.LoadUint64(&c.stats.ConnectionsDestroyed),
	}
}
