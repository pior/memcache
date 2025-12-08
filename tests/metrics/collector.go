package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pior/memcache"
	"github.com/pior/memcache/tests/workload"
)

// Collector periodically collects metrics from the memcache client and workload
type Collector struct {
	client          *memcache.Client
	workloadRunner  *workload.Runner
	interval        time.Duration
	snapshots       []Snapshot
	mu              sync.Mutex
	circuitChanges  []CircuitBreakerChange
	lastCircuitState map[string]string
}

// Snapshot represents metrics at a point in time
type Snapshot struct {
	Timestamp    time.Time
	WorkloadStats workload.WorkloadStats
	PoolStats    []PoolSnapshot
}

// PoolSnapshot represents pool metrics for a single server
type PoolSnapshot struct {
	ServerAddr          string
	TotalConns          int32
	IdleConns           int32
	ActiveConns         int32
	CreatedConns        uint64
	AcquireErrors       uint64
	CircuitBreakerState string
	Requests            uint32
	TotalFailures       uint32
	ConsecutiveFailures uint32
}

// CircuitBreakerChange records when a circuit breaker changes state
type CircuitBreakerChange struct {
	Timestamp  time.Time
	ServerAddr string
	OldState   string
	NewState   string
}

// NewCollector creates a metrics collector
func NewCollector(client *memcache.Client, runner *workload.Runner, interval time.Duration) *Collector {
	return &Collector{
		client:           client,
		workloadRunner:   runner,
		interval:         interval,
		snapshots:        make([]Snapshot, 0),
		circuitChanges:   make([]CircuitBreakerChange, 0),
		lastCircuitState: make(map[string]string),
	}
}

// Start begins collecting metrics periodically
func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

func (c *Collector) collect() {
	snapshot := Snapshot{
		Timestamp:     time.Now(),
		WorkloadStats: c.workloadRunner.Stats(),
		PoolStats:     make([]PoolSnapshot, 0),
	}

	// Collect pool stats from all servers
	allStats := c.client.AllPoolStats()
	for _, serverStats := range allStats {
		poolSnap := PoolSnapshot{
			ServerAddr:          serverStats.Addr,
			TotalConns:          serverStats.PoolStats.TotalConns,
			IdleConns:           serverStats.PoolStats.IdleConns,
			ActiveConns:         serverStats.PoolStats.ActiveConns,
			CreatedConns:        serverStats.PoolStats.CreatedConns,
			AcquireErrors:       serverStats.PoolStats.AcquireErrors,
			CircuitBreakerState: serverStats.CircuitBreakerState.String(),
			Requests:            serverStats.CircuitBreakerCounts.Requests,
			TotalFailures:       serverStats.CircuitBreakerCounts.TotalFailures,
			ConsecutiveFailures: serverStats.CircuitBreakerCounts.ConsecutiveFailures,
		}
		snapshot.PoolStats = append(snapshot.PoolStats, poolSnap)
	}

	c.mu.Lock()
	c.snapshots = append(c.snapshots, snapshot)
	c.mu.Unlock()
}

// GetSnapshots returns all collected snapshots
func (c *Collector) GetSnapshots() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Snapshot{}, c.snapshots...)
}

// GetCircuitChanges returns all recorded circuit breaker state changes
func (c *Collector) GetCircuitChanges() []CircuitBreakerChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CircuitBreakerChange{}, c.circuitChanges...)
}

// RecordCircuitBreakerChange records a circuit breaker state change
// This is called from the OnStateChange callback
func (c *Collector) RecordCircuitBreakerChange(serverAddr, oldState, newState string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.circuitChanges = append(c.circuitChanges, CircuitBreakerChange{
		Timestamp:  time.Now(),
		ServerAddr: serverAddr,
		OldState:   oldState,
		NewState:   newState,
	})
	// Update last known state
	c.lastCircuitState[serverAddr] = newState
}

// PrintLatest prints the most recent snapshot
func (c *Collector) PrintLatest() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.snapshots) == 0 {
		fmt.Println("[Metrics] No data collected yet")
		return
	}

	snapshot := c.snapshots[len(c.snapshots)-1]
	fmt.Printf("\n[Metrics] %s\n", snapshot.Timestamp.Format("15:04:05"))
	fmt.Printf("  Workload: %s\n", snapshot.WorkloadStats.String())

	for _, pool := range snapshot.PoolStats {
		fmt.Printf("  Server %s:\n", pool.ServerAddr)
		fmt.Printf("    Connections: %d total, %d active, %d idle\n",
			pool.TotalConns, pool.ActiveConns, pool.IdleConns)
		fmt.Printf("    Circuit: %s (requests: %d, failures: %d, consecutive: %d)\n",
			pool.CircuitBreakerState, pool.Requests, pool.TotalFailures, pool.ConsecutiveFailures)
	}
}

// PrintSummary prints a summary of all collected metrics
func (c *Collector) PrintSummary() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.snapshots) == 0 {
		fmt.Println("[Summary] No data collected")
		return
	}

	firstSnap := c.snapshots[0]
	lastSnap := c.snapshots[len(c.snapshots)-1]
	duration := lastSnap.Timestamp.Sub(firstSnap.Timestamp)

	fmt.Println("\n========================================")
	fmt.Println("          TEST SUMMARY")
	fmt.Println("========================================")
	fmt.Printf("Duration: %s\n", duration.Round(time.Second))
	fmt.Printf("Snapshots: %d\n", len(c.snapshots))

	// Workload summary
	fmt.Println("\nWorkload Statistics:")
	fmt.Printf("  Total Operations: %d\n", lastSnap.WorkloadStats.TotalOps)
	fmt.Printf("  Successful: %d\n", lastSnap.WorkloadStats.SuccessOps)
	fmt.Printf("  Failed: %d\n", lastSnap.WorkloadStats.FailedOps)
	fmt.Printf("  Error Rate: %.2f%%\n", lastSnap.WorkloadStats.ErrorRate*100)

	if duration > 0 {
		opsPerSec := float64(lastSnap.WorkloadStats.TotalOps) / duration.Seconds()
		fmt.Printf("  Throughput: %.0f ops/sec\n", opsPerSec)
	}

	// Circuit breaker changes
	if len(c.circuitChanges) > 0 {
		fmt.Println("\nCircuit Breaker State Changes:")
		for _, change := range c.circuitChanges {
			fmt.Printf("  [%s] %s: %s → %s\n",
				change.Timestamp.Format("15:04:05"),
				change.ServerAddr,
				change.OldState,
				change.NewState)
		}
	} else {
		fmt.Println("\nCircuit Breaker: No state changes")
	}

	// Final pool state
	fmt.Println("\nFinal Pool States:")
	for _, pool := range lastSnap.PoolStats {
		fmt.Printf("  %s:\n", pool.ServerAddr)
		fmt.Printf("    Connections: %d total, %d active, %d idle\n",
			pool.TotalConns, pool.ActiveConns, pool.IdleConns)
		fmt.Printf("    Created: %d, Errors: %d\n",
			pool.CreatedConns, pool.AcquireErrors)
		fmt.Printf("    Circuit: %s\n", pool.CircuitBreakerState)
	}

	fmt.Println("========================================")
}
