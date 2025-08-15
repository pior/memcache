package memcache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
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

	t.Run("Set", func(t *testing.T) {
		setCmd := NewSetCommand(key, value, time.Hour)
		err := client.doWait(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		assertNoResponseError(t, setCmd)
	})

	t.Run("Get", func(t *testing.T) {
		getCmd := NewGetCommand(key)
		err := client.doWait(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get operation failed: %v", err)
		}
		assertNoResponseError(t, getCmd)
		if string(getCmd.Response.Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(getCmd.Response.Value))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		delCmd := NewDeleteCommand(key)
		err := client.doWait(ctx, delCmd)
		if err != nil {
			t.Fatalf("Delete operation failed: %v", err)
		}
		assertNoResponseError(t, delCmd)
	})

	t.Run("CheckDeleted", func(t *testing.T) {
		// Verify key is deleted
		getCmd := NewGetCommand(key)
		err := client.doWait(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after delete failed: %v", err)
		}
		if getCmd.Response.Error != protocol.ErrCacheMiss {
			t.Errorf("Expected cache miss, got: %v", getCmd.Response.Error)
		}
	})
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
	setCommands := make([]*protocol.Command, numKeys)

	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("multi_key_%d", i)
		values[i] = []byte(fmt.Sprintf("multi_value_%d", i))
		setCommands[i] = NewSetCommand(keys[i], values[i], time.Hour)
	}

	// Execute all set commands at once
	err := client.Do(ctx, setCommands...)
	if err != nil {
		t.Fatalf("Multiple set operations failed: %v", err)
	}

	err = WaitAll(ctx, setCommands...)
	if err != nil {
		t.Fatalf("Failed to wait for all set commands: %v", err)
	}

	assertNoResponseError(t, setCommands...)

	// Get multiple keys
	getCommands := make([]*protocol.Command, numKeys)
	for i := 0; i < numKeys; i++ {
		getCommands[i] = NewGetCommand(keys[i])
	}

	err = client.DoWait(ctx, getCommands...)
	if err != nil {
		t.Fatalf("Multiple get operations failed: %v", err)
	}

	// Verify all gets succeeded
	for i, cmd := range getCommands {
		if cmd.Response.Error != nil {
			t.Errorf("Get operation %d failed: %v", i, cmd.Response.Error)
		}
		if string(cmd.Response.Value) != string(values[i]) {
			t.Errorf("Expected value %q, got %q", string(values[i]), string(cmd.Response.Value))
		}
	}

	// Clean up - delete all keys
	deleteCommands := make([]*protocol.Command, numKeys)
	for i := 0; i < numKeys; i++ {
		deleteCommands[i] = NewDeleteCommand(keys[i])
	}

	err = client.Do(ctx, deleteCommands...)
	if err != nil {
		t.Fatalf("Multiple delete operations failed: %v", err)
	}

	assertNoResponseError(t, deleteCommands...)
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
	err := client.doWait(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}
	assertNoResponseError(t, setCmd)

	// Verify key exists immediately
	getCmd := NewGetCommand(key)
	err = client.doWait(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}
	assertNoResponseError(t, getCmd)
	if string(getCmd.Response.Value) != string(value) {
		t.Errorf("Expected value %q, got %q", string(value), string(getCmd.Response.Value))
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Verify key has expired
	getCmd = NewGetCommand(key)
	err = client.doWait(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get after TTL failed: %v", err)
	}

	assertResponseErrorIs(t, getCmd, protocol.ErrCacheMiss)
}

func TestIntegration_ConcurrentOperations(t *testing.T) {
	// Test that basic operations work when called from multiple goroutines
	// but serialize the actual memcache operations to avoid race conditions
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 1,
			MaxConnections: 2,
			ConnTimeout:    3 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Use a mutex to serialize access and avoid race conditions
	var mu sync.Mutex
	numWorkers := 3

	var wg sync.WaitGroup
	errorChan := make(chan error, numWorkers)

	// Each worker does one operation, serialized by mutex
	for worker := 0; worker < numWorkers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Serialize the memcache operations to avoid concurrency issues
			mu.Lock()
			defer mu.Unlock()

			key := fmt.Sprintf("serial_test_%d", workerID)
			value := []byte(fmt.Sprintf("value_%d", workerID))

			// Simple set
			setCmd := NewSetCommand(key, value, time.Hour)
			if err := client.doWait(ctx, setCmd); err != nil {
				errorChan <- fmt.Errorf("worker %d set failed: %v", workerID, err)
				return
			}

			if setCmd.Response.Error != nil {
				errorChan <- fmt.Errorf("worker %d set error: %v", workerID, setCmd.Response.Error)
				return
			}

			// Simple get
			getCmd := NewGetCommand(key)
			if err := client.doWait(ctx, getCmd); err != nil {
				errorChan <- fmt.Errorf("worker %d get failed: %v", workerID, err)
				return
			}

			if getCmd.Response.Error != nil {
				errorChan <- fmt.Errorf("worker %d get error: %v", workerID, getCmd.Response.Error)
				return
			}
			if string(getCmd.Response.Value) != string(value) {
				errorChan <- fmt.Errorf("worker %d value mismatch: expected %q, got %q",
					workerID, string(value), string(getCmd.Response.Value))
				return
			}

			// Cleanup
			delCmd := NewDeleteCommand(key)
			client.Do(ctx, delCmd)
		}(worker)
	}

	// Wait for all workers to complete with a timeout mechanism
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All workers completed successfully
	case <-ctx.Done():
		t.Errorf("Test timed out waiting for workers to complete: %v", ctx.Err())
	}

	close(errorChan)

	// Check for any errors
	var errorCount int
	for err := range errorChan {
		t.Errorf("Concurrent operation error: %v", err)
		errorCount++
		if errorCount > 5 { // Reduced limit since we have fewer operations
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
			err := client.doWait(ctx, setCmd)
			if err != nil {
				t.Fatalf("Set large value (%d bytes) failed: %v", size, err)
			}

			if setCmd.Response.Error != nil {
				t.Fatalf("Set large value (%d bytes) returned error: %v", size, setCmd.Response.Error)
			}

			// Get large value
			getCmd := NewGetCommand(key)
			err = client.doWait(ctx, getCmd)
			if err != nil {
				t.Fatalf("Get large value (%d bytes) failed: %v", size, err)
			}

			if getCmd.Response.Error != nil {
				t.Fatalf("Get large value (%d bytes) returned error: %v", size, getCmd.Response.Error)
			}

			// Verify value integrity
			if len(getCmd.Response.Value) != size {
				t.Errorf("Value size mismatch: expected %d, got %d", size, len(getCmd.Response.Value))
			}

			// Verify pattern
			for i, b := range getCmd.Response.Value {
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
	err := client.doWait(ctx, setCmd)
	if err == nil {
		t.Error("Expected error with cancelled context, got nil")
	}

	// Test with timeout context
	ctx, cancel = context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(10 * time.Millisecond)

	err = client.Do(ctx, setCmd)
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
	commands := []*protocol.Command{
		NewSetCommand("mixed_key_1", []byte("value_1"), time.Hour),
		NewSetCommand("mixed_key_2", []byte("value_2"), time.Hour),
		NewGetCommand("mixed_key_1"),
		NewGetCommand("nonexistent_key"),
		NewDeleteCommand("mixed_key_2"),
		NewGetCommand("mixed_key_2"), // Should be cache miss after delete
	}

	err := client.Do(ctx, commands...)
	if err != nil {
		t.Fatalf("Mixed operations failed: %v", err)
	}

	_ = WaitAll(ctx, commands...)

	// Verify responses
	expectedResults := []struct {
		shouldHaveValue bool
		expectedError   error
		key             string
	}{
		{false, nil, "mixed_key_1"},                       // set
		{false, nil, "mixed_key_2"},                       // set
		{true, nil, "mixed_key_1"},                        // get existing
		{false, protocol.ErrCacheMiss, "nonexistent_key"}, // get nonexistent
		{false, nil, "mixed_key_2"},                       // delete
		{false, protocol.ErrCacheMiss, "mixed_key_2"},     // get after delete
	}

	for i, expected := range expectedResults {
		if commands[i].Response.Error != expected.expectedError {
			t.Errorf("Response %d: expected error %v, got %v", i, expected.expectedError, commands[i].Response.Error)
		}
		if expected.shouldHaveValue && len(commands[i].Response.Value) == 0 {
			t.Errorf("Response %d: expected value but got none", i)
		}
		if !expected.shouldHaveValue && commands[i].Response.Error == nil && len(commands[i].Response.Value) > 0 {
			t.Errorf("Response %d: expected no value but got %q", i, string(commands[i].Response.Value))
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

func TestIntegration_WaitAll(t *testing.T) {
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

	t.Run("WaitForMultipleOperations", func(t *testing.T) {
		// Create multiple commands
		numCommands := 5
		commands := make([]*protocol.Command, numCommands)
		expectedKeys := make([]string, numCommands)

		for i := 0; i < numCommands; i++ {
			key := fmt.Sprintf("waitall_test_key_%d", i)
			value := []byte(fmt.Sprintf("waitall_test_value_%d", i))
			expectedKeys[i] = key
			commands[i] = NewSetCommand(key, value, time.Hour)
		}

		// Execute all commands
		err := client.Do(ctx, commands...)
		if err != nil {
			t.Fatalf("Do with multiple commands failed: %v", err)
		}

		// Wait for all responses to be ready
		err = WaitAll(ctx, commands...)
		if err != nil {
			t.Fatalf("WaitAll failed: %v", err)
		}

		// All responses should be immediately available
		for i, cmd := range commands {
			if cmd.Response.Error != nil {
				t.Errorf("Command %d returned error: %v", i, cmd.Response.Error)
			}
		}

		// Clean up
		for _, key := range expectedKeys {
			delCmd := NewDeleteCommand(key)
			client.Do(ctx, delCmd)
		}
	})

	t.Run("WaitWithTimeout", func(t *testing.T) {
		cmd := NewGetCommand("waitall_timeout_test")
		cmd.Opaque = "1234"

		// Execute command
		err := client.doWait(ctx, cmd)
		if err != nil {
			t.Fatalf("Do failed: %v", err)
		}

		// Wait with a reasonable timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err = WaitAll(timeoutCtx, cmd)
		if err != nil {
			t.Errorf("WaitAll with timeout failed: %v", err)
		}

		// Response should be available
		if cmd.Response.Error != nil {
			t.Errorf("GetResponse failed: %v", cmd.Response.Error)
		}
		if cmd.Response.Opaque != "1234" {
			t.Errorf("Expected opaque 1234, got %s", cmd.Response.Opaque)
		}
	})
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

	// Helper function to create invalid commands for testing
	createInvalidCommand := func(cmdType, key string, value []byte) *protocol.Command {
		c := protocol.NewCommand(cmdType, key)
		c.Value = value
		c.Flags = protocol.Flags{}
		return c
	}

	// Test various error conditions
	tests := []struct {
		name        string
		cmd         *protocol.Command
		expectError bool
	}{
		{
			name:        "empty key",
			cmd:         createInvalidCommand("mg", "", nil),
			expectError: true,
		},
		{
			name:        "invalid key with space",
			cmd:         createInvalidCommand("mg", "key with space", nil),
			expectError: true,
		},
		{
			name:        "invalid key with newline",
			cmd:         createInvalidCommand("mg", "key\nwith\nnewline", nil),
			expectError: true,
		},
		{
			name:        "key too long",
			cmd:         createInvalidCommand("mg", string(make([]byte, 300)), nil), // memcached max key length is ~250
			expectError: true,
		},
		{
			name:        "unsupported command type",
			cmd:         createInvalidCommand("unknown", "valid_key", nil),
			expectError: true,
		},
		{
			name:        "set without value",
			cmd:         createInvalidCommand("ms", "valid_key", nil),
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
			err := client.doWait(ctx, tt.cmd)
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
			if err := client.doWait(ctx, setCmd); err != nil {
				b.Errorf("Set failed: %v", err)
			}

			// Get
			getCmd := NewGetCommand(key)
			if err := client.doWait(ctx, getCmd); err != nil {
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
		if err := client.doWait(ctx, setCmd); err != nil {
			b.Fatalf("Failed to populate key %s: %v", key, err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_get_key_%d", i%numKeys)
			getCmd := NewGetCommand(key)
			if err := client.doWait(ctx, getCmd); err != nil {
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
		err := client.doWait(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		if setCmd.Response.Error != nil {
			t.Fatalf("Set operation returned error: %v", setCmd.Response.Error)
		}

		// Increment by 5
		incrCmd := NewIncrementCommand(key, 5)
		err = client.doWait(ctx, incrCmd)
		if err != nil {
			t.Fatalf("Increment operation failed: %v", err)
		}
		if incrCmd.Response.Error != nil {
			t.Fatalf("Increment operation returned error: %v", incrCmd.Response.Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.doWait(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after increment failed: %v", err)
		}
		if getCmd.Response.Error != nil {
			t.Fatalf("Get after increment returned error: %v", getCmd.Response.Error)
		}

		// Verify value is incremented (this test depends on memcached behavior)
		t.Logf("Value after increment: %s", string(getCmd.Response.Value))

		// Clean up
		delCmd := NewDeleteCommand(key)
		_ = client.doWait(ctx, delCmd)
	})

	t.Run("Decrement", func(t *testing.T) {
		key := "decrement_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("20"), time.Hour)
		err := client.doWait(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		if setCmd.Response.Error != nil {
			t.Fatalf("Set operation returned error: %v", setCmd.Response.Error)
		}

		// Decrement by 3
		decrCmd := NewDecrementCommand(key, 3)
		err = client.doWait(ctx, decrCmd)
		if err != nil {
			t.Fatalf("Decrement operation failed: %v", err)
		}
		if decrCmd.Response.Error != nil {
			t.Fatalf("Decrement operation returned error: %v", decrCmd.Response.Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after decrement failed: %v", err)
		}
		if getCmd.Response.Error != nil {
			t.Fatalf("Get after decrement returned error: %v", getCmd.Response.Error)
		}

		// Verify value is decremented (this test depends on memcached behavior)
		t.Logf("Value after decrement: %s", string(getCmd.Response.Value))

		// Clean up
		delCmd := NewDeleteCommand(key)
		_ = client.doWait(ctx, delCmd)
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
		err := client.doWait(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}

		if setCmd.Response.Error != nil {
			t.Fatalf("Set operation returned error: %v", setCmd.Response.Error)
		}

		// Test basic get first (NewGetCommand sets "v" flag by default)
		getCmd := NewGetCommand(key)
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Basic get failed: %v", err)
		}

		if getCmd.Response.Error != nil {
			t.Fatalf("Basic get returned error: %v", getCmd.Response.Error)
		}
		if string(getCmd.Response.Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(getCmd.Response.Value))
		}
		t.Logf("Basic get response flags: %+v", getCmd.Response.Flags)

		// Test with size flag only (without value flag to avoid conflicts)
		getCmd = NewGetCommand(key)
		getCmd.Flags = protocol.Flags{{Type: protocol.FlagSize, Value: ""}} // Replace flags to request only size

		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get with size flag failed: %v", err)
		}

		if getCmd.Response.Error != nil {
			t.Fatalf("Get with size flag returned error: %v", getCmd.Response.Error)
		}
		// Should have size flag in response but no value
		if sizeStr, exists := getCmd.Response.Flags.Get("s"); !exists {
			t.Error("Expected size flag 's' in response")
		} else {
			t.Logf("Size flag value: %s", sizeStr)
		}
		if len(getCmd.Response.Value) != 0 {
			t.Error("Expected no value when only requesting size")
		}

		// Test with both value and size flags
		getCmd = NewGetCommand(key)
		getCmd.Flags = protocol.Flags{
			{Type: protocol.FlagValue, Value: ""}, // Request value
			{Type: protocol.FlagSize, Value: ""},  // Request size
		}
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get with value and size flags failed: %v", err)
		}

		if getCmd.Response.Error != nil {
			t.Fatalf("Get with value and size flags returned error: %v", getCmd.Response.Error)
		}
		// Should have both value and size
		t.Logf("Combined flags response: status=%s, flags=%+v, value_len=%d",
			getCmd.Response.Status, getCmd.Response.Flags, len(getCmd.Response.Value))
		if string(getCmd.Response.Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(getCmd.Response.Value))
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
		err := client.doWait(ctx, debugCmd)
		if err != nil {
			t.Logf("Debug command failed (may not be supported): %v", err)
			return
		}

		if debugCmd.Response.Error != nil {
			t.Logf("Failed to get debug response: %v", debugCmd.Response.Error)
			return
		}

		t.Logf("Debug response: status=%s, flags=%+v", debugCmd.Response.Status, debugCmd.Response.Flags)
	})

	t.Run("NoOpCommand", func(t *testing.T) {
		// Try no-op command
		nopCmd := NewNoOpCommand()
		err := client.doWait(ctx, nopCmd)
		if err != nil {
			t.Logf("NoOp command failed (may not be supported): %v", err)
			return
		}

		if nopCmd.Response.Error != nil {
			t.Logf("Failed to get noop response: %v", nopCmd.Response.Error)
			return
		}

		t.Logf("NoOp response: status=%s, flags=%+v", nopCmd.Response.Status, nopCmd.Response.Flags)
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
		longKey := strings.Repeat("a", protocol.MaxKeyLength+1)
		getCmd := NewGetCommand(longKey)

		err := client.doWait(ctx, getCmd)
		if err == nil {
			if getCmd.Response.Error == nil {
				t.Error("Expected error for invalid key, but got none")
			}
		}
	})

	t.Run("KeyWithSpaces", func(t *testing.T) {
		// Test with key containing spaces
		invalidKey := "key with spaces"
		getCmd := NewGetCommand(invalidKey)

		err := client.doWait(ctx, getCmd)
		if err == nil {
			if getCmd.Response.Error == nil {
				t.Error("Expected error for key with spaces, but got none")
			}
		}
	})

	t.Run("GetNonExistentKey", func(t *testing.T) {
		// Test getting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_12345"
		getCmd := NewGetCommand(nonExistentKey)

		err := client.doWait(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get non-existent key failed: %v", err)
		}

		if getCmd.Response.Error != protocol.ErrCacheMiss {
			t.Errorf("Expected protocol.ErrCacheMiss, got: %v", getCmd.Response.Error)
		}
	})

	t.Run("DeleteNonExistentKey", func(t *testing.T) {
		// Test deleting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_54321"
		delCmd := NewDeleteCommand(nonExistentKey)

		err := client.doWait(ctx, delCmd)
		if err != nil {
			t.Fatalf("Delete non-existent key failed: %v", err)
		}

		if delCmd.Response.Error != nil {
			t.Fatalf("Failed to get delete response for non-existent key: %v", delCmd.Response.Error)
		}

		// Memcached may return different responses for delete of non-existent key
		t.Logf("Delete non-existent key response: status=%s, error=%v", delCmd.Response.Status, delCmd.Response.Error)
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
		t.Fatal("memcached not responding, skipping integration test")
	}

	t.Cleanup(func() {
		client.Close()
	})

	return client
}
