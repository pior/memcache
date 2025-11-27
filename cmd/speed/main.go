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
	Name       string
	Initialize func(ctx context.Context, client Client, uid int64)
	Operation  OperationFunc
}

type OperationFunc func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error

type Result struct {
	name       string
	count      int64
	duration   time.Duration
	opsPerSec  float64
	avgLatency time.Duration
}

type Config struct {
	addr        string
	client      string
	pool        string
	concurrency int
	count       int64
	only        string
}

func main() {
	config := Config{}
	flag.StringVar(&config.addr, "addr", "127.0.0.1:11211", "memcache server address")
	flag.StringVar(&config.client, "client", "pior", "client implementation: pior or bradfitz")
	flag.StringVar(&config.pool, "pool", "channel", "pool implementation for pior client: channel or puddle")
	flag.IntVar(&config.concurrency, "concurrency", 1, "number of concurrent workers")
	flag.Int64Var(&config.count, "count", 1_000_000, "target operation count")
	flag.StringVar(&config.only, "only", "", "run only the specified operation (e.g., 'Set')")
	flag.Parse()

	if config.client != "pior" && config.client != "bradfitz" {
		log.Fatalf("Invalid client: %s (must be 'pior' or 'bradfitz')", config.client)
	}

	if config.pool != "channel" && config.pool != "puddle" {
		log.Fatalf("Invalid pool: %s (must be 'channel' or 'puddle')", config.pool)
	}

	fmt.Printf("Memcache Speed Test\n")
	fmt.Printf("===================\n")
	fmt.Printf("Client:      %s\n", config.client)
	if config.client == "pior" {
		fmt.Printf("Pool:        %s\n", config.pool)
	}
	fmt.Printf("Server:      %s\n", config.addr)
	fmt.Printf("Concurrency: %d\n", config.concurrency)
	fmt.Printf("Target:      %s operations\n\n", formatNumber(config.count))

	client, closeFunc := createClient(config)
	defer closeFunc()

	ctx := context.Background()
	uid := rand.Int64N(1_000_000)

	var tests = []Test{
		{
			Name: "get-miss",
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, _ = client.Get(ctx, key)
				return nil
			},
		},
		{
			Name: "set",
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Set(ctx, memcache.Item{
					Key:   key,
					Value: []byte("benchmark-value-0123456789"),
					TTL:   memcache.NoTTL,
				})
			},
		},
		{
			Name: "get-hit",
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				_, err := client.Get(ctx, key)
				return err
			},
		},
		{
			Name: "delete-found",
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name: "delete-miss",
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, operationID)
				return client.Delete(ctx, key)
			},
		},
		{
			Name: "increment",
			Initialize: func(ctx context.Context, client Client, uid int64) {
				key := fmt.Sprintf("test-%d-counter", uid)

				_ = client.Set(ctx, memcache.Item{
					Key:   key,
					Value: []byte("0"),
					TTL:   memcache.NoTTL,
				})
			},
			Operation: func(ctx context.Context, client Client, uid int64, workerID int, operationID int64) error {
				key := fmt.Sprintf("test-%d-counter", uid)
				_, err := client.Increment(ctx, key, 1, memcache.NoTTL)
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

		fmt.Printf("  Completed in %s (%.0f ops/sec, %s avg latency)\n",
			formatDuration(result.duration),
			result.opsPerSec,
			formatDuration(result.avgLatency),
		)

		results = append(results, result)
	}

	fmt.Printf("\n")
	fmt.Printf("%-20s %12s %10s %12s %12s\n", "Operation", "Count", "Duration", "Ops/sec", "Avg Latency")
	for _, result := range results {
		fmt.Printf("%-20s %12s %10s %12s %12s\n",
			result.name,
			formatNumber(result.count),
			formatDuration(result.duration),
			formatNumber(int64(result.opsPerSec)),
			formatDuration(result.avgLatency),
		)
	}

	// Display stats for pior client
	if config.client == "pior" {
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
	if test.Initialize != nil {
		test.Initialize(ctx, client, uid)
	}

	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				test.Operation(ctx, client, uid, workerID, j)
			}
		}(i)
	}

	wg.Wait()

	duration := time.Since(start)

	return Result{
		name:       test.Name,
		count:      config.count,
		duration:   duration,
		opsPerSec:  float64(config.count) / duration.Seconds(),
		avgLatency: duration / time.Duration(opsPerWorker),
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

	stats := piorCli.Stats()
	allPoolStats := piorCli.AllPoolStats()

	fmt.Printf("\n")
	fmt.Printf("Client Statistics\n")
	fmt.Printf("=================\n")
	fmt.Printf("Operations:\n")
	fmt.Printf("  Gets:       %s\n", formatNumber(int64(stats.Gets)))
	fmt.Printf("  Sets:       %s\n", formatNumber(int64(stats.Sets)))
	fmt.Printf("  Deletes:    %s\n", formatNumber(int64(stats.Deletes)))
	fmt.Printf("  Increments: %s\n", formatNumber(int64(stats.Increments)))
	if stats.Gets > 0 {
		hitRate := float64(stats.GetHits) / float64(stats.Gets) * 100
		fmt.Printf("  Get Hits:   %s (%.1f%%)\n", formatNumber(int64(stats.GetHits)), hitRate)
	}
	fmt.Printf("  Errors:     %s\n", formatNumber(int64(stats.Errors)))

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
