package memcache

import (
	"sync/atomic"
	"time"
)

// PoolMetrics is a point-in-time snapshot of a connection pool's statistics.
//
// For Prometheus integration, expose these as:
//   - Gauges: TotalConns, IdleConns, ActiveConns
//   - Counters: AcquireCount, AcquireWaitCount, CreatedConns, DestroyedConns, AcquireErrors
//   - Histogram: AcquireWaitDuration (use AcquireWaitCount and AcquireWaitTimeNs to calculate)
type PoolMetrics struct {
	// Lifetime counters
	AcquireCount      uint64 // Total acquire attempts
	AcquireWaitCount  uint64 // Acquires that had to wait
	CreatedConns      uint64 // Total connections created
	DestroyedConns    uint64 // Total connections destroyed
	AcquireErrors     uint64 // Failed acquire attempts
	AcquireWaitTimeNs uint64 // Total nanoseconds spent waiting

	// Current state gauges
	TotalConns  int32 // Total connections in pool (active + idle)
	IdleConns   int32 // Idle connections available
	ActiveConns int32 // Connections currently in use
}

// poolMetricsCollector accumulates pool statistics using atomic counters.
// Not exported - pools update their own stats and expose a PoolMetrics snapshot.
type poolMetricsCollector struct {
	acquireCount      atomic.Uint64
	acquireWaitCount  atomic.Uint64
	createdConns      atomic.Uint64
	destroyedConns    atomic.Uint64
	acquireErrors     atomic.Uint64
	acquireWaitTimeNs atomic.Uint64

	totalConns  atomic.Int32
	idleConns   atomic.Int32
	activeConns atomic.Int32
}

func (c *poolMetricsCollector) recordAcquire() {
	c.acquireCount.Add(1)
}

func (c *poolMetricsCollector) recordAcquireWait(duration time.Duration) {
	c.acquireWaitCount.Add(1)
	c.acquireWaitTimeNs.Add(uint64(duration.Nanoseconds()))
}

func (c *poolMetricsCollector) recordCreate() {
	c.createdConns.Add(1)
	c.totalConns.Add(1)
}

// recordDestroyActive records the destruction of a connection that was in use
// (acquired from the pool, or just drained from the idle channel).
func (c *poolMetricsCollector) recordDestroyActive() {
	c.destroyedConns.Add(1)
	c.totalConns.Add(-1)
	c.activeConns.Add(-1)
}

// recordDestroyIdle records the destruction of a connection that was idle.
func (c *poolMetricsCollector) recordDestroyIdle() {
	c.destroyedConns.Add(1)
	c.totalConns.Add(-1)
	c.idleConns.Add(-1)
}

func (c *poolMetricsCollector) recordAcquireError() {
	c.acquireErrors.Add(1)
}

func (c *poolMetricsCollector) recordAcquireFromIdle() {
	c.idleConns.Add(-1)
	c.activeConns.Add(1)
}

func (c *poolMetricsCollector) recordActivate() {
	c.activeConns.Add(1)
}

func (c *poolMetricsCollector) recordRelease() {
	c.idleConns.Add(1)
	c.activeConns.Add(-1)
}

func (c *poolMetricsCollector) snapshot() PoolMetrics {
	return PoolMetrics{
		AcquireCount:      c.acquireCount.Load(),
		AcquireWaitCount:  c.acquireWaitCount.Load(),
		CreatedConns:      c.createdConns.Load(),
		DestroyedConns:    c.destroyedConns.Load(),
		AcquireErrors:     c.acquireErrors.Load(),
		AcquireWaitTimeNs: c.acquireWaitTimeNs.Load(),
		TotalConns:        c.totalConns.Load(),
		IdleConns:         c.idleConns.Load(),
		ActiveConns:       c.activeConns.Load(),
	}
}
