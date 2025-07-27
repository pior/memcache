package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache"
)

type OperationType string

const (
	CacheHit     OperationType = "cache-hit"
	DynamicValue OperationType = "dynamic-value"
	CacheMiss    OperationType = "cache-miss"
	Increment    OperationType = "increment"
	Delete       OperationType = "delete"
	All          OperationType = "all"
)

type BenchmarkResult struct {
	Operation    OperationType
	Duration     time.Duration
	TotalOps     int64
	Successes    int64
	Failures     int64
	AvgLatency   time.Duration
	OpsPerSecond float64
	Correctness  bool
	ErrorMessage string
}

func main() {
	var (
		operation   = flag.String("operation", "all", "Operation type: cache-hit, dynamic-value, cache-miss, increment, delete, or all")
		duration    = flag.Duration("duration", 5*time.Second, "Duration to run benchmarks")
		concurrency = flag.Int("concurrency", 1, "Number of concurrent workers")
		servers     = flag.String("servers", "localhost:11211", "Comma-separated list of memcache servers")
	)
	flag.Parse()

	fmt.Printf("Memcache Benchmark Tool\n")
	fmt.Printf("=======================\n")
	fmt.Printf("Operation: %s\n", *operation)
	fmt.Printf("Duration: %v\n", *duration)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Servers: %s\n", *servers)
	fmt.Println()

	// Create client
	config := &memcache.ClientConfig{
		Servers: []string{*servers}, // Note: simplified for demo
		PoolConfig: &memcache.PoolConfig{
			MinConnections: 2,
			MaxConnections: 20,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    5 * time.Minute,
		},
		HashRing: &memcache.HashRingConfig{
			VirtualNodes: 160,
		},
	}

	client, err := memcache.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test connection first
	fmt.Print("Testing connection...")
	ctx := context.Background()
	testCmd := memcache.NewGetCommand("test-connection-key")
	_, err = client.Do(ctx, testCmd)
	if err != nil {
		fmt.Printf(" failed: %v\n", err)
		fmt.Printf("Make sure memcached is running on %s\n", *servers)
		fmt.Printf("You can start it with: docker-compose up -d\n")
		return
	}
	fmt.Println(" success!")
	fmt.Println()

	// Run benchmarks
	if OperationType(*operation) == All {
		runAllOperations(client, *duration, *concurrency)
	} else {
		result := runSingleOperation(client, OperationType(*operation), *duration, *concurrency)
		printResult(result)
	}
}

func runAllOperations(client *memcache.Client, duration time.Duration, concurrency int) {
	operations := []OperationType{CacheHit, DynamicValue, CacheMiss, Increment, Delete}

	for _, op := range operations {
		fmt.Printf("\n--- Running %s benchmark ---\n", op)
		result := runSingleOperation(client, op, duration, concurrency)
		printResult(result)

		// Short pause between operations
		time.Sleep(500 * time.Millisecond)
	}
}

func runSingleOperation(client *memcache.Client, operation OperationType, duration time.Duration, concurrency int) *BenchmarkResult {
	switch operation {
	case CacheHit:
		return runCacheHitBenchmark(client, duration, concurrency)
	case DynamicValue:
		return runDynamicValueBenchmark(client, duration, concurrency)
	case CacheMiss:
		return runCacheMissBenchmark(client, duration, concurrency)
	case Increment:
		return runIncrementBenchmark(client, duration, concurrency)
	case Delete:
		return runDeleteBenchmark(client, duration, concurrency)
	default:
		return &BenchmarkResult{
			Operation:    operation,
			Correctness:  false,
			ErrorMessage: fmt.Sprintf("Unknown operation: %s", operation),
		}
	}
}

// Cache-hit: 1 set then 100 get
func runCacheHitBenchmark(client *memcache.Client, duration time.Duration, concurrency int) *BenchmarkResult {
	ctx := context.Background()
	key := "cache-hit-key"
	value := []byte("cache-hit-value")

	fmt.Printf("Setting up initial value for cache-hit test...\n")

	// Set the initial value
	setCmd := memcache.NewSetCommand(key, value, time.Hour)
	_, err := client.Do(ctx, setCmd)
	if err != nil {
		return &BenchmarkResult{
			Operation:    CacheHit,
			Correctness:  false,
			ErrorMessage: fmt.Sprintf("Failed to set initial value: %v", err),
		}
	}

	fmt.Printf("Starting cache-hit benchmark with %d workers for %v...\n", concurrency, duration)

	result := &BenchmarkResult{Operation: CacheHit, Correctness: true}
	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			batchCount := 0
			for time.Since(startTime) < duration {
				// Perform 100 gets for every cache-hit test
				for j := 0; j < 100; j++ {
					opStart := time.Now()
					getCmd := memcache.NewGetCommand(key)
					responses, err := client.Do(ctx, getCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))

					if err != nil || len(responses) == 0 || responses[0].Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
						// Verify correctness
						if string(responses[0].Value) != string(value) {
							result.Correctness = false
							result.ErrorMessage = "Value mismatch"
						}
					}
				}
				batchCount++
				if batchCount%10 == 0 {
					fmt.Printf("Worker %d completed %d batches (total ops: %d)\n", workerID, batchCount, atomic.LoadInt64(&totalOps))
				}
				// Small delay to prevent overwhelming the server
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	fmt.Printf("Cache-hit benchmark completed.\n")

	result.Duration = time.Since(startTime)
	result.TotalOps = totalOps
	result.Successes = successes
	result.Failures = failures

	if totalOps > 0 {
		result.AvgLatency = time.Duration(totalLatency / totalOps)
		result.OpsPerSecond = float64(totalOps) / result.Duration.Seconds()
	}

	return result
}

// Dynamic-value: 1 set then 1 get
func runDynamicValueBenchmark(client *memcache.Client, duration time.Duration, concurrency int) *BenchmarkResult {
	ctx := context.Background()

	result := &BenchmarkResult{Operation: DynamicValue, Correctness: true}
	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < duration {
				key := fmt.Sprintf("dynamic-key-%d-%d", workerID, opCount)
				value := []byte(fmt.Sprintf("dynamic-value-%d-%d", workerID, opCount))

				// Set
				opStart := time.Now()
				setCmd := memcache.NewSetCommand(key, value, time.Hour)
				_, err := client.Do(ctx, setCmd)
				setLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(setLatency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				atomic.AddInt64(&successes, 1)

				// Get
				opStart = time.Now()
				getCmd := memcache.NewGetCommand(key)
				responses, err := client.Do(ctx, getCmd)
				getLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(getLatency))

				if err != nil || len(responses) == 0 || responses[0].Error != nil {
					atomic.AddInt64(&failures, 1)
				} else {
					atomic.AddInt64(&successes, 1)
					// Verify correctness
					if string(responses[0].Value) != string(value) {
						result.Correctness = false
						result.ErrorMessage = "Value mismatch"
					}
				}

				opCount++
			}
		}(i)
	}

	wg.Wait()

	result.Duration = time.Since(startTime)
	result.TotalOps = totalOps
	result.Successes = successes
	result.Failures = failures

	if totalOps > 0 {
		result.AvgLatency = time.Duration(totalLatency / totalOps)
		result.OpsPerSecond = float64(totalOps) / result.Duration.Seconds()
	}

	return result
}

// Cache-miss: 1 get (on inexistent key)
func runCacheMissBenchmark(client *memcache.Client, duration time.Duration, concurrency int) *BenchmarkResult {
	ctx := context.Background()

	result := &BenchmarkResult{Operation: CacheMiss, Correctness: true}
	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < duration {
				key := fmt.Sprintf("nonexistent-key-%d-%d", workerID, opCount)

				opStart := time.Now()
				getCmd := memcache.NewGetCommand(key)
				responses, err := client.Do(ctx, getCmd)
				latency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(latency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
				} else if len(responses) > 0 && responses[0].Error == memcache.ErrCacheMiss {
					atomic.AddInt64(&successes, 1)
				} else {
					atomic.AddInt64(&failures, 1)
					result.Correctness = false
					result.ErrorMessage = "Expected cache miss but got value"
				}

				opCount++
			}
		}(i)
	}

	wg.Wait()

	result.Duration = time.Since(startTime)
	result.TotalOps = totalOps
	result.Successes = successes
	result.Failures = failures

	if totalOps > 0 {
		result.AvgLatency = time.Duration(totalLatency / totalOps)
		result.OpsPerSecond = float64(totalOps) / result.Duration.Seconds()
	}

	return result
}

// Increment: 100 incr then 1 get (to check the value)
func runIncrementBenchmark(client *memcache.Client, duration time.Duration, concurrency int) *BenchmarkResult {
	ctx := context.Background()
	key := "increment-key"

	// Initialize counter to 0
	setCmd := memcache.NewSetCommand(key, []byte("0"), time.Hour)
	_, err := client.Do(ctx, setCmd)
	if err != nil {
		return &BenchmarkResult{
			Operation:    Increment,
			Correctness:  false,
			ErrorMessage: fmt.Sprintf("Failed to initialize counter: %v", err),
		}
	}

	result := &BenchmarkResult{Operation: Increment, Correctness: true}
	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for time.Since(startTime) < duration {
				// Perform 100 increments
				for j := 0; j < 100; j++ {
					opStart := time.Now()
					// Note: Using a generic command for increment as it may not be directly supported
					// This is a simplified approach - in a real implementation you'd use the meta protocol increment
					incrCmd := &memcache.Command{
						Type:  "ma", // meta arithmetic
						Key:   key,
						Flags: map[string]string{"D": "1"}, // Delta of 1
					}
					_, err := client.Do(ctx, incrCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))

					if err != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
					}
				}

				// Get to verify the value
				opStart := time.Now()
				getCmd := memcache.NewGetCommand(key)
				responses, err := client.Do(ctx, getCmd)
				latency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(latency))

				if err != nil || len(responses) == 0 || responses[0].Error != nil {
					atomic.AddInt64(&failures, 1)
				} else {
					atomic.AddInt64(&successes, 1)
					// Verify it's a number
					if _, err := strconv.Atoi(string(responses[0].Value)); err != nil {
						result.Correctness = false
						result.ErrorMessage = "Counter value is not a number"
					}
				}
			}
		}()
	}

	wg.Wait()

	result.Duration = time.Since(startTime)
	result.TotalOps = totalOps
	result.Successes = successes
	result.Failures = failures

	if totalOps > 0 {
		result.AvgLatency = time.Duration(totalLatency / totalOps)
		result.OpsPerSecond = float64(totalOps) / result.Duration.Seconds()
	}

	return result
}

// Delete: 1 set then 1 delete
func runDeleteBenchmark(client *memcache.Client, duration time.Duration, concurrency int) *BenchmarkResult {
	ctx := context.Background()

	result := &BenchmarkResult{Operation: Delete, Correctness: true}
	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < duration {
				key := fmt.Sprintf("delete-key-%d-%d", workerID, opCount)
				value := []byte(fmt.Sprintf("delete-value-%d-%d", workerID, opCount))

				// Set
				opStart := time.Now()
				setCmd := memcache.NewSetCommand(key, value, time.Hour)
				_, err := client.Do(ctx, setCmd)
				setLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(setLatency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				atomic.AddInt64(&successes, 1)

				// Delete
				opStart = time.Now()
				delCmd := memcache.NewDeleteCommand(key)
				responses, err := client.Do(ctx, delCmd)
				delLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(delLatency))

				if err != nil || (len(responses) > 0 && responses[0].Error != nil && responses[0].Error != memcache.ErrCacheMiss) {
					atomic.AddInt64(&failures, 1)
				} else {
					atomic.AddInt64(&successes, 1)
				}

				opCount++
			}
		}(i)
	}

	wg.Wait()

	result.Duration = time.Since(startTime)
	result.TotalOps = totalOps
	result.Successes = successes
	result.Failures = failures

	if totalOps > 0 {
		result.AvgLatency = time.Duration(totalLatency / totalOps)
		result.OpsPerSecond = float64(totalOps) / result.Duration.Seconds()
	}

	return result
}

func printResult(result *BenchmarkResult) {
	fmt.Printf("Operation: %s\n", result.Operation)
	fmt.Printf("Duration: %v\n", result.Duration)
	fmt.Printf("Total Operations: %d\n", result.TotalOps)
	fmt.Printf("Successes: %d\n", result.Successes)
	fmt.Printf("Failures: %d\n", result.Failures)
	if result.TotalOps > 0 {
		fmt.Printf("Success Rate: %.2f%%\n", float64(result.Successes)/float64(result.TotalOps)*100)
		fmt.Printf("Ops/sec: %.2f\n", result.OpsPerSecond)
		fmt.Printf("Avg Latency: %v\n", result.AvgLatency)
	}
	fmt.Printf("Correctness: %t\n", result.Correctness)
	if result.ErrorMessage != "" {
		fmt.Printf("Error: %s\n", result.ErrorMessage)
	}
	fmt.Println()
}
