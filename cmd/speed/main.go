package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/pior/memcache"
)

type Test struct {
	Name        string
	ItemsPerOp  int // Number of items processed per operation (1 for single ops, 10 for batch-10, etc.)
	Operation   OperationFunc
}

type OperationFunc func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error

type Result struct {
	name        string
	count       int64
	itemsPerOp  int
	duration    time.Duration
	opsPerSec   float64
	itemsPerSec float64
	avgLatency  time.Duration
}

type Config struct {
	addr        string
	bradfitz    bool
	pool        string
	concurrency int
	count       int64
	only        string
}

func main() {
	config := Config{}
	flag.StringVar(&config.addr, "addr", "127.0.0.1:11211", "memcache server address")
	flag.BoolVar(&config.bradfitz, "bradfitz", false, "use bradfitz client implementation (default is pior)")
	flag.StringVar(&config.pool, "pool", "channel", "pool implementation for pior client: channel or puddle")
	flag.IntVar(&config.concurrency, "concurrency", 1, "number of concurrent workers")
	flag.Int64Var(&config.count, "count", 1_000_000, "target operation count")
	flag.StringVar(&config.only, "only", "", "run only the specified operation (e.g., 'Set')")
	flag.Parse()

	if config.pool != "channel" && config.pool != "puddle" {
		log.Fatalf("Invalid pool: %s (must be 'channel' or 'puddle')", config.pool)
	}

	fmt.Printf("Memcache Speed Test\n")
	fmt.Printf("===================\n")
	if config.bradfitz {
		fmt.Printf("Client:      bradfitz\n")
	} else {
		fmt.Printf("Client:      pior\n")
		fmt.Printf("Pool:        %s\n", config.pool)
	}
	fmt.Printf("Server:      %s\n", config.addr)
	fmt.Printf("Concurrency: %d\n", config.concurrency)
	fmt.Printf("Target:      %s operations\n\n", formatNumber(config.count))

	client, closeFunc := createClient(config)
	defer closeFunc()

	ctx := context.Background()
	uid := rand.Int64N(1_000_000)

	// Verify server is reachable before starting benchmarks
	fmt.Printf("Verifying connection to %s...\n", config.addr)

	testKey := fmt.Sprintf("test-%d-preflight", uid)
	err := client.Set(ctx, memcache.Item{Key: testKey, Value: []byte(testKey), TTL: 1 * time.Second})
	if err != nil {
		log.Fatalf("Failed to set a test key to memcache server: %v\n", err)
	}
	item, err := client.Get(ctx, testKey)
	if err != nil {
		log.Fatalf("Failed to get the test key from memcache server: %v\n", err)
	}
	if !item.Found || string(item.Value) != testKey {
		log.Fatalf("Test key value mismatch: expected %q, got %q\n", testKey, item.Value)
	}

	fmt.Printf("Connection verified!\n\n")

	data10kb := make([]byte, 1024*10)

	var tests = []Test{
		{
			Name:       "get-miss",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "multi-get-miss-10",
			ItemsPerOp: 10,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				keys := make([]string, 10)
				for i := range 10 {
					keys[i] = fmt.Sprintf("test-%d-%d-%d-%d", uid, workerID, operationID, i)
				}
				_, err := client.MultiGet(ctx, keys)
				return err
			},
		},
		{
			Name:       "set",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Set(ctx, memcache.Item{
					Key:   key,
					Value: []byte("benchmark-value-0123456789"),
					TTL:   time.Minute,
				})
			},
		},
		{
			Name:       "multi-set-10",
			ItemsPerOp: 10,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				items := make([]memcache.Item, 10)
				for i := range 10 {
					items[i] = memcache.Item{
						Key:   fmt.Sprintf("test-%d-%d-%d-%d", uid, workerID, operationID, i),
						Value: []byte("benchmark-value-0123456789"),
						TTL:   time.Minute,
					}
				}
				return client.MultiSet(ctx, items)
			},
		},
		{
			Name:       "get-hit",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "multi-get-hit-10",
			ItemsPerOp: 10,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				keys := make([]string, 10)
				for i := range 10 {
					keys[i] = fmt.Sprintf("test-%d-%d-%d-%d", uid, workerID, operationID, i)
				}
				_, err := client.MultiGet(ctx, keys)
				return err
			},
		},
		{
			Name:       "set-10kb",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Set(ctx, memcache.Item{
					Key:   key,
					Value: data10kb,
					TTL:   time.Minute,
				})
			},
		},
		{
			Name:       "get-hit-10kb",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name:       "delete-found",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name:       "delete-miss",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name:       "increment",
			ItemsPerOp: 1,
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-counter", uid)
				_, err := client.Increment(ctx, key, 1, time.Minute)
				return err
			},
		},
	}

	var results []Result

	for _, test := range tests {
		if config.only != "" && test.Name != config.only {
			continue
		}

		fmt.Printf("Running: %s\n", test.Name)

		result := runBenchmark(ctx, client, config, uid, test)

		if test.ItemsPerOp > 1 {
			fmt.Printf("  Completed in %s (%.0f ops/sec, %.0f items/sec, %s avg latency)\n",
				formatDuration(result.duration),
				result.opsPerSec,
				result.itemsPerSec,
				formatDuration(result.avgLatency),
			)
		} else {
			fmt.Printf("  Completed in %s (%.0f ops/sec, %s avg latency)\n",
				formatDuration(result.duration),
				result.opsPerSec,
				formatDuration(result.avgLatency),
			)
		}

		results = append(results, result)
	}

	fmt.Printf("\n")
	fmt.Printf("%-20s %12s %10s %12s %12s %12s\n", "Operation", "Count", "Duration", "Ops/sec", "Items/sec", "Avg Latency")
	for _, result := range results {
		itemsPerSecStr := ""
		if result.itemsPerOp > 1 {
			itemsPerSecStr = formatNumber(int64(result.itemsPerSec))
		} else {
			itemsPerSecStr = "-"
		}
		fmt.Printf("%-20s %12s %10s %12s %12s %12s\n",
			result.name,
			formatNumber(result.count),
			formatDuration(result.duration),
			formatNumber(int64(result.opsPerSec)),
			itemsPerSecStr,
			formatDuration(result.avgLatency),
		)
	}

	// Display stats for pior client
	if !config.bradfitz {
		printPiorClientStats(client)
	}
}

// runBenchmark is a generic benchmark runner that executes an operation function
func runBenchmark(
	ctx context.Context,
	client Client,
	config Config,
	uid int64,
	test Test,
) Result {
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				if err := test.Operation(ctx, client, uid, workerID, j); err != nil {
					log.Fatalf("Operation %s failed: %v\n", test.Name, err)
				}
			}
		}(i)
	}

	wg.Wait()

	duration := time.Since(start)
	opsPerSec := float64(config.count) / duration.Seconds()
	totalItems := config.count * int64(test.ItemsPerOp)
	itemsPerSec := float64(totalItems) / duration.Seconds()

	return Result{
		name:        test.Name,
		count:       config.count,
		itemsPerOp:  test.ItemsPerOp,
		duration:    duration,
		opsPerSec:   opsPerSec,
		itemsPerSec: itemsPerSec,
		avgLatency:  duration / time.Duration(opsPerWorker),
	}
}

func formatNumber(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	} else if n >= 1_000 {
		return fmt.Sprintf("%.2fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	} else if d >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fµs", float64(d.Nanoseconds())/1000)
}

func printPiorClientStats(client Client) {
	piorCli, ok := client.(*piorClient)
	if !ok {
		return
	}

	allPoolStats := piorCli.AllPoolStats()

	fmt.Printf("\n")
	fmt.Printf("Pool Statistics\n")
	fmt.Printf("===============\n")
	for _, serverStats := range allPoolStats {
		poolStats := serverStats.PoolStats
		fmt.Printf("\nServer: %s\n", serverStats.Addr)
		fmt.Printf("Connections:\n")
		fmt.Printf("  Total:    %d\n", poolStats.TotalConns)
		fmt.Printf("  Active:   %d\n", poolStats.ActiveConns)
		fmt.Printf("  Idle:     %d\n", poolStats.IdleConns)
		fmt.Printf("  Created:  %s\n", formatNumber(int64(poolStats.CreatedConns)))
		fmt.Printf("  Destroyed: %s\n", formatNumber(int64(poolStats.DestroyedConns)))

		fmt.Printf("\nAcquire Performance:\n")
		fmt.Printf("  Total:    %s\n", formatNumber(int64(poolStats.AcquireCount)))
		if poolStats.AcquireWaitCount > 0 {
			waitPct := float64(poolStats.AcquireWaitCount) / float64(poolStats.AcquireCount) * 100
			avgWait := time.Duration(poolStats.AcquireWaitTimeNs / poolStats.AcquireWaitCount)
			fmt.Printf("  Waited:   %s (%.1f%%, avg %s)\n",
				formatNumber(int64(poolStats.AcquireWaitCount)),
				waitPct,
				formatDuration(avgWait))
		}
		if poolStats.AcquireErrors > 0 {
			fmt.Printf("  Errors:   %s\n", formatNumber(int64(poolStats.AcquireErrors)))
		}
	}
}
