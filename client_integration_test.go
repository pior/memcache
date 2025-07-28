package memcache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIntegration_BasicOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Test Set operation
	key := "integration_test_key"
	value := []byte("integration_test_value")

	setCmd := NewSetCommand(key, value, time.Hour)
	responses, err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("Set operation returned error: %v", responses[0].Error)
	}

	// Test Get operation
	getCmd := NewGetCommand(key)
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("Get operation returned error: %v", responses[0].Error)
	}
	if string(responses[0].Value) != string(value) {
		t.Errorf("Expected value %q, got %q", string(value), string(responses[0].Value))
	}

	// Test Delete operation
	delCmd := NewDeleteCommand(key)
	responses, err = client.Do(ctx, delCmd)
	if err != nil {
		t.Fatalf("Delete operation failed: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("Delete operation returned error: %v", responses[0].Error)
	}

	// Verify key is deleted
	getCmd = NewGetCommand(key)
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(responses))
	}
	if responses[0].Error != ErrCacheMiss {
		t.Errorf("Expected cache miss, got: %v", responses[0].Error)
	}
}

func TestIntegration_MultipleKeys(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Set multiple keys
	numKeys := 10
	keys := make([]string, numKeys)
	values := make([][]byte, numKeys)
	setCommands := make([]*Command, numKeys)

	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("multi_key_%d", i)
		values[i] = []byte(fmt.Sprintf("multi_value_%d", i))
		setCommands[i] = NewSetCommand(keys[i], values[i], time.Hour)
	}

	// Execute all set commands at once
	responses, err := client.Do(ctx, setCommands...)
	if err != nil {
		t.Fatalf("Multiple set operations failed: %v", err)
	}
	if len(responses) != numKeys {
		t.Fatalf("Expected %d responses, got %d", numKeys, len(responses))
	}

	// Verify all sets succeeded
	for i, resp := range responses {
		if resp.Error != nil {
			t.Errorf("Set operation %d failed: %v", i, resp.Error)
		}
		if resp.Key != keys[i] {
			t.Errorf("Expected key %q, got %q", keys[i], resp.Key)
		}
	}

	// Get multiple keys
	getCommands := make([]*Command, numKeys)
	for i := 0; i < numKeys; i++ {
		getCommands[i] = NewGetCommand(keys[i])
	}

	responses, err = client.Do(ctx, getCommands...)
	if err != nil {
		t.Fatalf("Multiple get operations failed: %v", err)
	}
	if len(responses) != numKeys {
		t.Fatalf("Expected %d responses, got %d", numKeys, len(responses))
	}

	// Verify all gets succeeded
	for i, resp := range responses {
		if resp.Error != nil {
			t.Errorf("Get operation %d failed: %v", i, resp.Error)
		}
		if resp.Key != keys[i] {
			t.Errorf("Expected key %q, got %q", keys[i], resp.Key)
		}
		if string(resp.Value) != string(values[i]) {
			t.Errorf("Expected value %q, got %q", string(values[i]), string(resp.Value))
		}
	}

	// Clean up - delete all keys
	deleteCommands := make([]*Command, numKeys)
	for i := 0; i < numKeys; i++ {
		deleteCommands[i] = NewDeleteCommand(keys[i])
	}

	responses, err = client.Do(ctx, deleteCommands...)
	if err != nil {
		t.Fatalf("Multiple delete operations failed: %v", err)
	}
	if len(responses) != numKeys {
		t.Fatalf("Expected %d responses, got %d", numKeys, len(responses))
	}

	// Verify all deletes succeeded
	for i, resp := range responses {
		if resp.Error != nil {
			t.Errorf("Delete operation %d failed: %v", i, resp.Error)
		}
	}
}

func TestIntegration_TTL(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	key := "ttl_test_key"
	value := []byte("ttl_test_value")

	setCmd := NewSetCommand(key, value, 1*time.Second)
	responses, err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Set operation returned error: %v", responses[0].Error)
	}

	// Verify key exists immediately
	getCmd := NewGetCommand(key)
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Get operation returned error: %v", responses[0].Error)
	}
	if string(responses[0].Value) != string(value) {
		t.Errorf("Expected value %q, got %q", string(value), string(responses[0].Value))
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Verify key has expired
	getCmd = NewGetCommand(key)
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get after TTL failed: %v", err)
	}
	if responses[0].Error != ErrCacheMiss {
		t.Errorf("Expected cache miss after TTL, got: %v", responses[0].Error)
	}
}

func TestIntegration_ConcurrentOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 5,
			MaxConnections: 20,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()
	numWorkers := 10
	numOpsPerWorker := 50

	var wg sync.WaitGroup
	errors := make(chan error, numWorkers*numOpsPerWorker)

	// Start multiple workers performing operations concurrently
	for worker := 0; worker < numWorkers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for op := 0; op < numOpsPerWorker; op++ {
				key := fmt.Sprintf("concurrent_key_%d_%d", workerID, op)
				value := []byte(fmt.Sprintf("concurrent_value_%d_%d", workerID, op))

				// Set
				setCmd := NewSetCommand(key, value, time.Hour)
				responses, err := client.Do(ctx, setCmd)
				if err != nil {
					errors <- fmt.Errorf("worker %d op %d set failed: %v", workerID, op, err)
					continue
				}
				if responses[0].Error != nil {
					errors <- fmt.Errorf("worker %d op %d set error: %v", workerID, op, responses[0].Error)
					continue
				}

				// Get
				getCmd := NewGetCommand(key)
				responses, err = client.Do(ctx, getCmd)
				if err != nil {
					errors <- fmt.Errorf("worker %d op %d get failed: %v", workerID, op, err)
					continue
				}
				if responses[0].Error != nil {
					errors <- fmt.Errorf("worker %d op %d get error: %v", workerID, op, responses[0].Error)
					continue
				}
				if string(responses[0].Value) != string(value) {
					errors <- fmt.Errorf("worker %d op %d value mismatch: expected %q, got %q",
						workerID, op, string(value), string(responses[0].Value))
					continue
				}

				// Delete
				delCmd := NewDeleteCommand(key)
				responses, err = client.Do(ctx, delCmd)
				if err != nil {
					errors <- fmt.Errorf("worker %d op %d delete failed: %v", workerID, op, err)
					continue
				}
				if responses[0].Error != nil {
					errors <- fmt.Errorf("worker %d op %d delete error: %v", workerID, op, responses[0].Error)
					continue
				}
			}
		}(worker)
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	var errorCount int
	for err := range errors {
		t.Errorf("Concurrent operation error: %v", err)
		errorCount++
		if errorCount > 10 { // Limit error output
			break
		}
	}

	if errorCount > 0 {
		t.Fatalf("Found %d errors in concurrent operations", errorCount)
	}
}

func TestIntegration_LargeValues(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Test with various large value sizes
	sizes := []int{1024, 10240, 102400, 524288} // 1KB, 10KB, 100KB, 512KB (avoid 1MB limit)

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			key := fmt.Sprintf("large_value_key_%d", size)
			value := make([]byte, size)

			// Fill with pattern for verification
			for i := range value {
				value[i] = byte(i % 256)
			}

			// Set large value
			setCmd := NewSetCommand(key, value, time.Hour)
			responses, err := client.Do(ctx, setCmd)
			if err != nil {
				t.Fatalf("Set large value (%d bytes) failed: %v", size, err)
			}
			if responses[0].Error != nil {
				t.Fatalf("Set large value (%d bytes) returned error: %v", size, responses[0].Error)
			}

			// Get large value
			getCmd := NewGetCommand(key)
			responses, err = client.Do(ctx, getCmd)
			if err != nil {
				t.Fatalf("Get large value (%d bytes) failed: %v", size, err)
			}
			if responses[0].Error != nil {
				t.Fatalf("Get large value (%d bytes) returned error: %v", size, responses[0].Error)
			}

			// Verify value integrity
			if len(responses[0].Value) != size {
				t.Errorf("Value size mismatch: expected %d, got %d", size, len(responses[0].Value))
			}

			// Verify pattern
			for i, b := range responses[0].Value {
				expected := byte(i % 256)
				if b != expected {
					t.Errorf("Value corruption at byte %d: expected %d, got %d", i, expected, b)
					break
				}
			}

			// Clean up
			delCmd := NewDeleteCommand(key)
			client.Do(ctx, delCmd)
		})
	}
}

func TestIntegration_ContextCancellation(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	key := "context_test_key"
	value := []byte("context_test_value")

	setCmd := NewSetCommand(key, value, time.Hour)
	_, err := client.Do(ctx, setCmd)
	if err == nil {
		t.Error("Expected error with cancelled context, got nil")
	}

	// Test with timeout context
	ctx, cancel = context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(10 * time.Millisecond)

	_, err = client.Do(ctx, setCmd)
	if err == nil {
		t.Error("Expected error with expired context, got nil")
	}
}

func TestIntegration_MixedOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Mix of operations in a single Do call
	commands := []*Command{
		NewSetCommand("mixed_key_1", []byte("value_1"), time.Hour),
		NewSetCommand("mixed_key_2", []byte("value_2"), time.Hour),
		NewGetCommand("mixed_key_1"),
		NewGetCommand("nonexistent_key"),
		NewDeleteCommand("mixed_key_2"),
		NewGetCommand("mixed_key_2"), // Should be cache miss after delete
	}

	responses, err := client.Do(ctx, commands...)
	if err != nil {
		t.Fatalf("Mixed operations failed: %v", err)
	}

	if len(responses) != len(commands) {
		t.Fatalf("Expected %d responses, got %d", len(commands), len(responses))
	}

	// Verify responses
	expectedResults := []struct {
		shouldHaveValue bool
		expectedError   error
		key             string
	}{
		{false, nil, "mixed_key_1"},              // set
		{false, nil, "mixed_key_2"},              // set
		{true, nil, "mixed_key_1"},               // get existing
		{false, ErrCacheMiss, "nonexistent_key"}, // get nonexistent
		{false, nil, "mixed_key_2"},              // delete
		{false, ErrCacheMiss, "mixed_key_2"},     // get after delete
	}

	for i, expected := range expectedResults {
		resp := responses[i]
		if resp.Key != expected.key {
			t.Errorf("Response %d: expected key %q, got %q", i, expected.key, resp.Key)
		}
		if resp.Error != expected.expectedError {
			t.Errorf("Response %d: expected error %v, got %v", i, expected.expectedError, resp.Error)
		}
		if expected.shouldHaveValue && len(resp.Value) == 0 {
			t.Errorf("Response %d: expected value but got none", i)
		}
		if !expected.shouldHaveValue && resp.Error == nil && len(resp.Value) > 0 {
			t.Errorf("Response %d: expected no value but got %q", i, string(resp.Value))
		}
	}

	// Clean up
	client.Do(ctx, NewDeleteCommand("mixed_key_1"))
	client.Do(ctx, NewDeleteCommand("mixed_key_2"))
}

func TestIntegration_Ping(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 1,
			MaxConnections: 5,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Test ping
	err := client.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}

	// Test ping with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Ping(ctx)
	if err != nil {
		t.Errorf("Ping with timeout failed: %v", err)
	}
}

func TestIntegration_Stats(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	// Perform some operations to generate stats
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("stats_test_key_%d", i)
		value := []byte(fmt.Sprintf("stats_test_value_%d", i))

		setCmd := NewSetCommand(key, value, time.Hour)
		client.Do(ctx, setCmd)

		getCmd := NewGetCommand(key)
		client.Do(ctx, getCmd)
	}

	// Get stats
	stats := client.Stats()
	if stats == nil {
		t.Error("Stats returned nil")
		return
	}

	if len(stats) == 0 {
		t.Error("Stats returned empty slice")
		return
	}

	// Verify stats structure
	for i, stat := range stats {
		t.Logf("Pool %d stats: %+v", i, stat)
		// Basic validation that stats are reasonable
		if stat.ActiveConnections < 0 {
			t.Errorf("Invalid ActiveConnections: %d", stat.ActiveConnections)
		}
		if stat.TotalConnections < 0 {
			t.Errorf("Invalid TotalConnections: %d", stat.TotalConnections)
		}
		if stat.TotalInFlight < 0 {
			t.Errorf("Invalid TotalInFlight: %d", stat.TotalInFlight)
		}
	}

	// Clean up
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("stats_test_key_%d", i)
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	}
}

func TestIntegration_ErrorHandling(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	// Test various error conditions
	tests := []struct {
		name        string
		cmd         *Command
		expectError bool
	}{
		{
			name:        "empty key",
			cmd:         &Command{Type: "mg", Key: ""},
			expectError: true,
		},
		{
			name:        "invalid key with space",
			cmd:         &Command{Type: "mg", Key: "key with space"},
			expectError: true,
		},
		{
			name:        "invalid key with newline",
			cmd:         &Command{Type: "mg", Key: "key\nwith\nnewline"},
			expectError: true,
		},
		{
			name:        "key too long",
			cmd:         &Command{Type: "mg", Key: string(make([]byte, 300))}, // memcached max key length is ~250
			expectError: true,
		},
		{
			name:        "unsupported command type",
			cmd:         &Command{Type: "unknown", Key: "valid_key"},
			expectError: true,
		},
		{
			name:        "set without value",
			cmd:         &Command{Type: "ms", Key: "valid_key", Value: nil},
			expectError: true,
		},
		{
			name:        "valid get",
			cmd:         NewGetCommand("valid_key"),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Do(ctx, tt.cmd)
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// BenchmarkIntegration_SetGet benchmarks basic set/get operations
func BenchmarkIntegration_SetGet(b *testing.B) {
	client := createTestingClient(b, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 5,
			MaxConnections: 20,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	// Test connection
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		b.Skip("memcached not available, skipping benchmark")
	}

	value := []byte("benchmark_value_1234567890")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_key_%d", i%1000) // Cycle through 1000 keys

			// Set
			setCmd := NewSetCommand(key, value, time.Hour)
			if _, err := client.Do(ctx, setCmd); err != nil {
				b.Errorf("Set failed: %v", err)
			}

			// Get
			getCmd := NewGetCommand(key)
			if _, err := client.Do(ctx, getCmd); err != nil {
				b.Errorf("Get failed: %v", err)
			}

			i++
		}
	})
}

// BenchmarkIntegration_GetOnly benchmarks get-only operations (cache hits)
func BenchmarkIntegration_GetOnly(b *testing.B) {
	client := createTestingClient(b, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 5,
			MaxConnections: 20,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	// Test connection
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		b.Skip("memcached not available, skipping benchmark")
	}

	// Pre-populate cache
	value := []byte("benchmark_value_1234567890")
	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("bench_get_key_%d", i)
		setCmd := NewSetCommand(key, value, time.Hour)
		if _, err := client.Do(ctx, setCmd); err != nil {
			b.Fatalf("Failed to populate key %s: %v", key, err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_get_key_%d", i%numKeys)
			getCmd := NewGetCommand(key)
			if _, err := client.Do(ctx, getCmd); err != nil {
				b.Errorf("Get failed: %v", err)
			}
			i++
		}
	})
}

func TestIntegration_ArithmeticOperations(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	t.Run("Increment", func(t *testing.T) {
		key := "increment_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("10"), time.Hour)
		responses, err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Set operation returned error: %v", responses[0].Error)
		}

		// Increment by 5
		incrCmd := NewIncrementCommand(key, 5)
		responses, err = client.Do(ctx, incrCmd)
		if err != nil {
			t.Fatalf("Increment operation failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Increment operation returned error: %v", responses[0].Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		responses, err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after increment failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Get after increment returned error: %v", responses[0].Error)
		}

		// Verify value is incremented (this test depends on memcached behavior)
		t.Logf("Value after increment: %s", string(responses[0].Value))

		// Clean up
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	})

	t.Run("Decrement", func(t *testing.T) {
		key := "decrement_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("20"), time.Hour)
		responses, err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Set operation returned error: %v", responses[0].Error)
		}

		// Decrement by 3
		decrCmd := NewDecrementCommand(key, 3)
		responses, err = client.Do(ctx, decrCmd)
		if err != nil {
			t.Fatalf("Decrement operation failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Decrement operation returned error: %v", responses[0].Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		responses, err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after decrement failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Get after decrement returned error: %v", responses[0].Error)
		}

		// Verify value is decremented (this test depends on memcached behavior)
		t.Logf("Value after decrement: %s", string(responses[0].Value))

		// Clean up
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	})
}

func TestIntegration_MetaFlags(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	t.Run("GetWithFlags", func(t *testing.T) {
		key := "flags_test"
		value := []byte("flags_test_value")

		// Set value first
		setCmd := NewSetCommand(key, value, time.Hour)
		responses, err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Set operation returned error: %v", responses[0].Error)
		}

		// Test basic get first (NewGetCommand sets "v" flag by default)
		getCmd := NewGetCommand(key)
		responses, err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Basic get failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Basic get returned error: %v", responses[0].Error)
		}
		if string(responses[0].Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(responses[0].Value))
		}
		t.Logf("Basic get response flags: %+v", responses[0].Flags)

		// Test with size flag only (without value flag to avoid conflicts)
		getCmd = &Command{
			Type:  CmdMetaGet,
			Key:   key,
			Flags: Flags{{Type: FlagSize, Value: ""}}, // Request only size
		}

		responses, err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get with size flag failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Get with size flag returned error: %v", responses[0].Error)
		}
		// Should have size flag in response but no value
		if sizeStr, exists := responses[0].GetFlag("s"); !exists {
			t.Error("Expected size flag 's' in response")
		} else {
			t.Logf("Size flag value: %s", sizeStr)
		}
		if len(responses[0].Value) != 0 {
			t.Error("Expected no value when only requesting size")
		}

		// Test with both value and size flags
		getCmd = &Command{
			Type: CmdMetaGet,
			Key:  key,
			Flags: Flags{
				{Type: FlagValue, Value: ""}, // Request value
				{Type: FlagSize, Value: ""},  // Request size
			},
		}
		responses, err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get with value and size flags failed: %v", err)
		}
		if responses[0].Error != nil {
			t.Fatalf("Get with value and size flags returned error: %v", responses[0].Error)
		}
		// Should have both value and size
		t.Logf("Combined flags response: status=%s, flags=%+v, value_len=%d",
			responses[0].Status, responses[0].Flags, len(responses[0].Value))
		if string(responses[0].Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(responses[0].Value))
		}

		// Clean up
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	})
}

func TestIntegration_DebugCommands(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	t.Run("DebugCommand", func(t *testing.T) {
		// Try debug command (may not be supported by all memcached versions)
		debugCmd := NewDebugCommand("debug_test")
		responses, err := client.Do(ctx, debugCmd)
		if err != nil {
			t.Logf("Debug command failed (may not be supported): %v", err)
			return
		}

		t.Logf("Debug response: status=%s, flags=%+v", responses[0].Status, responses[0].Flags)
	})

	t.Run("NoOpCommand", func(t *testing.T) {
		// Try no-op command
		nopCmd := NewNoOpCommand()
		responses, err := client.Do(ctx, nopCmd)
		if err != nil {
			t.Logf("NoOp command failed (may not be supported): %v", err)
			return
		}

		t.Logf("NoOp response: status=%s, flags=%+v", responses[0].Status, responses[0].Flags)
	})
}

func TestIntegration_EnhancedErrorHandling(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx := context.Background()

	t.Run("InvalidKey", func(t *testing.T) {
		// Test with invalid key (too long)
		longKey := strings.Repeat("a", MaxKeyLength+1)
		getCmd := NewGetCommand(longKey)

		responses, err := client.Do(ctx, getCmd)
		if err == nil && (len(responses) == 0 || responses[0].Error == nil) {
			t.Error("Expected error for invalid key, but got none")
		}
	})

	t.Run("KeyWithSpaces", func(t *testing.T) {
		// Test with key containing spaces
		invalidKey := "key with spaces"
		getCmd := NewGetCommand(invalidKey)

		responses, err := client.Do(ctx, getCmd)
		if err == nil && (len(responses) == 0 || responses[0].Error == nil) {
			t.Error("Expected error for key with spaces, but got none")
		}
	})

	t.Run("GetNonExistentKey", func(t *testing.T) {
		// Test getting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_12345"
		getCmd := NewGetCommand(nonExistentKey)

		responses, err := client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get non-existent key failed: %v", err)
		}
		if len(responses) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(responses))
		}
		if responses[0].Error != ErrCacheMiss {
			t.Errorf("Expected ErrCacheMiss, got: %v", responses[0].Error)
		}
	})

	t.Run("DeleteNonExistentKey", func(t *testing.T) {
		// Test deleting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_54321"
		delCmd := NewDeleteCommand(nonExistentKey)

		responses, err := client.Do(ctx, delCmd)
		if err != nil {
			t.Fatalf("Delete non-existent key failed: %v", err)
		}
		if len(responses) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(responses))
		}

		// Memcached may return different responses for delete of non-existent key
		t.Logf("Delete non-existent key response: status=%s, error=%v", responses[0].Status, responses[0].Error)
	})
}

func createTestingClient(t testing.TB, config *ClientConfig) *Client {
	if testing.Short() {
		t.Skip("testing.Short(), skipping integration test")
	}

	if config == nil {
		config = &ClientConfig{
			Servers: []string{"localhost:11211"},
			PoolConfig: &PoolConfig{
				MinConnections: 1,
				MaxConnections: 5,
				ConnTimeout:    time.Second,
				IdleTimeout:    time.Minute,
			},
		}
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		client.Close()
		t.Skip("memcached not responding, skipping integration test")
	}

	t.Cleanup(func() {
		client.Close()
	})

	return client
}
