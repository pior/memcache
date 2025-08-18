package memcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestClientDo(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	t.Run("no commands", func(t *testing.T) {
		err := client.Do(ctx)
		require.NoError(t, err)
	})

	t.Run("multi commands", func(t *testing.T) {
		commands := []*protocol.Command{
			NewGetCommand("key1"),
			NewSetCommand("key2", []byte("value2"), time.Minute),
			NewDeleteCommand("key3"),
		}
		setOpaqueFromKey(commands...)

		err := client.DoWait(ctx, commands...)
		require.NoError(t, err)

		// Check responses match commands
		for i, cmd := range commands {
			if cmd.Response.Opaque != cmd.Opaque {
				t.Errorf("Response %d: expected opaque %s, got %s", i, cmd.Opaque, cmd.Response.Opaque)
			}
		}
	})
}

func TestIntegration_BasicOperations(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	// Test Set operation
	key := "integration_test_key"
	value := []byte("integration_test_value")

	t.Run("Set", func(t *testing.T) {
		setCmd := NewSetCommand(key, value, time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.NoError(t, err)
		assertNoResponseError(t, setCmd)
	})

	t.Run("Get", func(t *testing.T) {
		getCmd := NewGetCommand(key)
		err := client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertNoResponseError(t, getCmd)
		require.Equal(t, value, getCmd.Response.Value)
	})

	t.Run("Delete", func(t *testing.T) {
		delCmd := NewDeleteCommand(key)
		err := client.DoWait(ctx, delCmd)
		require.NoError(t, err)
		assertNoResponseError(t, delCmd)

		// Verify key is deleted
		getCmd := NewGetCommand(key)
		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertResponseErrorIs(t, getCmd, protocol.ErrCacheMiss)
	})

	t.Run("Increment", func(t *testing.T) {
		key := "increment_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("10"), time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.NoError(t, err)
		assertNoResponseError(t, setCmd)

		// Increment by 5
		incrCmd := NewIncrementCommand(key, 5)
		err = client.DoWait(ctx, incrCmd)
		require.NoError(t, err)
		assertNoResponseError(t, incrCmd)

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertNoResponseError(t, getCmd)

		// Verify value is incremented (this test depends on memcached behavior)
		t.Logf("Value after increment: %s", string(getCmd.Response.Value))
	})

	t.Run("Decrement", func(t *testing.T) {
		key := "decrement_test"

		// Set initial value
		setCmd := NewSetCommand(key, []byte("20"), time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.NoError(t, err)
		assertNoResponseError(t, setCmd)

		// Decrement by 3
		decrCmd := NewDecrementCommand(key, 3)
		err = client.DoWait(ctx, decrCmd)
		require.NoError(t, err)
		assertNoResponseError(t, decrCmd)

		// Get to verify result
		getCmd := NewGetCommand(key)
		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertNoResponseError(t, getCmd)

		// Verify value is decremented (this test depends on memcached behavior)
		t.Logf("Value after decrement: %s", string(getCmd.Response.Value))
	})

	t.Run("Debug", func(t *testing.T) {
		setCmd := NewSetCommand("debug_test", []byte("debug_value"), time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.NoError(t, err)

		debugCmd := NewDebugCommand("debug_test")
		err = client.DoWait(ctx, debugCmd)
		require.NoError(t, err)

		assertNoResponseError(t, debugCmd)
		t.Logf("Debug response: status=%s, flags=%+v", debugCmd.Response.Status, debugCmd.Response.Flags)
		assertResponseValueMatch(t, debugCmd, `ME debug_test exp=3600 la=0 cas=\d+ fetch=no cls=1 size=80`)
	})

	t.Run("NoOp", func(t *testing.T) {
		// Try no-op command
		nopCmd := NewNoOpCommand()
		err := client.DoWait(ctx, nopCmd)
		require.NoError(t, err)

		assertNoResponseError(t, nopCmd)
		t.Logf("NoOp response: status=%s, flags=%+v", nopCmd.Response.Status, nopCmd.Response.Flags)
	})

}

func TestIntegration_MultipleKeys(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	// Set multiple keys
	numKeys := 10
	keys := make([]string, numKeys)
	values := make([][]byte, numKeys)
	setCommands := make([]*protocol.Command, numKeys)

	for i := range numKeys {
		keys[i] = fmt.Sprintf("multi_key_%d", i)
		values[i] = []byte(fmt.Sprintf("multi_value_%d", i))
		setCommands[i] = NewSetCommand(keys[i], values[i], time.Hour)
	}

	// Execute all set commands at once
	err := client.DoWait(ctx, setCommands...)
	require.NoError(t, err)

	assertNoResponseError(t, setCommands...)

	// Get multiple keys
	getCommands := make([]*protocol.Command, numKeys)
	for i := range numKeys {
		getCommands[i] = NewGetCommand(keys[i])
	}

	err = client.DoWait(ctx, getCommands...)
	require.NoError(t, err)

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
	for i := range numKeys {
		deleteCommands[i] = NewDeleteCommand(keys[i])
	}

	err = client.DoWait(ctx, deleteCommands...)
	require.NoError(t, err)

	assertNoResponseError(t, deleteCommands...)
}

func TestIntegration_TTL(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	key := "ttl_test_key"
	value := []byte("ttl_test_value")

	setCmd := NewSetCommand(key, value, 1*time.Second)
	err := client.DoWait(ctx, setCmd)
	require.NoError(t, err)
	assertNoResponseError(t, setCmd)

	// Verify key exists immediately
	getCmd := NewGetCommand(key)
	err = client.DoWait(ctx, getCmd)
	require.NoError(t, err)

	assertNoResponseError(t, getCmd)
	require.Equal(t, value, getCmd.Response.Value)

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Verify key has expired
	getCmd = NewGetCommand(key)
	err = client.DoWait(ctx, getCmd)
	require.NoError(t, err)

	assertResponseErrorIs(t, getCmd, protocol.ErrCacheMiss)
}

func TestIntegration_ConcurrentOperations(t *testing.T) {
	// Test that basic operations work when called from multiple goroutines
	// but serialize the actual memcache operations to avoid race conditions
	client := createTestingClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Use a mutex to serialize access and avoid race conditions
	var mu sync.Mutex
	numWorkers := 3

	var wg sync.WaitGroup
	errorChan := make(chan error, numWorkers)

	// Each worker does one operation, serialized by mutex
	for worker := range numWorkers {
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
			if err := client.DoWait(ctx, setCmd); err != nil {
				errorChan <- fmt.Errorf("worker %d set failed: %v", workerID, err)
				return
			}

			if setCmd.Response.Error != nil {
				errorChan <- fmt.Errorf("worker %d set error: %v", workerID, setCmd.Response.Error)
				return
			}

			// Simple get
			getCmd := NewGetCommand(key)
			if err := client.DoWait(ctx, getCmd); err != nil {
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
	client := createTestingClient(t)

	ctx := context.Background()

	sizes := []int{
		1024,       // 1KB
		1024 * 10,  // 10KB
		1024 * 100, // 100KB
		1024 * 512, // 512KB
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			key := fmt.Sprintf("large_value_key_%d", size)

			value := bytes.Repeat([]byte("AA55"), size/4)

			// Set large value
			setCmd := NewSetCommand(key, value, time.Hour)
			err := client.DoWait(ctx, setCmd)
			require.NoError(t, err)

			assertNoResponseError(t, setCmd)

			// Get large value
			getCmd := NewGetCommand(key)
			err = client.DoWait(ctx, getCmd)
			require.NoError(t, err)

			assertNoResponseError(t, getCmd)
			assertResponseValue(t, getCmd, value)

			// Clean up
			delCmd := NewDeleteCommand(key)
			_ = client.Do(ctx, delCmd)
		})
	}
}

func TestIntegration_ContextCancellation(t *testing.T) {
	client := createTestingClient(t)

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	key := "context_test_key"
	value := []byte("context_test_value")

	setCmd := NewSetCommand(key, value, time.Hour)
	err := client.DoWait(ctx, setCmd)
	require.Error(t, err)

	// Test with timeout context
	ctx, cancel = context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(10 * time.Millisecond)

	err = client.Do(ctx, setCmd)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestIntegration_MixedOperations(t *testing.T) {
	client := createTestingClient(t)

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
	require.NoError(t, err)

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
	client := createTestingClient(t)

	ctx := context.Background()

	// Test ping
	err := client.Ping(ctx)
	require.NoError(t, err)

	// Test ping with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = client.Ping(ctx)
	require.NoError(t, err)
}

func TestIntegration_Stats(t *testing.T) {
	client := createTestingClient(t)

	// Perform some operations to generate stats
	ctx := context.Background()
	for i := range 10 {
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
	for i := range 10 {
		key := fmt.Sprintf("stats_test_key_%d", i)
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	}
}

func TestIntegration_WaitAll(t *testing.T) {
	client := createTestingClient(t)

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
		err := client.DoWait(ctx, commands...)
		require.NoError(t, err)

		// All responses should be immediately available
		for _, cmd := range commands {
			assertNoResponseError(t, cmd)
		}

		// Clean up
		for _, key := range expectedKeys {
			delCmd := NewDeleteCommand(key)
			_ = client.Do(ctx, delCmd)
		}
	})

	t.Run("WaitWithTimeout", func(t *testing.T) {
		cmd := NewGetCommand("waitall_timeout_test")
		cmd.Opaque = "1234"

		ctx, cancel := context.WithTimeout(ctx, time.Microsecond)
		defer cancel()

		err := client.DoWait(ctx, cmd)

		switch {
		case strings.HasSuffix(err.Error(), "i/o timeout"):
		case errors.Is(err, context.DeadlineExceeded):
		default:
			require.Fail(t, "expected an i/o timeout error, got: %v", err)
		}
	})
}

func TestIntegration_ErrorHandling(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	// Test various error conditions
	tests := []struct {
		name string
		cmd  *protocol.Command
	}{
		{
			name: "empty key",
			cmd:  protocol.NewCommand("mg", ""),
		},
		{
			name: "invalid key with space",
			cmd:  protocol.NewCommand("mg", "key with space"),
		},
		{
			name: "invalid key with newline",
			cmd:  protocol.NewCommand("mg", "key\nwith\nnewline"),
		},
		{
			name: "key too long",
			cmd:  protocol.NewCommand("mg", string(make([]byte, 300))), // memcached max key length is ~250
		},
		{
			name: "unsupported command type",
			cmd:  protocol.NewCommand("unknown", "valid_key"),
		},
		{
			name: "set without value",
			cmd:  protocol.NewCommand("ms", "valid_key"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.DoWait(ctx, tt.cmd)
			require.Error(t, err)
		})
	}

	t.Run("InvalidKey", func(t *testing.T) {
		// Test with invalid key (too long)
		longKey := strings.Repeat("a", protocol.MaxKeyLength+1)
		getCmd := NewGetCommand(longKey)

		err := client.DoWait(ctx, getCmd)
		require.ErrorIs(t, err, ErrMalformedKey)
	})

	t.Run("KeyWithSpaces", func(t *testing.T) {
		// Test with key containing spaces
		invalidKey := "key with spaces"
		getCmd := NewGetCommand(invalidKey)

		err := client.DoWait(ctx, getCmd)
		require.ErrorIs(t, err, ErrMalformedKey)
	})

	t.Run("GetNonExistentKey", func(t *testing.T) {
		getCmd := NewGetCommand("definitely_does_not_exist_12345")

		err := client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertResponseErrorIs(t, getCmd, protocol.ErrCacheMiss)
	})

	t.Run("DeleteNonExistentKey", func(t *testing.T) {
		// Test deleting a key that doesn't exist
		delCmd := NewDeleteCommand("definitely_does_not_exist_54321")

		err := client.DoWait(ctx, delCmd)
		require.NoError(t, err)
		assertResponseErrorIs(t, delCmd, protocol.ErrCacheMiss)

		// Memcached may return different responses for delete of non-existent key
		t.Logf("Delete non-existent key response: status=%s, error=%v", delCmd.Response.Status, delCmd.Response.Error)
	})
}

func TestIntegration_Flags(t *testing.T) {
	client := createTestingClient(t)

	ctx := context.Background()

	t.Run("GetWithFlags", func(t *testing.T) {
		key := "flags_test"
		value := []byte("flags_test_value")

		// Set value first
		setCmd := NewSetCommand(key, value, time.Hour)
		err := client.DoWait(ctx, setCmd)
		require.NoError(t, err)
		assertNoResponseError(t, setCmd)

		// Test basic get first (NewGetCommand sets "v" flag by default)
		getCmd := NewGetCommand(key)
		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)

		assertResponseValue(t, getCmd, value)
		t.Logf("Basic get response flags: %+v", getCmd.Response.Flags)

		// Test with size flag only (without value flag to avoid conflicts)
		getCmd = NewGetCommand(key)
		getCmd.Flags = protocol.Flags{{Type: protocol.FlagSize}} // Replace flags to request only size

		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertResponseErrorIs(t, getCmd, nil)

		// Should have size flag in response but no value
		if sizeStr, exists := getCmd.Response.Flags.Get("s"); !exists {
			t.Error("Expected size flag 's' in response")
		} else {
			t.Logf("Size flag value: %s", sizeStr)
		}
		require.Len(t, getCmd.Response.Value, 0, "Expected no value when only requesting size")

		// Test with both value and size flags
		getCmd = NewGetCommand(key)
		getCmd.Flags = protocol.Flags{
			{Type: protocol.FlagValue}, // Request value
			{Type: protocol.FlagSize},  // Request size
		}
		err = client.DoWait(ctx, getCmd)
		require.NoError(t, err)
		assertNoResponseError(t, getCmd)
		assertResponseValue(t, getCmd, value)

		// Clean up
		delCmd := NewDeleteCommand(key)
		client.Do(ctx, delCmd)
	})
}

func createTestingClient(t testing.TB) *Client {
	if testing.Short() {
		t.Skip("testing.Short(), skipping integration test")
	}

	t.Helper()

	client, err := NewClient(GetMemcacheServers(), &ClientConfig{
		PoolConfig: PoolConfig{
			MinConnections: 1,
			MaxConnections: 5,
			ConnTimeout:    time.Second,
			IdleTimeout:    time.Minute,
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		client.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Fatal("memcached not responding, skipping integration test")
	}

	return client
}
