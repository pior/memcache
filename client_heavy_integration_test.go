package memcache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
)

const (
	RequestsPerBatch    = 1000            // Number of requests per batch
	TestDuration        = 2 * time.Second // Duration for each heavy test
	RequiredSuccessRate = 100.0           // Required success rate percentage
)

func TestHeavy_CacheHitOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	key := "heavy-cache-hit-key"
	value := []byte("heavy-cache-hit-value-with-some-content")
	concurrency := 4

	t.Logf("Setting up initial value for cache-hit test...")

	// Set the initial value
	setCmd := NewSetCommand(key, value, time.Hour)
	err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Failed to set initial value: %v", err)
	}

	resp, err := setCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get set response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("Set command failed: %v", resp.Error)
	}

	t.Logf("Starting cache-hit test with %d workers for %v...", concurrency, TestDuration)

	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			batchCount := 0
			for time.Since(startTime) < TestDuration {
				// Perform requests per batch (configurable)
				for range RequestsPerBatch {
					opStart := time.Now()
					getCmd := NewGetCommand(key)
					err := client.Do(ctx, getCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))

					if err != nil {
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Get error: %v", workerID, err)
						continue
					}

					getResp, err := getCmd.GetResponse(ctx)
					if err != nil {
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Failed to get response: %v", workerID, err)
						continue
					}

					if getResp.Error != nil {
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Response error: %v", workerID, getResp.Error)
					} else {
						atomic.AddInt64(&successes, 1)
						// Verify correctness
						if string(getResp.Value) != string(value) {
							t.Errorf("Worker %d: Value mismatch - expected %q, got %q",
								workerID, string(value), string(getResp.Value))
						}
					}
				}
				batchCount++
			}
			t.Logf("Worker %d completed %d batches", workerID, batchCount)
		}(i)
	}

	wg.Wait()

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Cache-hit test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)

	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}

	// Expect at least some reasonable throughput (should be much higher in practice)
	if opsPerSecond < 100 {
		t.Errorf("Throughput too low: %.2f ops/sec (expected >= 100)", opsPerSecond)
	}
}

func TestHeavy_DynamicValueOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	concurrency := 3

	t.Logf("Starting dynamic-value test with %d workers for %v...", concurrency, TestDuration)

	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < TestDuration {
				// Each operation: set a dynamic value, then get it
				key := fmt.Sprintf("heavy-dynamic-key-%d-%d", workerID, opCount)
				value := []byte(fmt.Sprintf("heavy-dynamic-value-%d-%d-%d", workerID, opCount, time.Now().UnixNano()))

				// Set operation
				opStart := time.Now()
				setCmd := NewSetCommand(key, value, time.Minute)
				err := client.Do(ctx, setCmd)
				setLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(setLatency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					t.Logf("Worker %d: Set error: %v", workerID, err)
					continue
				}

				setResp, err := setCmd.GetResponse(ctx)
				if err != nil || setResp.Error != nil {
					atomic.AddInt64(&failures, 1)
					if err != nil {
						t.Logf("Worker %d: Set response error: %v", workerID, err)
					}
					continue
				}

				// Get operation
				opStart = time.Now()
				getCmd := NewGetCommand(key)
				err = client.Do(ctx, getCmd)
				getLatency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(getLatency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					t.Logf("Worker %d: Get error: %v", workerID, err)
					continue
				}

				getResp, err := getCmd.GetResponse(ctx)
				if err != nil || getResp.Error != nil {
					atomic.AddInt64(&failures, 1)
					if err != nil {
						t.Logf("Worker %d: Get response error: %v", workerID, err)
					}
				} else {
					atomic.AddInt64(&successes, 2) // Both set and get succeeded
					// Verify correctness
					if string(getResp.Value) != string(value) {
						t.Errorf("Worker %d: Value mismatch for key %s - expected %q, got %q",
							workerID, key, string(value), string(getResp.Value))
					}
				}

				opCount++
			}
			t.Logf("Worker %d completed %d operations", workerID, opCount)
		}(i)
	}

	wg.Wait()

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Dynamic-value test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)

	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}
}

func TestHeavy_CacheMissOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	concurrency := 4

	t.Logf("Starting cache-miss test with %d workers for %v...", concurrency, TestDuration)

	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < TestDuration {
				// Try to get a non-existent key
				key := fmt.Sprintf("heavy-nonexistent-key-%d-%d-%d", workerID, opCount, time.Now().UnixNano())

				opStart := time.Now()
				getCmd := NewGetCommand(key)
				err := client.Do(ctx, getCmd)
				latency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(latency))

				if err != nil {
					// For cache misses, we expect ErrCacheMiss
					if err == protocol.ErrCacheMiss {
						atomic.AddInt64(&successes, 1)
					} else {
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Unexpected error: %v", workerID, err)
					}
				} else {
					getResp, err := getCmd.GetResponse(ctx)
					if err != nil {
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Get response error: %v", workerID, err)
					} else if getResp.Error == protocol.ErrCacheMiss {
						// Cache miss in response
						atomic.AddInt64(&successes, 1)
					} else {
						// Unexpected success (key shouldn't exist)
						atomic.AddInt64(&failures, 1)
						t.Logf("Worker %d: Unexpected success for non-existent key %s", workerID, key)
					}
				}

				opCount++
			}
			t.Logf("Worker %d completed %d operations", workerID, opCount)
		}(i)
	}

	wg.Wait()

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Cache-miss test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)

	// Expect 100% success rate (cache misses should be handled correctly)
	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}
}

func TestHeavy_IncrementOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	concurrency := 3

	// Set up initial counters
	keys := make([]string, concurrency)
	for i := range concurrency {
		keys[i] = fmt.Sprintf("heavy-counter-%d", i)
		setCmd := NewSetCommand(keys[i], []byte("0"), time.Hour)
		err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Failed to execute set command for counter %s: %v", keys[i], err)
		}

		setResp, err := setCmd.GetResponse(ctx)
		if err != nil || setResp.Error != nil {
			t.Fatalf("Failed to set initial counter %s: %v", keys[i], err)
		}
	}

	t.Logf("Starting increment test with %d workers for %v...", concurrency, TestDuration)

	var totalOps, successes, failures int64
	var totalLatency int64
	var incrementCounts []int64 = make([]int64, concurrency)

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			key := keys[workerID]

			for time.Since(startTime) < TestDuration {
				opStart := time.Now()
				incrCmd := NewIncrementCommand(key, 1)
				err := client.Do(ctx, incrCmd)
				latency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(latency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					t.Logf("Worker %d: Increment error: %v", workerID, err)
					continue
				}

				incrResp, err := incrCmd.GetResponse(ctx)
				if err != nil || incrResp.Error != nil {
					atomic.AddInt64(&failures, 1)
					if err != nil {
						t.Logf("Worker %d: Increment response error: %v", workerID, err)
					}
				} else {
					atomic.AddInt64(&successes, 1)
					atomic.AddInt64(&incrementCounts[workerID], 1)
				}

				opCount++
			}
			t.Logf("Worker %d completed %d increment operations", workerID, opCount)
		}(i)
	}

	wg.Wait()

	// Verify final counter values
	for i := range concurrency {
		getCmd := NewGetCommand(keys[i])
		err := client.Do(ctx, getCmd)
		if err != nil {
			t.Errorf("Failed to execute get command for final counter %s: %v", keys[i], err)
			continue
		}

		getResp, err := getCmd.GetResponse(ctx)
		if err != nil || getResp.Error != nil {
			t.Errorf("Failed to get final counter value for %s: %v", keys[i], err)
		} else {
			finalValue := string(getResp.Value)
			expectedCount := atomic.LoadInt64(&incrementCounts[i])
			t.Logf("Counter %s: final value = %s, expected increments = %d", keys[i], finalValue, expectedCount)
		}
	}

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Increment test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)

	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}
}

func TestHeavy_DeleteOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	concurrency := 3

	t.Logf("Starting delete test with %d workers for %v...", concurrency, TestDuration)

	var totalOps, successes, failures int64
	var totalLatency int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < TestDuration {
				// Set a key, then delete it
				key := fmt.Sprintf("heavy-delete-key-%d-%d", workerID, opCount)
				value := []byte(fmt.Sprintf("heavy-delete-value-%d-%d", workerID, opCount))

				// Set the key
				setCmd := NewSetCommand(key, value, time.Minute)
				err := client.Do(ctx, setCmd)
				if err != nil {
					t.Logf("Worker %d: Failed to set key for deletion: %v", workerID, err)
					continue
				}

				// Delete the key
				opStart := time.Now()
				deleteCmd := NewDeleteCommand(key)
				err = client.Do(ctx, deleteCmd)
				latency := time.Since(opStart)

				atomic.AddInt64(&totalOps, 1)
				atomic.AddInt64(&totalLatency, int64(latency))

				if err != nil {
					atomic.AddInt64(&failures, 1)
					t.Logf("Worker %d: Delete error: %v", workerID, err)
					continue
				}

				deleteResp, err := deleteCmd.GetResponse(ctx)
				if err != nil || deleteResp.Error != nil {
					atomic.AddInt64(&failures, 1)
					if err != nil {
						t.Logf("Worker %d: Delete response error: %v", workerID, err)
					}
				} else {
					atomic.AddInt64(&successes, 1)
				}

				opCount++
			}
			t.Logf("Worker %d completed %d delete operations", workerID, opCount)
		}(i)
	}

	wg.Wait()

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Delete test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)

	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}
}

func TestHeavy_MixedOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()
	duration := 3 * TestDuration // Mixed test runs 3x longer
	concurrency := 4

	t.Logf("Starting mixed operations test with %d workers for %v...", concurrency, duration)

	var totalOps, successes, failures int64
	var totalLatency int64
	var opCounts [5]int64 // [get, set, delete, increment, miss]

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			localOpCount := 0
			for time.Since(startTime) < duration {
				// Cycle through different operations
				switch localOpCount % 5 {
				case 0: // Cache hit
					key := fmt.Sprintf("mixed-hit-key-%d", workerID)
					value := []byte("mixed-hit-value")

					// Ensure key exists
					setCmd := NewSetCommand(key, value, time.Minute)
					client.Do(ctx, setCmd)

					opStart := time.Now()
					getCmd := NewGetCommand(key)
					err := client.Do(ctx, getCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					atomic.AddInt64(&opCounts[0], 1)

					if err != nil {
						atomic.AddInt64(&failures, 1)
						continue
					}

					getResp, err := getCmd.GetResponse(ctx)
					if err != nil || getResp.Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
					}

				case 1: // Set operation
					key := fmt.Sprintf("mixed-set-key-%d-%d", workerID, localOpCount)
					value := []byte(fmt.Sprintf("mixed-set-value-%d", localOpCount))

					opStart := time.Now()
					setCmd := NewSetCommand(key, value, time.Minute)
					err := client.Do(ctx, setCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					atomic.AddInt64(&opCounts[1], 1)

					if err != nil {
						atomic.AddInt64(&failures, 1)
						continue
					}

					setResp, err := setCmd.GetResponse(ctx)
					if err != nil || setResp.Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
					}

				case 2: // Delete operation
					key := fmt.Sprintf("mixed-delete-key-%d-%d", workerID, localOpCount)
					value := []byte("mixed-delete-value")

					// Set key first
					setCmd := NewSetCommand(key, value, time.Minute)
					client.Do(ctx, setCmd)

					opStart := time.Now()
					deleteCmd := NewDeleteCommand(key)
					err := client.Do(ctx, deleteCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					atomic.AddInt64(&opCounts[2], 1)

					if err != nil {
						atomic.AddInt64(&failures, 1)
						continue
					}

					deleteResp, err := deleteCmd.GetResponse(ctx)
					if err != nil || deleteResp.Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
					}

				case 3: // Increment operation
					key := fmt.Sprintf("mixed-counter-%d", workerID)

					// Ensure counter exists
					setCmd := NewSetCommand(key, []byte("0"), time.Minute)
					client.Do(ctx, setCmd)

					opStart := time.Now()
					incrCmd := NewIncrementCommand(key, 1)
					err := client.Do(ctx, incrCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					atomic.AddInt64(&opCounts[3], 1)

					if err != nil {
						atomic.AddInt64(&failures, 1)
						continue
					}

					incrResp, err := incrCmd.GetResponse(ctx)
					if err != nil || incrResp.Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
					}

				case 4: // Cache miss
					key := fmt.Sprintf("mixed-miss-key-%d-%d-%d", workerID, localOpCount, time.Now().UnixNano())

					opStart := time.Now()
					getCmd := NewGetCommand(key)
					err := client.Do(ctx, getCmd)
					latency := time.Since(opStart)

					atomic.AddInt64(&totalOps, 1)
					atomic.AddInt64(&totalLatency, int64(latency))
					atomic.AddInt64(&opCounts[4], 1)

					// For cache miss, we expect an error
					if err == protocol.ErrCacheMiss {
						atomic.AddInt64(&successes, 1)
					} else if err != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						getResp, err := getCmd.GetResponse(ctx)
						if err != nil {
							atomic.AddInt64(&failures, 1)
						} else if getResp.Error == protocol.ErrCacheMiss {
							atomic.AddInt64(&successes, 1)
						} else {
							// Unexpected success
							atomic.AddInt64(&failures, 1)
						}
					}
				}

				localOpCount++
			}
			t.Logf("Worker %d completed %d mixed operations", workerID, localOpCount)
		}(i)
	}

	wg.Wait()

	actualDuration := time.Since(startTime)
	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / actualDuration.Seconds()
	avgLatency := time.Duration(totalLatency / totalOps)

	t.Logf("Mixed operations test completed:")
	t.Logf("  Duration: %v", actualDuration)
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)
	t.Logf("  Avg Latency: %v", avgLatency)
	t.Logf("  Operation breakdown:")
	t.Logf("    Cache hits: %d", opCounts[0])
	t.Logf("    Sets: %d", opCounts[1])
	t.Logf("    Deletes: %d", opCounts[2])
	t.Logf("    Increments: %d", opCounts[3])
	t.Logf("    Cache misses: %d", opCounts[4])

	if successRate < RequiredSuccessRate {
		t.Errorf("Success rate too low: %.2f%% (expected >= %.1f%%)", successRate, RequiredSuccessRate)
	}
}
