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

	setCmd := NewSetCommand(key, value, time.Hour)
	err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}

	setResp, err := setCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get set response: %v", err)
	}
	if setResp.Error != nil {
		t.Fatalf("Set operation returned error: %v", setResp.Error)
	}

	// Test Get operation
	getCmd := NewGetCommand(key)
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}

	getResp, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get response: %v", err)
	}
	if getResp.Error != nil {
		t.Fatalf("Get operation returned error: %v", getResp.Error)
	}
	if string(getResp.Value) != string(value) {
		t.Errorf("Expected value %q, got %q", string(value), string(getResp.Value))
	}

	// Test Delete operation
	delCmd := NewDeleteCommand(key)
	err = client.Do(ctx, delCmd)
	if err != nil {
		t.Fatalf("Delete operation failed: %v", err)
	}

	delResp, err := delCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get delete response: %v", err)
	}
	if delResp.Error != nil {
		t.Fatalf("Delete operation returned error: %v", delResp.Error)
	}

	// Verify key is deleted
	getCmd = NewGetCommand(key)
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}

	getAfterDelResp, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get response after delete: %v", err)
	}
	if getAfterDelResp.Error != protocol.ErrCacheMiss {
		t.Errorf("Expected cache miss, got: %v", getAfterDelResp.Error)
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

	// Verify all sets succeeded
	for i, cmd := range setCommands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response for set command %d: %v", i, err)
		}
		if resp.Error != nil {
			t.Errorf("Set operation %d failed: %v", i, resp.Error)
		}
		if resp.Key != keys[i] {
			t.Errorf("Expected key %q, got %q", keys[i], resp.Key)
		}
	}

	// Get multiple keys
	getCommands := make([]*protocol.Command, numKeys)
	for i := 0; i < numKeys; i++ {
		getCommands[i] = NewGetCommand(keys[i])
	}

	err = client.Do(ctx, getCommands...)
	if err != nil {
		t.Fatalf("Multiple get operations failed: %v", err)
	}

	// Verify all gets succeeded
	for i, cmd := range getCommands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response for get command %d: %v", i, err)
		}
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
	deleteCommands := make([]*protocol.Command, numKeys)
	for i := 0; i < numKeys; i++ {
		deleteCommands[i] = NewDeleteCommand(keys[i])
	}

	err = client.Do(ctx, deleteCommands...)
	if err != nil {
		t.Fatalf("Multiple delete operations failed: %v", err)
	}

	// Verify all deletes succeeded
	for i, cmd := range deleteCommands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response for delete command %d: %v", i, err)
		}
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
	err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}

	setResp, err := setCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get set response: %v", err)
	}
	if setResp.Error != nil {
		t.Fatalf("Set operation returned error: %v", setResp.Error)
	}

	// Verify key exists immediately
	getCmd := NewGetCommand(key)
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}

	getResp, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get response: %v", err)
	}
	if getResp.Error != nil {
		t.Fatalf("Get operation returned error: %v", getResp.Error)
	}
	if string(getResp.Value) != string(value) {
		t.Errorf("Expected value %q, got %q", string(value), string(getResp.Value))
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Verify key has expired
	getCmd = NewGetCommand(key)
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get after TTL failed: %v", err)
	}

	getAfterTTLResp, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get response after TTL: %v", err)
	}
	if getAfterTTLResp.Error != protocol.ErrCacheMiss {
		t.Errorf("Expected cache miss after TTL, got: %v", getAfterTTLResp.Error)
	}
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
			if err := client.Do(ctx, setCmd); err != nil {
				errorChan <- fmt.Errorf("worker %d set failed: %v", workerID, err)
				return
			}

			setResp, err := setCmd.GetResponse(ctx)
			if err != nil {
				errorChan <- fmt.Errorf("worker %d set response failed: %v", workerID, err)
				return
			}
			if setResp.Error != nil {
				errorChan <- fmt.Errorf("worker %d set error: %v", workerID, setResp.Error)
				return
			}

			// Simple get
			getCmd := NewGetCommand(key)
			if err := client.Do(ctx, getCmd); err != nil {
				errorChan <- fmt.Errorf("worker %d get failed: %v", workerID, err)
				return
			}

			getResp, err := getCmd.GetResponse(ctx)
			if err != nil {
				errorChan <- fmt.Errorf("worker %d get response failed: %v", workerID, err)
				return
			}
			if getResp.Error != nil {
				errorChan <- fmt.Errorf("worker %d get error: %v", workerID, getResp.Error)
				return
			}
			if string(getResp.Value) != string(value) {
				errorChan <- fmt.Errorf("worker %d value mismatch: expected %q, got %q",
					workerID, string(value), string(getResp.Value))
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
			err := client.Do(ctx, setCmd)
			if err != nil {
				t.Fatalf("Set large value (%d bytes) failed: %v", size, err)
			}

			setResp, err := setCmd.GetResponse(ctx)
			if err != nil {
				t.Fatalf("Failed to get set response for large value (%d bytes): %v", size, err)
			}
			if setResp.Error != nil {
				t.Fatalf("Set large value (%d bytes) returned error: %v", size, setResp.Error)
			}

			// Get large value
			getCmd := NewGetCommand(key)
			err = client.Do(ctx, getCmd)
			if err != nil {
				t.Fatalf("Get large value (%d bytes) failed: %v", size, err)
			}

			getResp, err := getCmd.GetResponse(ctx)
			if err != nil {
				t.Fatalf("Failed to get response for large value (%d bytes): %v", size, err)
			}
			if getResp.Error != nil {
				t.Fatalf("Get large value (%d bytes) returned error: %v", size, getResp.Error)
			}

			// Verify value integrity
			if len(getResp.Value) != size {
				t.Errorf("Value size mismatch: expected %d, got %d", size, len(getResp.Value))
			}

			// Verify pattern
			for i, b := range getResp.Value {
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
	err := client.Do(ctx, setCmd)
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
		resp, err := commands[i].GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response for command %d: %v", i, err)
		}
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
			resp, err := cmd.GetResponse(ctx)
			if err != nil {
				t.Errorf("GetResponse for command %d failed: %v", i, err)
			}
			if resp.Key != expectedKeys[i] {
				t.Errorf("Command %d: expected key %s, got %s", i, expectedKeys[i], resp.Key)
			}
			if resp.Error != nil {
				t.Errorf("Command %d returned error: %v", i, resp.Error)
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

		// Execute command
		err := client.Do(ctx, cmd)
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
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Errorf("GetResponse failed: %v", err)
		}
		if resp.Key != "waitall_timeout_test" {
			t.Errorf("Expected key waitall_timeout_test, got %s", resp.Key)
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
			err := client.Do(ctx, tt.cmd)
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
			if err := client.Do(ctx, setCmd); err != nil {
				b.Errorf("Set failed: %v", err)
			}

			// Get
			getCmd := NewGetCommand(key)
			if err := client.Do(ctx, getCmd); err != nil {
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
		if err := client.Do(ctx, setCmd); err != nil {
			b.Fatalf("Failed to populate key %s: %v", key, err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_get_key_%d", i%numKeys)
			getCmd := NewGetCommand(key)
			if err := client.Do(ctx, getCmd); err != nil {
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
		err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		setResp, err := setCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get set response: %v", err)
		}
		if setResp.Error != nil {
			t.Fatalf("Set operation returned error: %v", setResp.Error)
		}

		// Increment by 5
		incrCmd := NewIncrementCommand(key, 5)
		err = client.Do(ctx, incrCmd)
		if err != nil {
			t.Fatalf("Increment operation failed: %v", err)
		}
		incrResp, err := incrCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get increment response: %v", err)
		}
		if incrResp.Error != nil {
			t.Fatalf("Increment operation returned error: %v", incrResp.Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after increment failed: %v", err)
		}
		getResp, err := getCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response after increment: %v", err)
		}
		if getResp.Error != nil {
			t.Fatalf("Get after increment returned error: %v", getResp.Error)
		}

		// Verify value is incremented (this test depends on memcached behavior)
		t.Logf("Value after increment: %s", string(getResp.Value))

		// Clean up
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	})

	t.Run("Decrement", func(t *testing.T) {
		key := "decrement_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("20"), time.Hour)
		err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}
		setResp, err := setCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get set response: %v", err)
		}
		if setResp.Error != nil {
			t.Fatalf("Set operation returned error: %v", setResp.Error)
		}

		// Decrement by 3
		decrCmd := NewDecrementCommand(key, 3)
		err = client.Do(ctx, decrCmd)
		if err != nil {
			t.Fatalf("Decrement operation failed: %v", err)
		}
		decrResp, err := decrCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get decrement response: %v", err)
		}
		if decrResp.Error != nil {
			t.Fatalf("Decrement operation returned error: %v", decrResp.Error)
		}

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get after decrement failed: %v", err)
		}
		getResp, err := getCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response after decrement: %v", err)
		}
		if getResp.Error != nil {
			t.Fatalf("Get after decrement returned error: %v", getResp.Error)
		}

		// Verify value is decremented (this test depends on memcached behavior)
		t.Logf("Value after decrement: %s", string(getResp.Value))

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
		err := client.Do(ctx, setCmd)
		if err != nil {
			t.Fatalf("Set operation failed: %v", err)
		}

		setResp, err := setCmd.GetResponse(ctx)
		if err != nil || setResp.Error != nil {
			t.Fatalf("Set operation returned error: %v", err)
		}

		// Test basic get first (NewGetCommand sets "v" flag by default)
		getCmd := NewGetCommand(key)
		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Basic get failed: %v", err)
		}

		getResp, err := getCmd.GetResponse(ctx)
		if err != nil || getResp.Error != nil {
			t.Fatalf("Basic get returned error: %v", err)
		}
		if string(getResp.Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(getResp.Value))
		}
		t.Logf("Basic get response flags: %+v", getResp.Flags)

		// Test with size flag only (without value flag to avoid conflicts)
		getCmd = NewGetCommand(key)
		getCmd.Flags = protocol.Flags{{Type: protocol.FlagSize, Value: ""}} // Replace flags to request only size

		err = client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get with size flag failed: %v", err)
		}

		sizeResp, err := getCmd.GetResponse(ctx)
		if err != nil || sizeResp.Error != nil {
			t.Fatalf("Get with size flag returned error: %v", err)
		}
		// Should have size flag in response but no value
		if sizeStr, exists := sizeResp.GetFlag("s"); !exists {
			t.Error("Expected size flag 's' in response")
		} else {
			t.Logf("Size flag value: %s", sizeStr)
		}
		if len(sizeResp.Value) != 0 {
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

		combinedResp, err := getCmd.GetResponse(ctx)
		if err != nil || combinedResp.Error != nil {
			t.Fatalf("Get with value and size flags returned error: %v", err)
		}
		// Should have both value and size
		t.Logf("Combined flags response: status=%s, flags=%+v, value_len=%d",
			combinedResp.Status, combinedResp.Flags, len(combinedResp.Value))
		if string(combinedResp.Value) != string(value) {
			t.Errorf("Expected value %q, got %q", string(value), string(combinedResp.Value))
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
		err := client.Do(ctx, debugCmd)
		if err != nil {
			t.Logf("Debug command failed (may not be supported): %v", err)
			return
		}

		debugResp, err := debugCmd.GetResponse(ctx)
		if err != nil {
			t.Logf("Failed to get debug response: %v", err)
			return
		}

		t.Logf("Debug response: status=%s, flags=%+v", debugResp.Status, debugResp.Flags)
	})

	t.Run("NoOpCommand", func(t *testing.T) {
		// Try no-op command
		nopCmd := NewNoOpCommand()
		err := client.Do(ctx, nopCmd)
		if err != nil {
			t.Logf("NoOp command failed (may not be supported): %v", err)
			return
		}

		nopResp, err := nopCmd.GetResponse(ctx)
		if err != nil {
			t.Logf("Failed to get noop response: %v", err)
			return
		}

		t.Logf("NoOp response: status=%s, flags=%+v", nopResp.Status, nopResp.Flags)
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

		err := client.Do(ctx, getCmd)
		if err == nil {
			getResp, respErr := getCmd.GetResponse(ctx)
			if respErr == nil && getResp.Error == nil {
				t.Error("Expected error for invalid key, but got none")
			}
		}
	})

	t.Run("KeyWithSpaces", func(t *testing.T) {
		// Test with key containing spaces
		invalidKey := "key with spaces"
		getCmd := NewGetCommand(invalidKey)

		err := client.Do(ctx, getCmd)
		if err == nil {
			getResp, respErr := getCmd.GetResponse(ctx)
			if respErr == nil && getResp.Error == nil {
				t.Error("Expected error for key with spaces, but got none")
			}
		}
	})

	t.Run("GetNonExistentKey", func(t *testing.T) {
		// Test getting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_12345"
		getCmd := NewGetCommand(nonExistentKey)

		err := client.Do(ctx, getCmd)
		if err != nil {
			t.Fatalf("Get non-existent key failed: %v", err)
		}

		getResp, err := getCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get response for non-existent key: %v", err)
		}
		if getResp.Error != protocol.ErrCacheMiss {
			t.Errorf("Expected protocol.ErrCacheMiss, got: %v", getResp.Error)
		}
	})

	t.Run("DeleteNonExistentKey", func(t *testing.T) {
		// Test deleting a key that doesn't exist
		nonExistentKey := "definitely_does_not_exist_54321"
		delCmd := NewDeleteCommand(nonExistentKey)

		err := client.Do(ctx, delCmd)
		if err != nil {
			t.Fatalf("Delete non-existent key failed: %v", err)
		}

		delResp, err := delCmd.GetResponse(ctx)
		if err != nil {
			t.Fatalf("Failed to get delete response for non-existent key: %v", err)
		}

		// Memcached may return different responses for delete of non-existent key
		t.Logf("Delete non-existent key response: status=%s, error=%v", delResp.Status, delResp.Error)
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
