package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/pior/memcache"
	"github.com/pior/memcache/tests/internal/promexporter"
	"github.com/pior/memcache/tests/testutils"
	"github.com/pior/memcache/tests/workload"
	"github.com/sony/gobreaker/v2"
)

func main() {
	// Flags
	concurrency := flag.Int("concurrency", 100, "Number of concurrent workers")
	poolSize := flag.Int("pool", 100, "Max connection pool size")
	workloadName := flag.String("workload", "mixed", "Workload pattern to use")
	metricsPort := flag.String("metrics-port", ":9090", "Port for Prometheus metrics")
	maxProcs := flag.Int("max-procs", 4, "Maximum number of CPU cores to use (GOMAXPROCS)")
	hotKeys := flag.Int("hot-keys", 10, "Number of hot keys for workload (default 10)")

	flag.Parse()

	// Configure workload
	workload.SetHotKeyCount(*hotKeys)

	// Limit CPU cores
	runtime.GOMAXPROCS(*maxProcs)

	log.Printf("Starting memcache load generator")
	log.Printf("  Max CPU cores: %d", *maxProcs)
	log.Printf("  Concurrency: %d", *concurrency)
	log.Printf("  Connection pool size: %d", *poolSize)
	log.Printf("  Workload: %s", *workloadName)
	log.Printf("  Hot keys: %d", *hotKeys)
	log.Printf("  Metrics: http://localhost%s/metrics", *metricsPort)

	// Setup Prometheus exporter
	exporter := promexporter.NewExporter()
	go func() {
		log.Printf("Starting metrics server on %s", *metricsPort)
		if err := exporter.ServeHTTP(*metricsPort); err != nil {
			log.Fatalf("Metrics server error: %v", err)
		}
	}()

	// Setup memcache client

	config := memcache.Config{
		MaxSize:             int32(*poolSize),
		MaxConnLifetime:     1 * time.Minute,
		MaxConnIdleTime:     30 * time.Second,
		HealthCheckInterval: 1 * time.Second,
		CircuitBreakerSettings: &gobreaker.Settings{
			MaxRequests:  3,
			Interval:     30 * time.Second,
			Timeout:      5 * time.Second,
			BucketPeriod: 10 * time.Second,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				if counts.ConsecutiveFailures >= 100 {
					return true
				}
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 10 && failureRatio >= 0.3
			},
			OnStateChange: func(name string, from, to gobreaker.State) {
				log.Printf("Circuit breaker %s: %s -> %s", name, from.String(), to.String())
				exporter.ClientMetrics().RecordCircuitBreakerTransition(name, from.String(), to.String())

				// Update state gauge
				stateValue := circuitStateToInt(to)
				exporter.ClientMetrics().SetCircuitBreakerState(name, stateValue)
			},
		},
	}

	servers := memcache.NewStaticServers(
		"10.0.0.234:11211",
		// "10.0.0.234:11212",
		// "10.0.0.234:11213",
	)
	client, err := memcache.NewClient(servers, config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	fmt.Printf("[Setup] Created memcache client with %d servers\n", len(servers.List()))

	// Wait for client to be healthy
	ctx := context.Background()
	log.Printf("Waiting for memcache servers to be healthy...")
	if err := testutils.WaitForHealthy(ctx, client); err != nil {
		log.Fatalf("Health check failed: %v", err)
	}
	log.Printf("All servers healthy")

	// Get workload
	wl, err := workload.Get(*workloadName)
	if err != nil {
		log.Fatalf("Failed to load workload: %v", err)
	}
	log.Printf("Workload: %s - %s", wl.Name(), wl.Description())

	// Create workload runner
	runner := workload.NewRunner(client, wl, *concurrency)

	// Start metrics collection goroutine
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go metricsCollectionLoop(ctx, runner, client, exporter.ClientMetrics())

	// Start workload
	log.Printf("Starting workload with %d workers", *concurrency)
	go func() {
		if err := runner.Run(ctx); err != nil {
			log.Printf("Workload error: %v", err)
		}
	}()

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Printf("Shutting down...")
	cancel()
	time.Sleep(500 * time.Millisecond)
	log.Printf("Shutdown complete")
}

func metricsCollectionLoop(ctx context.Context, runner *workload.Runner, client *memcache.Client, metrics *promexporter.ClientMetrics) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastTotal int64
	var lastCreated map[string]uint64 = make(map[string]uint64)
	var lastTime = time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Collect workload stats
			stats := runner.Stats()

			// Calculate rate
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			if elapsed > 0 {
				opsThisPeriod := stats.TotalOps - lastTotal
				rate := float64(opsThisPeriod) / elapsed
				metrics.SetOperationRate(rate)
			}

			metrics.SetErrorRate(stats.ErrorRate)
			lastTotal = stats.TotalOps
			lastTime = now

			// Collect pool and circuit breaker stats from all servers
			allStats := client.AllPoolStats()
			for _, serverStats := range allStats {
				server := serverStats.Addr

				// Pool metrics
				metrics.SetPoolConnections(
					server,
					int(serverStats.PoolStats.TotalConns),
					int(serverStats.PoolStats.ActiveConns),
					int(serverStats.PoolStats.IdleConns),
				)

				last := lastCreated[server]
				lastCreated[server] = serverStats.PoolStats.CreatedConns
				metrics.SetPoolCreated(server, serverStats.PoolStats.CreatedConns-last)
				// log.Printf("Server %s: Created connections delta: %d", server, serverStats.PoolStats.CreatedConns-last)

				metrics.SetPoolErrors(server, serverStats.PoolStats.AcquireErrors)

				// Circuit breaker metrics
				stateValue := circuitStateToInt(serverStats.CircuitBreakerState)
				metrics.SetCircuitBreakerState(server, stateValue)
				metrics.SetCircuitBreakerRequests(server, int(serverStats.CircuitBreakerCounts.Requests))
				metrics.SetCircuitBreakerFailures(
					server,
					int(serverStats.CircuitBreakerCounts.TotalFailures),
					int(serverStats.CircuitBreakerCounts.ConsecutiveFailures),
				)
			}
		}
	}
}

func circuitStateToInt(state fmt.Stringer) int {
	switch state.String() {
	case "closed":
		return 0
	case "half-open":
		return 1
	case "open":
		return 2
	default:
		return -1
	}
}
