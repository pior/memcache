package memcache

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMemcacheAddr = "127.0.0.1:11211"
)

// createTestClient creates a client for integration testing
func createTestClient(t *testing.T) *Client {
	t.Helper()

	config := Config{
		MaxSize:             10,
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 10 * time.Second,
	}

	client, err := NewClient(testMemcacheAddr, config)
	require.NoError(t, err)

	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func TestIntegration_GetSet(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		key   string
		value []byte
		ttl   time.Duration
	}{
		{
			name:  "simple string value",
			key:   "test:simple",
			value: []byte("hello world"),
			ttl:   NoTTL,
		},
		{
			name:  "with TTL",
			key:   "test:ttl",
			value: []byte("expires"),
			ttl:   60 * time.Second,
		},
		{
			name:  "empty value",
			key:   "test:empty",
			value: []byte{},
			ttl:   NoTTL,
		},
		{
			name:  "binary data",
			key:   "test:binary",
			value: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE},
			ttl:   NoTTL,
		},
		{
			name:  "large value",
			key:   "test:large",
			value: make([]byte, 10000),
			ttl:   NoTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the item
			err := client.Set(ctx, Item{
				Key:   tt.key,
				Value: tt.value,
				TTL:   tt.ttl,
			})
			require.NoError(t, err)

			// Get the item back
			item, err := client.Get(ctx, tt.key)
			require.NoError(t, err)
			assert.True(t, item.Found)
			assert.Equal(t, tt.key, item.Key)
			assert.Equal(t, tt.value, item.Value)

			// Clean up
			err = client.Delete(ctx, tt.key)
			require.NoError(t, err)
		})
	}
}

func TestIntegration_GetMiss(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	// Try to get a non-existent key
	item, err := client.Get(ctx, "nonexistent:key")
	require.NoError(t, err)
	assert.False(t, item.Found)
	assert.Equal(t, "nonexistent:key", item.Key)
	assert.Nil(t, item.Value)
}

func TestIntegration_Add(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:add"

	// Ensure key doesn't exist
	_ = client.Delete(ctx, key)

	// First add should succeed
	err := client.Add(ctx, Item{
		Key:   key,
		Value: []byte("first"),
	})
	require.NoError(t, err)

	// Verify it was stored
	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)
	assert.Equal(t, []byte("first"), item.Value)

	// Second add should fail (key exists)
	err = client.Add(ctx, Item{
		Key:   key,
		Value: []byte("second"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Value should still be "first"
	item, err = client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)
	assert.Equal(t, []byte("first"), item.Value)

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_Delete(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:delete"

	// Set a key
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("to be deleted"),
	})
	require.NoError(t, err)

	// Verify it exists
	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	// Delete it
	err = client.Delete(ctx, key)
	require.NoError(t, err)

	// Verify it's gone
	item, err = client.Get(ctx, key)
	require.NoError(t, err)
	assert.False(t, item.Found)

	// Delete non-existent key should not error
	err = client.Delete(ctx, "nonexistent:key")
	require.NoError(t, err)
}

func TestIntegration_Increment(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:counter"

	// Clean up first
	_ = client.Delete(ctx, key)

	tests := []struct {
		name          string
		delta         int64
		expectedValue int64
	}{
		{
			name:          "first increment creates with delta",
			delta:         1,
			expectedValue: 1,
		},
		{
			name:          "increment by 5",
			delta:         5,
			expectedValue: 6,
		},
		{
			name:          "increment by 10",
			delta:         10,
			expectedValue: 16,
		},
		{
			name:          "decrement with negative delta",
			delta:         -3,
			expectedValue: 13,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := client.Increment(ctx, key, tt.delta, NoTTL)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedValue, value)
		})
	}

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_IncrementNegativeDelta(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:counter:negative"

	// Clean up first
	_ = client.Delete(ctx, key)

	tests := []struct {
		name          string
		delta         int64
		expectedValue int64
		description   string
	}{
		{
			name:          "first decrement creates with 0",
			delta:         -5,
			expectedValue: 0,
			description:   "First negative delta initializes counter to 0 (can't start negative)",
		},
		{
			name:          "decrement by 3 (wraps to 0)",
			delta:         -3,
			expectedValue: 0,
			description:   "Decrement from 0 stays at 0 (memcache doesn't go negative)",
		},
		{
			name:          "increment by 10",
			delta:         10,
			expectedValue: 10,
			description:   "Positive delta increases counter normally",
		},
		{
			name:          "decrement by 3",
			delta:         -3,
			expectedValue: 7,
			description:   "Negative delta decrements from positive value",
		},
		{
			name:          "decrement by 10 (wraps to 0)",
			delta:         -10,
			expectedValue: 0,
			description:   "Decrementing below 0 wraps to 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := client.Increment(ctx, key, tt.delta, NoTTL)
			require.NoError(t, err, tt.description)
			assert.Equal(t, tt.expectedValue, value, tt.description)
		})
	}

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_IncrementWithTTL(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:counter:ttl"
	_ = client.Delete(ctx, key)

	// Increment with 2 second TTL
	value, err := client.Increment(ctx, key, 5, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(5), value)

	// Should exist immediately
	value, err = client.Increment(ctx, key, 3, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(8), value)

	// Wait for expiration
	time.Sleep(3 * time.Second)

	// Should be gone and recreated with delta
	value, err = client.Increment(ctx, key, 10, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(10), value, "After expiration, should create new counter with initial value = delta")

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_SetOverwrite(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:overwrite"

	// Set initial value
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("first"),
	})
	require.NoError(t, err)

	// Overwrite with new value
	err = client.Set(ctx, Item{
		Key:   key,
		Value: []byte("second"),
	})
	require.NoError(t, err)

	// Verify new value
	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)
	assert.Equal(t, []byte("second"), item.Value)

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_TTLExpiration(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:expire"

	// Set with 2 second TTL
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("expires soon"),
		TTL:   2 * time.Second,
	})
	require.NoError(t, err)

	// Should exist immediately
	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	// Wait for expiration
	time.Sleep(3 * time.Second)

	// Should be gone now
	item, err = client.Get(ctx, key)
	require.NoError(t, err)
	assert.False(t, item.Found)
}

func TestIntegration_ErrorCases(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	t.Run("set with invalid key too long", func(t *testing.T) {
		err := client.Set(ctx, Item{
			Key:   strings.Repeat("k", 251),
			Value: []byte("value"),
		})
		assert.EqualError(t, err, "key exceeds maximum length of 250 bytes")
		var wantErr *meta.InvalidKeyError
		assert.ErrorAs(t, err, &wantErr)
	})

	t.Run("get with empty key", func(t *testing.T) {
		_, err := client.Get(ctx, "")
		assert.EqualError(t, err, "key is empty")
	})

	t.Run("increment non-numeric value", func(t *testing.T) {
		key := "test:nonnumeric"
		_ = client.Delete(ctx, key)

		// Set a non-numeric value
		err := client.Set(ctx, Item{Key: key, Value: []byte("not a number")})
		require.NoError(t, err)

		// Try to increment - memcache should return CLIENT_ERROR
		_, err = client.Increment(ctx, key, 1, NoTTL)
		assert.EqualError(t, err, "CLIENT_ERROR: cannot increment or decrement non-numeric value")
	})
}

func TestIntegration_ContextCancellation(t *testing.T) {
	client := createTestClient(t)

	t.Run("cancelled context on get", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := client.Get(ctx, "test:key")
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("timeout context on get", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		time.Sleep(10 * time.Millisecond) // Ensure timeout occurs

		_, err := client.Get(ctx, "test:key")
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestIntegration_ConnectionPooling(t *testing.T) {
	// Create client with small pool
	config := Config{
		MaxSize:             2,
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 0, // Disable health checks for this test
	}

	client, err := NewClient(testMemcacheAddr, config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Perform multiple operations - should reuse connections
	for i := range 10 {
		key := fmt.Sprintf("test:pool:%d", i)
		err := client.Set(ctx, Item{
			Key:   key,
			Value: []byte(fmt.Sprintf("value%d", i)),
		})
		require.NoError(t, err)

		item, err := client.Get(ctx, key)
		require.NoError(t, err)
		assert.True(t, item.Found)

		err = client.Delete(ctx, key)
		require.NoError(t, err)
	}
}

func TestIntegration_Concurrency(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	numGoroutines := 50
	numOperations := 20

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numOperations)

	// Launch concurrent goroutines
	for i := range numGoroutines {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range numOperations {
				key := fmt.Sprintf("test:concurrent:%d:%d", workerID, j)

				// Set
				err := client.Set(ctx, Item{
					Key:   key,
					Value: []byte(fmt.Sprintf("value-%d-%d", workerID, j)),
				})
				if err != nil {
					errors <- fmt.Errorf("set failed: %w", err)
					continue
				}

				// Get
				item, err := client.Get(ctx, key)
				if err != nil {
					errors <- fmt.Errorf("get failed: %w", err)
					continue
				}
				if !item.Found {
					errors <- fmt.Errorf("item not found: %s", key)
					continue
				}

				// Delete
				err = client.Delete(ctx, key)
				if err != nil {
					errors <- fmt.Errorf("delete failed: %w", err)
					continue
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errorList []error
	for err := range errors {
		errorList = append(errorList, err)
	}

	if len(errorList) > 0 {
		t.Errorf("Got %d errors during concurrent operations:", len(errorList))
		for _, err := range errorList {
			t.Logf("  - %v", err)
		}
	}
}

func TestIntegration_ConcurrentCounters(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()

	key := "test:shared:counter"
	_ = client.Delete(ctx, key)

	numGoroutines := 10
	incrementsPerGoroutine := 10

	var wg sync.WaitGroup

	// Launch concurrent incrementers
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range incrementsPerGoroutine {
				_, err := client.Increment(ctx, key, 1, NoTTL)
				if err != nil {
					t.Errorf("increment failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// Final value should be numGoroutines * incrementsPerGoroutine
	expectedValue := int64(numGoroutines * incrementsPerGoroutine)
	finalValue, err := client.Increment(ctx, key, 0, NoTTL)
	require.NoError(t, err)
	assert.Equal(t, expectedValue, finalValue)

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_HealthCheck(t *testing.T) {
	// Create client with short health check interval
	config := Config{
		MaxSize:             5,
		MaxConnLifetime:     10 * time.Second,
		MaxConnIdleTime:     5 * time.Second,
		HealthCheckInterval: 1 * time.Second,
	}

	client, err := NewClient(testMemcacheAddr, config)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Create some connections
	key := "test:healthcheck"
	err = client.Set(ctx, Item{
		Key:   key,
		Value: []byte("value"),
	})
	require.NoError(t, err)

	// Wait for health check to run
	time.Sleep(2 * time.Second)

	// Connections should still work
	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	// Clean up
	_ = client.Delete(ctx, key)
}

func TestIntegration_Load(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	client := createTestClient(t)
	ctx := context.Background()

	numOperations := 10000

	t.Run("sequential load", func(t *testing.T) {
		start := time.Now()

		for i := range numOperations {
			key := fmt.Sprintf("test:load:seq:%d", i)

			err := client.Set(ctx, Item{
				Key:   key,
				Value: []byte(strconv.Itoa(i)),
			})
			require.NoError(t, err)

			item, err := client.Get(ctx, key)
			require.NoError(t, err)
			assert.True(t, item.Found)

			err = client.Delete(ctx, key)
			require.NoError(t, err)
		}

		duration := time.Since(start)
		opsPerSec := float64(numOperations*3) / duration.Seconds() // 3 ops per iteration
		t.Logf("Sequential: %d operations in %v (%.0f ops/sec)", numOperations*3, duration, opsPerSec)
	})

	t.Run("concurrent load", func(t *testing.T) {
		numWorkers := 20
		opsPerWorker := numOperations / numWorkers

		start := time.Now()

		var wg sync.WaitGroup
		for i := range numWorkers {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()

				for j := range opsPerWorker {
					key := fmt.Sprintf("test:load:conc:%d:%d", workerID, j)

					err := client.Set(ctx, Item{
						Key:   key,
						Value: []byte(strconv.Itoa(j)),
					})
					if err != nil {
						t.Errorf("set failed: %v", err)
						return
					}

					item, err := client.Get(ctx, key)
					if err != nil {
						t.Errorf("get failed: %v", err)
						return
					}
					if !item.Found {
						t.Errorf("item not found: %s", key)
						return
					}

					err = client.Delete(ctx, key)
					if err != nil {
						t.Errorf("delete failed: %v", err)
						return
					}
				}
			}(i)
		}

		wg.Wait()

		duration := time.Since(start)
		totalOps := numOperations * 3 // 3 ops per iteration
		opsPerSec := float64(totalOps) / duration.Seconds()
		t.Logf("Concurrent (%d workers): %d operations in %v (%.0f ops/sec)", numWorkers, totalOps, duration, opsPerSec)
	})

	t.Run("mixed operations load", func(t *testing.T) {
		numWorkers := 10
		opsPerWorker := numOperations / numWorkers

		start := time.Now()

		var wg sync.WaitGroup
		for i := range numWorkers {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()

				for j := range opsPerWorker {
					key := fmt.Sprintf("test:load:mixed:%d:%d", workerID, j)

					switch j % 5 {
					case 0: // Set
						err := client.Set(ctx, Item{
							Key:   key,
							Value: []byte(strconv.Itoa(j)),
						})
						if err != nil {
							t.Errorf("set failed: %v", err)
						}
					case 1: // Get
						_, err := client.Get(ctx, key)
						if err != nil {
							t.Errorf("get failed: %v", err)
						}
					case 2: // Add
						_ = client.Add(ctx, Item{
							Key:   key,
							Value: []byte(strconv.Itoa(j)),
						})
					case 3: // Increment
						counterKey := fmt.Sprintf("test:load:counter:%d", workerID)
						_, err := client.Increment(ctx, counterKey, 1, NoTTL)
						if err != nil {
							t.Errorf("increment failed: %v", err)
						}
					case 4: // Delete
						err := client.Delete(ctx, key)
						if err != nil {
							t.Errorf("delete failed: %v", err)
						}
					}
				}
			}(i)
		}

		wg.Wait()

		duration := time.Since(start)
		opsPerSec := float64(numOperations) / duration.Seconds()
		t.Logf("Mixed operations (%d workers): %d operations in %v (%.0f ops/sec)", numWorkers, numOperations, duration, opsPerSec)

		// Clean up counters
		for i := range numWorkers {
			counterKey := fmt.Sprintf("test:load:counter:%d", i)
			_ = client.Delete(ctx, counterKey)
		}
	})
}
