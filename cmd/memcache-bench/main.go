package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/pior/memcache"
)

func main() {
	var (
		numRequests = flag.Int("requests", 1000, "Number of requests to send")
		concurrency = flag.Int("concurrency", 10, "Number of concurrent workers")
		keySize     = flag.Int("key-size", 10, "Size of keys in bytes")
		valueSize   = flag.Int("value-size", 100, "Size of values in bytes")
		servers     = flag.String("servers", "localhost:11211", "Comma-separated list of memcache servers")
		operation   = flag.String("operation", "mixed", "Operation type: get, set, delete, or mixed")
		ttl         = flag.Int("ttl", 300, "TTL for set operations in seconds")
	)
	flag.Parse()

	fmt.Printf("Memcache Benchmark Tool\n")
	fmt.Printf("=======================\n")
	fmt.Printf("Requests: %d\n", *numRequests)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Key Size: %d bytes\n", *keySize)
	fmt.Printf("Value Size: %d bytes\n", *valueSize)
	fmt.Printf("Servers: %s\n", *servers)
	fmt.Printf("Operation: %s\n", *operation)
	fmt.Printf("TTL: %d seconds\n", *ttl)
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

	// Pre-populate some keys for get operations
	if *operation == "get" || *operation == "mixed" {
		fmt.Print("Pre-populating keys...")
		populateKeys(client, *numRequests, *keySize, *valueSize, *ttl)
		fmt.Println(" done")
	}

	// Run benchmark
	results := runBenchmark(client, *numRequests, *concurrency, *keySize, *valueSize, *operation, *ttl)

	// Print results
	printResults(results)
}

type BenchmarkResult struct {
	TotalRequests  int
	SuccessfulReqs int
	FailedRequests int
	TotalDuration  time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	AvgLatency     time.Duration
	RequestsPerSec float64
	Latencies      []time.Duration
}

func runBenchmark(client *memcache.Client, numRequests, concurrency, keySize, valueSize int, operation string, ttl int) *BenchmarkResult {
	var wg sync.WaitGroup
	var mu sync.Mutex

	results := &BenchmarkResult{
		TotalRequests: numRequests,
		MinLatency:    time.Hour, // Start with a high value
		Latencies:     make([]time.Duration, 0, numRequests),
	}

	requestsChan := make(chan int, numRequests)
	for i := 0; i < numRequests; i++ {
		requestsChan <- i
	}
	close(requestsChan)

	start := time.Now()

	// Start workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for reqNum := range requestsChan {
				latency := performOperation(client, reqNum, keySize, valueSize, operation, ttl)

				mu.Lock()
				results.Latencies = append(results.Latencies, latency)
				if latency > 0 {
					results.SuccessfulReqs++
					if latency < results.MinLatency {
						results.MinLatency = latency
					}
					if latency > results.MaxLatency {
						results.MaxLatency = latency
					}
				} else {
					results.FailedRequests++
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	results.TotalDuration = time.Since(start)

	// Calculate statistics
	if results.SuccessfulReqs > 0 {
		var totalLatency time.Duration
		for _, latency := range results.Latencies {
			if latency > 0 {
				totalLatency += latency
			}
		}
		results.AvgLatency = totalLatency / time.Duration(results.SuccessfulReqs)
		results.RequestsPerSec = float64(results.SuccessfulReqs) / results.TotalDuration.Seconds()
	}

	return results
}

func performOperation(client *memcache.Client, reqNum, keySize, valueSize int, operation string, ttl int) time.Duration {
	ctx := context.Background()
	key := generateKey(reqNum, keySize)

	start := time.Now()

	switch operation {
	case "get":
		cmd := memcache.NewGetCommand(key)
		responses, err := client.Do(ctx, cmd)
		if err != nil {
			return 0 // Indicate failure
		}
		if len(responses) > 0 && responses[0].Error != nil && responses[0].Error != memcache.ErrCacheMiss {
			return 0 // Indicate failure
		}

	case "set":
		value := generateValue(valueSize)
		cmd := memcache.NewSetCommand(key, value, time.Duration(ttl)*time.Second)
		responses, err := client.Do(ctx, cmd)
		if err != nil {
			return 0 // Indicate failure
		}
		if len(responses) > 0 && responses[0].Error != nil {
			return 0 // Indicate failure
		}

	case "delete":
		cmd := memcache.NewDeleteCommand(key)
		responses, err := client.Do(ctx, cmd)
		if err != nil {
			return 0 // Indicate failure
		}
		if len(responses) > 0 && responses[0].Error != nil && responses[0].Error != memcache.ErrCacheMiss {
			return 0 // Indicate failure
		}

	case "mixed":
		// Random operation: 50% get, 30% set, 20% delete
		rand := rand.Intn(100)
		if rand < 50 {
			cmd := memcache.NewGetCommand(key)
			responses, err := client.Do(ctx, cmd)
			if err != nil {
				return 0
			}
			if len(responses) > 0 && responses[0].Error != nil && responses[0].Error != memcache.ErrCacheMiss {
				return 0
			}
		} else if rand < 80 {
			value := generateValue(valueSize)
			cmd := memcache.NewSetCommand(key, value, time.Duration(ttl)*time.Second)
			responses, err := client.Do(ctx, cmd)
			if err != nil {
				return 0
			}
			if len(responses) > 0 && responses[0].Error != nil {
				return 0
			}
		} else {
			cmd := memcache.NewDeleteCommand(key)
			responses, err := client.Do(ctx, cmd)
			if err != nil {
				return 0
			}
			if len(responses) > 0 && responses[0].Error != nil && responses[0].Error != memcache.ErrCacheMiss {
				return 0
			}
		}
	}

	return time.Since(start)
}

func populateKeys(client *memcache.Client, numKeys, keySize, valueSize, ttl int) {
	ctx := context.Background()

	for i := 0; i < numKeys; i++ {
		key := generateKey(i, keySize)
		value := generateValue(valueSize)

		cmd := memcache.NewSetCommand(key, value, time.Duration(ttl)*time.Second)
		client.Do(ctx, cmd)
	}
}

func generateKey(reqNum, size int) string {
	base := fmt.Sprintf("key_%d", reqNum)
	if len(base) >= size {
		return base[:size]
	}

	padding := size - len(base)
	for i := 0; i < padding; i++ {
		base += "x"
	}
	return base
}

func generateValue(size int) []byte {
	value := make([]byte, size)
	for i := 0; i < size; i++ {
		value[i] = byte('a' + (i % 26))
	}
	return value
}

func printResults(results *BenchmarkResult) {
	fmt.Printf("\nBenchmark Results\n")
	fmt.Printf("=================\n")
	fmt.Printf("Total Requests:     %d\n", results.TotalRequests)
	fmt.Printf("Successful:         %d\n", results.SuccessfulReqs)
	fmt.Printf("Failed:             %d\n", results.FailedRequests)
	fmt.Printf("Success Rate:       %.2f%%\n", float64(results.SuccessfulReqs)/float64(results.TotalRequests)*100)
	fmt.Printf("Total Duration:     %v\n", results.TotalDuration)
	fmt.Printf("Requests/sec:       %.2f\n", results.RequestsPerSec)

	if results.SuccessfulReqs > 0 {
		fmt.Printf("Min Latency:        %v\n", results.MinLatency)
		fmt.Printf("Max Latency:        %v\n", results.MaxLatency)
		fmt.Printf("Avg Latency:        %v\n", results.AvgLatency)

		// Calculate percentiles
		percentiles := calculatePercentiles(results.Latencies)
		fmt.Printf("50th percentile:    %v\n", percentiles[50])
		fmt.Printf("95th percentile:    %v\n", percentiles[95])
		fmt.Printf("99th percentile:    %v\n", percentiles[99])
	}
}

func calculatePercentiles(latencies []time.Duration) map[int]time.Duration {
	// Filter out failed requests (latency = 0)
	validLatencies := make([]time.Duration, 0, len(latencies))
	for _, latency := range latencies {
		if latency > 0 {
			validLatencies = append(validLatencies, latency)
		}
	}

	if len(validLatencies) == 0 {
		return map[int]time.Duration{}
	}

	// Sort latencies
	for i := 0; i < len(validLatencies); i++ {
		for j := i + 1; j < len(validLatencies); j++ {
			if validLatencies[i] > validLatencies[j] {
				validLatencies[i], validLatencies[j] = validLatencies[j], validLatencies[i]
			}
		}
	}

	percentiles := map[int]time.Duration{}
	for _, p := range []int{50, 95, 99} {
		idx := int(float64(len(validLatencies)) * float64(p) / 100.0)
		if idx >= len(validLatencies) {
			idx = len(validLatencies) - 1
		}
		percentiles[p] = validLatencies[idx]
	}

	return percentiles
}
