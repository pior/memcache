package memcache

import "time"

// ConnPoolMetrics is a point-in-time snapshot of a connection pool's
// statistics, in the shape of database/sql's DBStats: monotonic lifetime
// counters plus current-state gauges.
//
// For Prometheus integration, expose these as:
//   - Gauges: TotalConns, IdleConns, ActiveConns
//   - Counters: AcquireCount, AcquireWaitCount, CreatedConns, DestroyedConns,
//     AcquireErrors, AcquireWaitDuration
type ConnPoolMetrics struct {
	// Lifetime counters
	AcquireCount        int64         // Total acquire attempts
	AcquireWaitCount    int64         // Acquires that had to wait
	AcquireWaitDuration time.Duration // Total time spent waiting to acquire
	CreatedConns        int64         // Total connections created
	DestroyedConns      int64         // Total connections destroyed
	AcquireErrors       int64         // Failed acquire attempts

	// Current state gauges
	TotalConns  int // Total connections in pool (active + idle)
	IdleConns   int // Idle connections available
	ActiveConns int // Connections currently in use
}
