package main

import (
	"context"
	crand "crypto/rand"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pior/memcache"
)

type Config struct {
	addr        string
	concurrency int
	cycles      int
	duration    time.Duration
}

type Stats struct {
	operations atomic.Int64
	successes  atomic.Int64
	misses     atomic.Int64
	failures   atomic.Int64
	errors     atomic.Int64
}

func (s *Stats) reset() {
	s.operations.Store(0)
	s.successes.Store(0)
	s.misses.Store(0)
	s.failures.Store(0)
	s.errors.Store(0)
}

func (s *Stats) snapshot() (ops, success, miss, fail, errs int64) {
	return s.operations.Load(), s.successes.Load(), s.misses.Load(), s.failures.Load(), s.errors.Load()
}

type Check struct {
	name     string
	duration time.Duration
	run      func(ctx context.Context, client *memcache.Client, stats *Stats, workerID int)
}

// isContextError returns true if the error is a context cancellation or deadline error
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func main() {
	config := Config{}
	flag.StringVar(&config.addr, "addr", "127.0.0.1:11211", "memcache server address")
	flag.IntVar(&config.concurrency, "concurrency", 10, "number of concurrent workers")
	flag.IntVar(&config.cycles, "cycles", 0, "number of cycles to run (0 = infinite)")
	flag.DurationVar(&config.duration, "duration", 5*time.Second, "duration per check")
	flag.Parse()

	fmt.Printf("Memcache Load Tester\n")
	fmt.Printf("====================\n")
	fmt.Printf("Server:      %s\n", config.addr)
	fmt.Printf("Concurrency: %d\n", config.concurrency)
	fmt.Printf("Cycles:      %s\n", cyclesString(config.cycles))
	fmt.Printf("Duration:    %s per check\n\n", config.duration)

	// Create client
	client, err := memcache.NewClient(config.addr, memcache.Config{
		MaxSize:             int32(config.concurrency * 2),
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 10 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n\nReceived interrupt signal, shutting down...")
		cancel()
	}()

	// Define checks
	checks := []Check{
		{
			name:     "Set/Get",
			duration: config.duration,
			run:      checkSetGet,
		},
		{
			name:     "Add",
			duration: config.duration,
			run:      checkAdd,
		},
		{
			name:     "Set/Delete/Get",
			duration: config.duration,
			run:      checkDelete,
		},
		{
			name:     "Increment",
			duration: config.duration,
			run:      checkIncrement,
		},
		{
			name:     "Decrement",
			duration: config.duration,
			run:      checkDecrement,
		},
		{
			name:     "Increment with TTL",
			duration: config.duration,
			run:      checkIncrementTTL,
		},
		{
			name:     "Mixed Operations",
			duration: config.duration,
			run:      checkMixed,
		},
		{
			name:     "Large Values",
			duration: config.duration,
			run:      checkLargeValues,
		},
		{
			name:     "Binary Data",
			duration: config.duration,
			run:      checkBinaryData,
		},
		{
			name:     "TTL Behavior",
			duration: config.duration,
			run:      checkTTL,
		},
	}

	// Run cycles
	cycle := 1
	for {
		if config.cycles > 0 && cycle > config.cycles {
			break
		}

		if ctx.Err() != nil {
			break
		}

		fmt.Printf("=== Cycle %d ===\n", cycle)

		for _, check := range checks {
			if ctx.Err() != nil {
				break
			}

			runCheck(ctx, client, check, config.concurrency)
		}

		fmt.Println()
		cycle++
	}

	fmt.Println("Load testing completed.")
}

func cyclesString(cycles int) string {
	if cycles == 0 {
		return "infinite"
	}
	return fmt.Sprintf("%d", cycles)
}

func runCheck(ctx context.Context, client *memcache.Client, check Check, concurrency int) {
	fmt.Printf("\n[%s]\n", check.name)

	stats := &Stats{}
	var wg sync.WaitGroup

	// Create context with timeout
	checkCtx, cancel := context.WithTimeout(ctx, check.duration)
	defer cancel()

	// Start progress reporter
	done := make(chan struct{})
	go reportProgress(checkCtx, stats, done)

	// Start workers
	startTime := time.Now()
	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for checkCtx.Err() == nil {
				check.run(checkCtx, client, stats, workerID)
			}
		}(i)
	}

	// Wait for workers to finish
	wg.Wait()
	close(done)

	// Print final stats
	duration := time.Since(startTime)
	ops, success, miss, fail, errs := stats.snapshot()
	opsPerSec := float64(ops) / duration.Seconds()

	fmt.Printf("\rCompleted: %d ops in %v (%.0f ops/sec) | Success: %d | Miss: %d | Fail: %d | Errors: %d\n",
		ops, duration.Round(time.Millisecond), opsPerSec, success, miss, fail, errs)
}

func reportProgress(ctx context.Context, stats *Stats, done chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	lastOps := int64(0)
	lastTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			now := time.Now()
			ops, success, miss, fail, errs := stats.snapshot()
			elapsed := now.Sub(lastTime).Seconds()

			rate := float64(ops-lastOps) / elapsed
			lastOps = ops
			lastTime = now

			fmt.Printf("\rRunning: %d ops (%.0f ops/sec) | Success: %d | Miss: %d | Fail: %d | Errors: %d",
				ops, rate, success, miss, fail, errs)
		}
	}
}

// Check implementations

func checkSetGet(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:setget:%d:%d", workerID, rand.IntN(100))
	value := []byte(fmt.Sprintf("value-%d", rand.IntN(1000)))

	// Set
	err := client.Set(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   memcache.NoTTL,
	})
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[SetGet] Set error for key %s: %v\n", key, err)
		}
		return
	}

	// Get
	item, err := client.Get(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[SetGet] Get error for key %s: %v\n", key, err)
		}
		return
	}

	stats.operations.Add(1)

	if !item.Found {
		stats.failures.Add(1)
		fmt.Printf("\n[SetGet] UNEXPECTED: Key %s not found after set\n", key)
		return
	}

	if string(item.Value) != string(value) {
		stats.failures.Add(1)
		fmt.Printf("\n[SetGet] UNEXPECTED: Value mismatch for key %s: expected %s, got %s\n", key, value, item.Value)
		return
	}

	stats.successes.Add(1)
}

func checkAdd(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:add:%d:%d", workerID, rand.IntN(1000))
	value := []byte(fmt.Sprintf("value-%d", rand.IntN(1000)))

	// Delete first to ensure key doesn't exist
	_ = client.Delete(ctx, key)

	// Add should succeed
	err := client.Add(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   memcache.NoTTL,
	})

	stats.operations.Add(1)

	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Add] First add error for key %s: %v\n", key, err)
		}
		return
	}

	// Second add should fail
	err = client.Add(ctx, memcache.Item{
		Key:   key,
		Value: []byte("different"),
		TTL:   memcache.NoTTL,
	})

	if err == nil {
		stats.failures.Add(1)
		fmt.Printf("\n[Add] UNEXPECTED: Second add succeeded for key %s (should fail)\n", key)
		return
	}

	stats.successes.Add(1)
}

func checkDelete(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:delete:%d:%d", workerID, rand.IntN(100))
	value := []byte(fmt.Sprintf("value-%d", rand.IntN(1000)))

	// Set
	err := client.Set(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   memcache.NoTTL,
	})
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Delete] Set error for key %s: %v\n", key, err)
		}
		return
	}

	// Delete
	err = client.Delete(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Delete] Delete error for key %s: %v\n", key, err)
		}
		return
	}

	// Get should return not found
	item, err := client.Get(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Delete] Get error for key %s: %v\n", key, err)
		}
		return
	}

	stats.operations.Add(1)

	if item.Found {
		stats.failures.Add(1)
		fmt.Printf("\n[Delete] UNEXPECTED: Key %s found after delete\n", key)
		return
	}

	stats.successes.Add(1)
	stats.misses.Add(1)
}

func checkIncrement(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:incr:%d", workerID)

	// Delete first
	_ = client.Delete(ctx, key)

	// First increment should create with value = delta
	value, err := client.Increment(ctx, key, 5, memcache.NoTTL)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Increment] First increment error for key %s: %v\n", key, err)
		}
		return
	}

	if value != 5 {
		stats.failures.Add(1)
		fmt.Printf("\n[Increment] UNEXPECTED: First increment returned %d, expected 5\n", value)
		return
	}

	// Second increment
	value, err = client.Increment(ctx, key, 3, memcache.NoTTL)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Increment] Second increment error for key %s: %v\n", key, err)
		}
		return
	}

	if value != 8 {
		stats.failures.Add(1)
		fmt.Printf("\n[Increment] UNEXPECTED: Second increment returned %d, expected 8\n", value)
		return
	}

	stats.operations.Add(1)
	stats.successes.Add(1)
}

func checkDecrement(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:decr:%d", workerID)

	// Delete first
	_ = client.Delete(ctx, key)

	// First decrement should create with value = 0 (can't start negative)
	value, err := client.Increment(ctx, key, -5, memcache.NoTTL)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Decrement] First decrement error for key %s: %v\n", key, err)
		}
		return
	}

	if value != 0 {
		stats.failures.Add(1)
		fmt.Printf("\n[Decrement] UNEXPECTED: First decrement returned %d, expected 0\n", value)
		return
	}

	// Increment to positive value
	value, err = client.Increment(ctx, key, 10, memcache.NoTTL)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Decrement] Increment error for key %s: %v\n", key, err)
		}
		return
	}

	// Decrement
	value, err = client.Increment(ctx, key, -3, memcache.NoTTL)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[Decrement] Decrement error for key %s: %v\n", key, err)
		}
		return
	}

	if value != 7 {
		stats.failures.Add(1)
		fmt.Printf("\n[Decrement] UNEXPECTED: Decrement returned %d, expected 7\n", value)
		return
	}

	stats.operations.Add(1)
	stats.successes.Add(1)
}

func checkIncrementTTL(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:incrttl:%d:%d", workerID, rand.IntN(1000))

	// Delete first
	_ = client.Delete(ctx, key)

	// Increment with short TTL
	value, err := client.Increment(ctx, key, 1, 2*time.Second)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[IncrementTTL] Increment error for key %s: %v\n", key, err)
		}
		return
	}

	if value != 1 {
		stats.failures.Add(1)
		fmt.Printf("\n[IncrementTTL] UNEXPECTED: Increment returned %d, expected 1\n", value)
		return
	}

	stats.operations.Add(1)
	stats.successes.Add(1)
}

func checkMixed(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	keyBase := fmt.Sprintf("test:mixed:%d", workerID)
	op := rand.IntN(5)

	switch op {
	case 0: // Set
		key := fmt.Sprintf("%s:set:%d", keyBase, rand.IntN(50))
		err := client.Set(ctx, memcache.Item{
			Key:   key,
			Value: []byte(fmt.Sprintf("value-%d", rand.IntN(1000))),
			TTL:   memcache.NoTTL,
		})
		if err != nil {
			if !isContextError(err) {
				stats.errors.Add(1)
			}
			return
		}
		stats.operations.Add(1)
		stats.successes.Add(1)

	case 1: // Get
		key := fmt.Sprintf("%s:set:%d", keyBase, rand.IntN(50))
		item, err := client.Get(ctx, key)
		if err != nil {
			if !isContextError(err) {
				stats.errors.Add(1)
			}
			return
		}
		stats.operations.Add(1)
		if item.Found {
			stats.successes.Add(1)
		} else {
			stats.misses.Add(1)
		}

	case 2: // Delete
		key := fmt.Sprintf("%s:set:%d", keyBase, rand.IntN(50))
		err := client.Delete(ctx, key)
		if err != nil {
			if !isContextError(err) {
				stats.errors.Add(1)
			}
			return
		}
		stats.operations.Add(1)
		stats.successes.Add(1)

	case 3: // Increment
		key := fmt.Sprintf("%s:counter:%d", keyBase, rand.IntN(10))
		_, err := client.Increment(ctx, key, int64(rand.IntN(10)+1), memcache.NoTTL)
		if err != nil {
			if !isContextError(err) {
				stats.errors.Add(1)
			}
			return
		}
		stats.operations.Add(1)
		stats.successes.Add(1)

	case 4: // Add
		key := fmt.Sprintf("%s:add:%d", keyBase, rand.IntN(100))
		err := client.Add(ctx, memcache.Item{
			Key:   key,
			Value: []byte(fmt.Sprintf("value-%d", rand.IntN(1000))),
			TTL:   memcache.NoTTL,
		})
		stats.operations.Add(1)
		if err != nil {
			// Add can legitimately fail if key exists
			if !isContextError(err) {
				stats.failures.Add(1)
			}
		} else {
			stats.successes.Add(1)
		}
	}
}

func checkLargeValues(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:large:%d:%d", workerID, rand.IntN(10))
	size := 50000 + rand.IntN(50000) // 50-100KB
	value := make([]byte, size)
	crand.Read(value)

	// Set
	err := client.Set(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   memcache.NoTTL,
	})
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[LargeValues] Set error for key %s: %v\n", key, err)
		}
		return
	}

	// Get
	item, err := client.Get(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[LargeValues] Get error for key %s: %v\n", key, err)
		}
		return
	}

	stats.operations.Add(1)

	if !item.Found {
		stats.failures.Add(1)
		fmt.Printf("\n[LargeValues] UNEXPECTED: Key %s not found after set\n", key)
		return
	}

	if len(item.Value) != len(value) {
		stats.failures.Add(1)
		fmt.Printf("\n[LargeValues] UNEXPECTED: Value size mismatch for key %s: expected %d, got %d\n", key, len(value), len(item.Value))
		return
	}

	stats.successes.Add(1)
}

func checkBinaryData(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:binary:%d:%d", workerID, rand.IntN(10))
	value := make([]byte, 100)
	crand.Read(value)

	// Set
	err := client.Set(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   memcache.NoTTL,
	})
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[BinaryData] Set error for key %s: %v\n", key, err)
		}
		return
	}

	// Get
	item, err := client.Get(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[BinaryData] Get error for key %s: %v\n", key, err)
		}
		return
	}

	stats.operations.Add(1)

	if !item.Found {
		stats.failures.Add(1)
		fmt.Printf("\n[BinaryData] UNEXPECTED: Key %s not found after set\n", key)
		return
	}

	if len(item.Value) != len(value) {
		stats.failures.Add(1)
		fmt.Printf("\n[BinaryData] UNEXPECTED: Value size mismatch for key %s: expected %d, got %d\n", key, len(value), len(item.Value))
		return
	}

	// Verify byte-by-byte
	for i := range value {
		if item.Value[i] != value[i] {
			stats.failures.Add(1)
			fmt.Printf("\n[BinaryData] UNEXPECTED: Value byte mismatch at index %d for key %s\n", i, key)
			return
		}
	}

	stats.successes.Add(1)
}

func checkTTL(ctx context.Context, client *memcache.Client, stats *Stats, workerID int) {
	key := fmt.Sprintf("test:ttl:%d:%d", workerID, rand.IntN(100))
	value := []byte(fmt.Sprintf("value-%d", rand.IntN(1000)))

	// Set with 2 second TTL
	err := client.Set(ctx, memcache.Item{
		Key:   key,
		Value: value,
		TTL:   2 * time.Second,
	})
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[TTL] Set error for key %s: %v\n", key, err)
		}
		return
	}

	// Get immediately - should exist
	item, err := client.Get(ctx, key)
	if err != nil {
		if !isContextError(err) {
			stats.errors.Add(1)
			fmt.Printf("\n[TTL] Get error for key %s: %v\n", key, err)
		}
		return
	}

	stats.operations.Add(1)

	if !item.Found {
		stats.failures.Add(1)
		fmt.Printf("\n[TTL] UNEXPECTED: Key %s not found immediately after set\n", key)
		return
	}

	stats.successes.Add(1)
}
