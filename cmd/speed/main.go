package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache"
)

type Config struct {
	addr        string
	concurrency int
	count       int64
}

type Result struct {
	name       string
	count      int64
	duration   time.Duration
	opsPerSec  float64
	avgLatency time.Duration
}

func main() {
	config := Config{}
	flag.StringVar(&config.addr, "addr", "127.0.0.1:11211", "memcache server address")
	flag.IntVar(&config.concurrency, "concurrency", 1, "number of concurrent workers")
	flag.Int64Var(&config.count, "count", 1_000_000, "target operation count")
	flag.Parse()

	fmt.Printf("Memcache Speed Test\n")
	fmt.Printf("===================\n")
	fmt.Printf("Server:      %s\n", config.addr)
	fmt.Printf("Concurrency: %d\n", config.concurrency)
	fmt.Printf("Target:      %s operations\n\n", formatNumber(config.count))

	// Create client
	client, err := memcache.NewClient(config.addr, memcache.Config{
		MaxSize:             int32(config.concurrency * 2),
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 0, // Disable for speed test
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	ctx := context.Background()
	uid := rand.Int64N(1_000_000)

	// Run tests
	results := []Result{
		runGetMiss(ctx, client, config, uid),
		runSet(ctx, client, config, uid),
		runGetHit(ctx, client, config, uid),
		runDelete(ctx, client, config, uid),
		runDeleteMiss(ctx, client, config, uid),
		runIncrement(ctx, client, config, uid),
	}

	// Print summary
	fmt.Printf("\n")
	fmt.Printf("Summary\n")
	fmt.Printf("=======\n")
	fmt.Printf("%-20s %12s %10s %12s %12s\n", "Operation", "Count", "Duration", "Ops/sec", "Avg Latency")
	fmt.Printf("%-20s %12s %10s %12s %12s\n", "─────────", "─────", "────────", "───────", "───────────")
	for _, result := range results {
		fmt.Printf("%-20s %12s %10s %12s %12s\n",
			result.name,
			formatNumber(result.count),
			formatDuration(result.duration),
			formatNumber(int64(result.opsPerSec)),
			formatDuration(result.avgLatency),
		)
	}
}

func runGetMiss(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Get (miss) with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, j)
				_, _ = client.Get(ctx, key)
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Get (miss)",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
	}
}

func runSet(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Set with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	value := []byte("benchmark-value-0123456789")
	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, j)
				_ = client.Set(ctx, memcache.Item{
					Key:   key,
					Value: value,
					TTL:   memcache.NoTTL,
				})
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Set",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
	}
}

func runGetHit(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Get (hit) with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, j)
				_, _ = client.Get(ctx, key)
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Get (hit)",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
	}
}

func runDelete(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Delete (found) with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, j)
				_ = client.Delete(ctx, key)
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Delete (found)",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
	}
}

func runDeleteMiss(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Delete (miss) with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				key := fmt.Sprintf("test-%d-%d-%d", uid, workerID, j)
				_ = client.Delete(ctx, key)
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Delete (miss)",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
	}
}

func runIncrement(ctx context.Context, client *memcache.Client, config Config, uid int64) Result {
	fmt.Printf("Running: Increment with %s operations...\n", formatNumber(config.count))

	var completed atomic.Int64
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			key := fmt.Sprintf("test-%d-%d-counter", uid, workerID)
			for range opsPerWorker {
				_, _ = client.Increment(ctx, key, 1, memcache.NoTTL)
				completed.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	count := completed.Load()
	opsPerSec := float64(count) / duration.Seconds()
	avgLatency := duration / time.Duration(count)

	fmt.Printf("  Completed: %s ops in %s (%.0f ops/sec, %s avg latency)\n\n",
		formatNumber(count), formatDuration(duration), opsPerSec, formatDuration(avgLatency))

	return Result{
		name:       "Increment",
		count:      count,
		duration:   duration,
		opsPerSec:  opsPerSec,
		avgLatency: avgLatency,
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
