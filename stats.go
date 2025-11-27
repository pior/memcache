package memcache

import (
	"sync/atomic"
	"time"
)

// PoolStats contains statistics about a connection pool.
// All fields are safe for concurrent access.
//
// Struct is optimized to fit within a single cache line (64 bytes).
// Fields are ordered largest to smallest for optimal memory layout.
//
// For Prometheus integration, expose these as:
//   - Gauges: TotalConns, IdleConns, ActiveConns
//   - Counters: AcquireCount, AcquireWaitCount, CreatedConns, DestroyedConns, AcquireErrors
//   - Histogram: AcquireWaitDuration (use AcquireWaitCount and AcquireWaitTimeNs to calculate)
type PoolStats struct {
	// Lifetime counters (uint64 - 8 bytes each)
	AcquireCount      uint64 // Total acquire attempts
	AcquireWaitCount  uint64 // Acquires that had to wait
	CreatedConns      uint64 // Total connections created
	DestroyedConns    uint64 // Total connections destroyed
	AcquireErrors     uint64 // Failed acquire attempts
	AcquireWaitTimeNs uint64 // Total nanoseconds spent waiting

	// Current state gauges (int32 - 4 bytes each)
	TotalConns  int32 // Total connections in pool (active + idle)
	IdleConns   int32 // Idle connections available
	ActiveConns int32 // Connections currently in use
	_           int32 // Padding to align to 64 bytes
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
