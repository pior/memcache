package memcache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/pior/memcache"
	"github.com/sony/gobreaker/v2"
)

// Example demonstrating how to use circuit breakers with the memcache client
func ExampleNewGobreakerConfig() {
	servers := memcache.NewStaticServers("localhost:11211", "localhost:11212")

	// Create client with circuit breakers for each server
	client, err := memcache.NewClient(servers, memcache.Config{
		MaxSize: 10,
		NewCircuitBreaker: memcache.NewGobreakerConfig(
			3,              // maxRequests in half-open state
			time.Minute,    // interval to reset failure counts
			10*time.Second, // timeout before transitioning to half-open
		),
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Perform operations - circuit breaker protects against failing servers
	_ = client.Set(ctx, memcache.Item{Key: "user:123", Value: []byte("John")})

	// Check circuit breaker states
	stats := client.AllPoolStats()
	for _, serverStats := range stats {
		fmt.Printf("Server: %s, Circuit: %s\n", serverStats.Addr, serverStats.CircuitBreakerState)
	}
}

// Example demonstrating custom circuit breaker configuration
func ExampleConfig_NewCircuitBreaker() {
	servers := memcache.NewStaticServers("localhost:11211")

	// Custom circuit breaker with specific settings
	client, err := memcache.NewClient(servers, memcache.Config{
		MaxSize: 10,
		NewCircuitBreaker: func(serverAddr string) memcache.CircuitBreaker {
			settings := gobreaker.Settings{
				Name:        serverAddr,
				MaxRequests: 5,
				Interval:    30 * time.Second,
				Timeout:     5 * time.Second,
				ReadyToTrip: func(counts gobreaker.Counts) bool {
					// Open circuit if failure rate exceeds 50% with at least 5 requests
					failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
					return counts.Requests >= 5 && failureRatio >= 0.5
				},
				OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
					fmt.Printf("Circuit breaker %s: %s -> %s\n", name, from, to)
				},
			}
			return memcache.NewGoBreaker(settings)
		},
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctx := context.Background()
	_ = client.Set(ctx, memcache.Item{Key: "key", Value: []byte("value")})
}

// Example demonstrating monitoring circuit breaker states
func ExampleServerPoolStats() {
	servers := memcache.NewStaticServers("localhost:11211", "localhost:11212", "localhost:11213")

	client, err := memcache.NewClient(servers, memcache.Config{
		MaxSize:           10,
		NewCircuitBreaker: memcache.NewGobreakerConfig(3, time.Minute, 10*time.Second),
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Perform some operations
	_ = client.Set(ctx, memcache.Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, memcache.Item{Key: "key2", Value: []byte("value2")})

	// Monitor circuit breaker states for all servers
	stats := client.AllPoolStats()
	for _, serverStats := range stats {
		fmt.Printf("Server: %s\n", serverStats.Addr)
		fmt.Printf("  Circuit Breaker: %s\n", serverStats.CircuitBreakerState)
		fmt.Printf("  Total Connections: %d\n", serverStats.PoolStats.TotalConns)
		fmt.Printf("  Active Connections: %d\n", serverStats.PoolStats.ActiveConns)
	}
}
