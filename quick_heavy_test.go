package memcache

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Quick version of cache hit test for validation
func TestHeavy_QuickCacheHit(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("SKIP_INTEGRATION is set")
	}

	client, err := NewClient(&ClientConfig{
		Servers: []string{"localhost:11211"},
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	key := "quick-cache-hit-key"
	value := []byte("quick-cache-hit-value")
	duration := 500 * time.Millisecond
	concurrency := 2

	// Set the initial value
	setCmd := NewSetCommand(key, value, time.Hour)
	responses, err := client.Do(ctx, setCmd)
	if err != nil || responses[0].Error != nil {
		t.Fatalf("Failed to set initial value: %v", err)
	}

	t.Logf("Starting quick cache-hit test with %d workers for %v...", concurrency, duration)

	var totalOps, successes, failures int64

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			opCount := 0
			for time.Since(startTime) < duration {
				// Perform 10 gets per batch
				for j := 0; j < 10; j++ {
					getCmd := NewGetCommand(key)
					responses, err := client.Do(ctx, getCmd)

					atomic.AddInt64(&totalOps, 1)

					if err != nil || len(responses) == 0 || responses[0].Error != nil {
						atomic.AddInt64(&failures, 1)
					} else {
						atomic.AddInt64(&successes, 1)
						// Verify correctness
						if string(responses[0].Value) != string(value) {
							t.Errorf("Value mismatch")
						}
					}
				}
				opCount++
				time.Sleep(10 * time.Millisecond)
			}
			t.Logf("Worker %d completed %d batches", workerID, opCount)
		}(i)
	}

	wg.Wait()

	successRate := float64(successes) / float64(totalOps) * 100
	opsPerSecond := float64(totalOps) / time.Since(startTime).Seconds()

	t.Logf("Quick cache-hit test results:")
	t.Logf("  Total Operations: %d", totalOps)
	t.Logf("  Successes: %d", successes)
	t.Logf("  Failures: %d", failures)
	t.Logf("  Success Rate: %.2f%%", successRate)
	t.Logf("  Ops/sec: %.2f", opsPerSecond)

	if successRate < 95.0 {
		t.Errorf("Success rate too low: %.2f%%", successRate)
	}
}
