package memcache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/pior/memcache"
	"github.com/sony/gobreaker/v2"
)

// Example demonstrating how to use circuit breakers with the memcache client
func ExampleNewClient() {
	servers := memcache.StaticServers("localhost:11211", "localhost:11212")

	// Create client with circuit breakers for each server
	client := memcache.NewClient(servers, memcache.Config{
		MaxSize: 10,
		CircuitBreakerSettings: &gobreaker.Settings{
			Name:        "",               // Name will be set to server address
			MaxRequests: 3,                // maxRequests in half-open state
			Interval:    time.Minute,      // interval to reset failure counts
			Timeout:     10 * time.Second, // timeout before transitioning to half-open
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 10 && failureRatio >= 0.6
			},
			OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
				fmt.Printf("Circuit breaker %s: %s -> %s\n", name, from, to)
			},
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Perform operations - circuit breaker protects against failing servers
	_ = client.Set(ctx, memcache.Item{Key: "user:123", Value: []byte("John")})

	// Check circuit breaker states
	stats := client.AllPoolStats()
	for _, serverStats := range stats {
		fmt.Printf("Server: %s\n", serverStats.Addr)
		fmt.Printf("  Circuit Breaker: %s\n", serverStats.CircuitBreakerState)
		fmt.Printf("  Total Connections: %d\n", serverStats.PoolStats.TotalConns)
		fmt.Printf("  Active Connections: %d\n", serverStats.PoolStats.ActiveConns)
	}
}
