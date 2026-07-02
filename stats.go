package memcache

// ConnPoolMetrics is a point-in-time snapshot of a connection pool's statistics.
//
// For Prometheus integration, expose these as:
//   - Gauges: TotalConns, IdleConns, ActiveConns
//   - Counters: AcquireCount, AcquireWaitCount, CreatedConns, DestroyedConns, AcquireErrors
//   - Histogram: AcquireWaitDuration (use AcquireWaitCount and AcquireWaitTimeNs to calculate)
type ConnPoolMetrics struct {
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
