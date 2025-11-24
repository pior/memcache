package memcache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/pior/memcache"
)

// Example demonstrating how to collect and use stats for CLI tools
func ExampleClient_Stats() {
	client, err := memcache.NewClient("localhost:11211", memcache.Config{
		MaxSize: 10,
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Perform some operations
	_ = client.Set(ctx, memcache.Item{Key: "user:123", Value: []byte("John")})
	_, _ = client.Get(ctx, "user:123")
	_, _ = client.Get(ctx, "user:456") // Cache miss

	// Get client stats
	stats := client.Stats()

	fmt.Printf("Operations:\n")
	fmt.Printf("  Gets: %d\n", stats.Gets)
	fmt.Printf("  Sets: %d\n", stats.Sets)
	fmt.Printf("  Deletes: %d\n", stats.Deletes)
	fmt.Printf("  Adds: %d\n", stats.Adds)
	fmt.Printf("  Increments: %d\n", stats.Increments)
	fmt.Printf("\n")
	fmt.Printf("Cache Performance:\n")
	fmt.Printf("  Hits: %d\n", stats.CacheHits)
	fmt.Printf("  Misses: %d\n", stats.CacheMisses)
	fmt.Printf("  Hit Rate: %.2f%%\n", stats.HitRate()*100)
	fmt.Printf("\n")
	fmt.Printf("Errors:\n")
	fmt.Printf("  Total Errors: %d\n", stats.Errors)
	fmt.Printf("  Connections Destroyed: %d\n", stats.ConnectionsDestroyed)
}

// Example demonstrating how to collect pool stats
func ExampleClient_PoolStats() {
	client, err := memcache.NewClient("localhost:11211", memcache.Config{
		MaxSize: 10,
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Perform some operations to create connections
	_ = client.Set(ctx, memcache.Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, memcache.Item{Key: "key2", Value: []byte("value2")})

	// Get pool stats
	poolStats := client.PoolStats()

	fmt.Printf("Pool Status:\n")
	fmt.Printf("  Total Connections: %d\n", poolStats.TotalConns)
	fmt.Printf("  Idle Connections: %d\n", poolStats.IdleConns)
	fmt.Printf("  Active Connections: %d\n", poolStats.ActiveConns)
	fmt.Printf("\n")
	fmt.Printf("Pool Lifetime:\n")
	fmt.Printf("  Connections Created: %d\n", poolStats.CreatedConns)
	fmt.Printf("  Connections Destroyed: %d\n", poolStats.DestroyedConns)
	fmt.Printf("  Total Acquires: %d\n", poolStats.AcquireCount)
	fmt.Printf("  Acquires That Waited: %d\n", poolStats.AcquireWaitCount)
	fmt.Printf("  Average Wait Time: %v\n", poolStats.AverageWaitTime())
	fmt.Printf("  Acquire Errors: %d\n", poolStats.AcquireErrors)
}

// Example demonstrating Prometheus-style metrics collection
func Example_prometheusMetrics() {
	client, err := memcache.NewClient("localhost:11211", memcache.Config{
		MaxSize: 10,
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Collect stats periodically (e.g., for Prometheus scraping)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Get client stats
		stats := client.Stats()

		// Expose as Prometheus counter metrics
		fmt.Printf("memcache_operations_total{operation=\"get\"} %d\n", stats.Gets)
		fmt.Printf("memcache_operations_total{operation=\"set\"} %d\n", stats.Sets)
		fmt.Printf("memcache_operations_total{operation=\"delete\"} %d\n", stats.Deletes)
		fmt.Printf("memcache_operations_total{operation=\"add\"} %d\n", stats.Adds)
		fmt.Printf("memcache_operations_total{operation=\"increment\"} %d\n", stats.Increments)

		// Cache hit/miss counters
		fmt.Printf("memcache_cache_hits_total %d\n", stats.CacheHits)
		fmt.Printf("memcache_cache_misses_total %d\n", stats.CacheMisses)

		// Error counter
		fmt.Printf("memcache_errors_total %d\n", stats.Errors)

		// Derived gauge - hit rate
		fmt.Printf("memcache_cache_hit_rate %.4f\n", stats.HitRate())

		// Get pool stats
		poolStats := client.PoolStats()

		// Expose as Prometheus gauge metrics
		fmt.Printf("memcache_pool_connections{state=\"total\"} %d\n", poolStats.TotalConns)
		fmt.Printf("memcache_pool_connections{state=\"idle\"} %d\n", poolStats.IdleConns)
		fmt.Printf("memcache_pool_connections{state=\"active\"} %d\n", poolStats.ActiveConns)

		// Pool lifetime counters
		fmt.Printf("memcache_pool_connections_created_total %d\n", poolStats.CreatedConns)
		fmt.Printf("memcache_pool_connections_destroyed_total %d\n", poolStats.DestroyedConns)
		fmt.Printf("memcache_pool_acquire_total %d\n", poolStats.AcquireCount)
		fmt.Printf("memcache_pool_acquire_wait_total %d\n", poolStats.AcquireWaitCount)
		fmt.Printf("memcache_pool_acquire_errors_total %d\n", poolStats.AcquireErrors)

		// Histogram-style metric (you would need to track buckets separately in real Prometheus integration)
		fmt.Printf("memcache_pool_acquire_wait_duration_seconds %.6f\n", poolStats.AverageWaitTime().Seconds())

		break // Only run once for example
	}
}
