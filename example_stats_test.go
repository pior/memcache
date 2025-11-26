package memcache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/pior/memcache"
)

// Example demonstrating how to collect and use stats for CLI tools
func ExampleClient_Stats() {
	servers := memcache.NewStaticServers("localhost:11211")
	client, err := memcache.NewClient(servers, memcache.Config{
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
	fmt.Printf("  Get Hits: %d\n", stats.GetHits)
	if stats.Gets > 0 {
		fmt.Printf("  Hit Rate: %.2f%%\n", float64(stats.GetHits)/float64(stats.Gets)*100)
	}
	fmt.Printf("\n")
	fmt.Printf("Errors:\n")
	fmt.Printf("  Total Errors: %d\n", stats.Errors)
}

// Example demonstrating how to collect pool stats
func ExampleClient_AllPoolStats() {
	servers := memcache.NewStaticServers("localhost:11211")
	client, err := memcache.NewClient(servers, memcache.Config{
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

	// Get pool stats for all servers
	allPoolStats := client.AllPoolStats()

	for _, serverStats := range allPoolStats {
		poolStats := serverStats.PoolStats
		fmt.Printf("Server: %s\n", serverStats.Addr)
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
		if poolStats.AcquireWaitCount > 0 {
			avgWait := time.Duration(poolStats.AcquireWaitTimeNs / poolStats.AcquireWaitCount)
			fmt.Printf("  Average Wait Time: %v\n", avgWait)
		}
		fmt.Printf("  Acquire Errors: %d\n", poolStats.AcquireErrors)
	}
}
