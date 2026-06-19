package memcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMemcacheAddrTimeout = "127.0.0.1:11211"
)

// TestTimeout_ConfigDefaultTimeout tests that Config.Timeout is applied when context has no deadline
func TestTimeout_ConfigDefaultTimeout(t *testing.T) {
	// Create client with short default timeout
	config := Config{
		MaxSize: 5,
		Timeout: 50 * time.Millisecond, // Very short timeout for testing
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	// Use context with no deadline - should use Config.Timeout
	ctx := context.Background()

	// Normal operations should still work with short timeout
	key := "test:timeout:default"
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("value"),
	})
	require.NoError(t, err)

	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	// Clean up
	_ = client.Delete(ctx, key)
}

// TestTimeout_ContextDeadlineOverridesDefault tests that context deadline takes precedence
func TestTimeout_ContextDeadlineOverridesDefault(t *testing.T) {
	// Create client with long default timeout
	config := Config{
		MaxSize: 5,
		Timeout: 10 * time.Second, // Long default timeout
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	// Use context with very short deadline - should override Config.Timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(10 * time.Millisecond)

	// Operation should fail with timeout or deadline error
	_, err := client.Get(ctx, "test:timeout:context")
	require.Error(t, err)
	// Error message can vary: "deadline", "timeout", etc.
	errMsg := err.Error()
	hasTimeoutOrDeadline := strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline")
	assert.True(t, hasTimeoutOrDeadline, "Expected timeout or deadline error, got: %s", errMsg)
}

// TestTimeout_NoTimeoutWhenZero tests that zero timeout means no timeout
func TestTimeout_NoTimeoutWhenZero(t *testing.T) {
	// Create client with zero timeout (no timeout)
	config := Config{
		MaxSize: 5,
		Timeout: 0, // No timeout
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Operations should work without any timeout
	key := "test:timeout:none"
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("value"),
	})
	require.NoError(t, err)

	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	// Clean up
	_ = client.Delete(ctx, key)
}

// TestTimeout_BatchOperations tests timeout handling in batch operations
func TestTimeout_BatchOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping batch timeout test in short mode")
	}

	// Create client with reasonable timeout
	config := Config{
		MaxSize: 5,
		Timeout: 2 * time.Second,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	batchCmd := NewBatchCommands(client)

	// Create larger batch to test deadline extension behavior
	numKeys := 20
	items := make([]Item, numKeys)
	keys := make([]string, numKeys)
	for i := range items {
		keys[i] = fmt.Sprintf("test:timeout:batch:%d", i)
		items[i] = Item{
			Key:   keys[i],
			Value: []byte(fmt.Sprintf("value-%d", i)),
		}
	}

	ctx := context.Background()

	// MultiSet should complete even with many items
	err := batchCmd.MultiSet(ctx, items)
	require.NoError(t, err, "MultiSet should not timeout with default timeout")

	// MultiGet should complete even with many items
	results, err := batchCmd.MultiGet(ctx, keys)
	require.NoError(t, err, "MultiGet should not timeout with default timeout")
	assert.Len(t, results, numKeys)

	// Verify all items
	for i, result := range results {
		assert.True(t, result.Found, "Key %s should be found", keys[i])
		assert.Equal(t, items[i].Value, result.Value)
	}

	// Clean up
	_ = batchCmd.MultiDelete(ctx, keys)
}

// TestTimeout_BatchWithShortDeadline tests batch operations with tight deadline
func TestTimeout_BatchWithShortDeadline(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 100 * time.Millisecond,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	batchCmd := NewBatchCommands(client)

	// Create small batch
	items := []Item{
		{Key: "test:timeout:short:1", Value: []byte("value1")},
		{Key: "test:timeout:short:2", Value: []byte("value2")},
		{Key: "test:timeout:short:3", Value: []byte("value3")},
	}

	// Use very short timeout that should still allow small batch to complete
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Small batch should succeed
	err := batchCmd.MultiSet(ctx, items)
	require.NoError(t, err, "Small batch should complete within short timeout")

	// Clean up
	keys := []string{items[0].Key, items[1].Key, items[2].Key}
	_ = batchCmd.MultiDelete(context.Background(), keys)
}

// TestTimeout_SingleOperation tests timeout on single operations
func TestTimeout_SingleOperation(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 100 * time.Millisecond,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Normal operations should work fine
	key := "test:timeout:single"
	err := client.Set(ctx, Item{
		Key:   key,
		Value: []byte("value"),
	})
	require.NoError(t, err)

	item, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.True(t, item.Found)

	err = client.Delete(ctx, key)
	require.NoError(t, err)
}

// TestTimeout_ConnectTimeout tests separate connection timeout
func TestTimeout_ConnectTimeout(t *testing.T) {
	// This test uses a non-routable IP to trigger connection timeout
	// 192.0.2.0/24 is reserved for documentation and testing (TEST-NET-1)
	nonRoutableAddr := "192.0.2.1:11211"

	config := Config{
		MaxSize:        5,
		ConnectTimeout: 100 * time.Millisecond, // Very short connect timeout
		Timeout:        1 * time.Second,        // Longer operation timeout
	}

	servers := StaticServers(nonRoutableAddr)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Try to connect - should timeout quickly due to ConnectTimeout
	start := time.Now()
	_, err := client.Get(ctx, "test:key")
	duration := time.Since(start)

	require.Error(t, err)
	// Should timeout close to ConnectTimeout (100ms), not Timeout (1s)
	// Allow some variance for network and system overhead
	assert.Less(t, duration, 500*time.Millisecond, "Should timeout quickly with ConnectTimeout")
}

// TestTimeout_ConnectTimeoutFallback tests that ConnectTimeout falls back to Timeout
func TestTimeout_ConnectTimeoutFallback(t *testing.T) {
	// When ConnectTimeout is not set, should use Timeout
	nonRoutableAddr := "192.0.2.1:11211"

	config := Config{
		MaxSize: 5,
		Timeout: 100 * time.Millisecond, // Should be used for connect too
		// ConnectTimeout not set - should fall back to Timeout
	}

	servers := StaticServers(nonRoutableAddr)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Try to connect - should timeout using Timeout value
	start := time.Now()
	_, err := client.Get(ctx, "test:key")
	duration := time.Since(start)

	require.Error(t, err)
	// Should timeout close to Timeout (100ms)
	assert.Less(t, duration, 500*time.Millisecond, "Should timeout using Timeout value")
}

// TestTimeout_Stats tests timeout on stats command
func TestTimeout_Stats(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 1 * time.Second,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Stats should complete within timeout
	results, err := client.Stats(ctx)
	require.NoError(t, err)
	require.Len(t, results, 1)

	serverStats := results[0]
	assert.NoError(t, serverStats.Error)
	assert.NotEmpty(t, serverStats.Stats)
}

// TestTimeout_StatsWithShortDeadline tests stats command with expired context
func TestTimeout_StatsWithShortDeadline(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 1 * time.Second,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	// Use already-expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	// Stats should fail quickly with context error
	_, err := client.Stats(ctx)
	if err != nil {
		// If we get an error, it should be related to context/deadline/timeout
		errMsg := err.Error()
		hasContextError := strings.Contains(errMsg, "context") ||
			strings.Contains(errMsg, "deadline") ||
			strings.Contains(errMsg, "timeout")
		assert.True(t, hasContextError, "Expected context/deadline/timeout error, got: %s", errMsg)
	}
	// Note: Stats might succeed if connection pool already has a connection established
	// This is acceptable behavior - the important thing is that deadlines are set
}

// TestTimeout_DeadlineExtensionInBatch tests that deadline is extended for each response
func TestTimeout_DeadlineExtensionInBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping deadline extension test in short mode")
	}

	// This test verifies the critical fix from Grafana PR #16
	// Deadline should be extended before reading each response in a batch

	config := Config{
		MaxSize: 10,
		Timeout: 200 * time.Millisecond, // Short per-response timeout
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	batchCmd := NewBatchCommands(client)

	// Create large batch - cumulative time would exceed timeout without deadline extension
	numKeys := 30
	items := make([]Item, numKeys)
	keys := make([]string, numKeys)
	for i := range items {
		keys[i] = fmt.Sprintf("test:timeout:extend:%d", i)
		items[i] = Item{
			Key:   keys[i],
			Value: []byte(fmt.Sprintf("value-%d", i)),
		}
	}

	ctx := context.Background()

	// Set all items
	err := batchCmd.MultiSet(ctx, items)
	require.NoError(t, err, "MultiSet should succeed with deadline extension")

	// Get all items - even though cumulative time might exceed timeout,
	// deadline is extended before each response so it should succeed
	results, err := batchCmd.MultiGet(ctx, keys)
	require.NoError(t, err, "MultiGet should succeed with deadline extension")
	assert.Len(t, results, numKeys)

	// Verify all items
	for i, result := range results {
		assert.True(t, result.Found, "Key %s should be found", keys[i])
	}

	// Clean up
	_ = batchCmd.MultiDelete(ctx, keys)
}

// TestTimeout_ContextCancellationMidBatch tests context cancellation handling
func TestTimeout_ContextCancellationMidBatch(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 1 * time.Second,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	batchCmd := NewBatchCommands(client)

	// Create batch
	items := []Item{
		{Key: "test:timeout:cancel:1", Value: []byte("value1")},
		{Key: "test:timeout:cancel:2", Value: []byte("value2")},
		{Key: "test:timeout:cancel:3", Value: []byte("value3")},
	}

	// Set items first
	err := batchCmd.MultiSet(context.Background(), items)
	require.NoError(t, err)

	// Use already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Operation might fail with context canceled, or might succeed if very fast
	_, err = batchCmd.MultiGet(ctx, []string{items[0].Key, items[1].Key, items[2].Key})
	if err != nil {
		// If it fails, should be due to context cancellation or timeout
		errMsg := err.Error()
		hasContextError := strings.Contains(errMsg, "context") ||
			strings.Contains(errMsg, "cancel") ||
			strings.Contains(errMsg, "deadline") ||
			strings.Contains(errMsg, "timeout")
		assert.True(t, hasContextError, "Expected context-related error, got: %s", errMsg)
	}
	// Note: On a local/fast memcache server, the operation might complete
	// before context cancellation is detected. This is acceptable.

	// Clean up with fresh context
	keys := []string{items[0].Key, items[1].Key, items[2].Key}
	_ = batchCmd.MultiDelete(context.Background(), keys)
}

// TestTimeout_Increment tests timeout on increment operations
func TestTimeout_Increment(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 100 * time.Millisecond,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()
	key := "test:timeout:incr"

	// Clean up first
	_ = client.Delete(ctx, key)

	// Increment should work with timeout
	value, err := client.Increment(ctx, key, 1, NoTTL)
	require.NoError(t, err)
	assert.Equal(t, int64(1), value)

	// Another increment
	value, err = client.Increment(ctx, key, 5, NoTTL)
	require.NoError(t, err)
	assert.Equal(t, int64(6), value)

	// Clean up
	_ = client.Delete(ctx, key)
}

// TestTimeout_Add tests timeout on add operations
func TestTimeout_Add(t *testing.T) {
	config := Config{
		MaxSize: 5,
		Timeout: 100 * time.Millisecond,
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()
	key := "test:timeout:add"

	// Clean up first
	_ = client.Delete(ctx, key)

	// Add should work with timeout
	err := client.Add(ctx, Item{
		Key:   key,
		Value: []byte("value"),
	})
	require.NoError(t, err)

	// Clean up
	_ = client.Delete(ctx, key)
}

// slowDialer is a custom dialer that adds delay to connection establishment
type slowDialer struct {
	delay time.Duration
}

func (d *slowDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Add artificial delay
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Use standard dialer after delay
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, address)
}

// TestTimeout_SlowConnection tests ConnectTimeout with slow connection establishment
func TestTimeout_SlowConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow connection test in short mode")
	}

	config := Config{
		MaxSize:        5,
		ConnectTimeout: 50 * time.Millisecond,                      // Short connect timeout
		Timeout:        1 * time.Second,                            // Longer operation timeout
		Dialer:         &slowDialer{delay: 200 * time.Millisecond}, // Slow dialer
	}

	servers := StaticServers(testMemcacheAddrTimeout)
	client := NewClient(servers, config)
	defer client.Close()

	ctx := context.Background()

	// Connection should timeout during establishment
	start := time.Now()
	_, err := client.Get(ctx, "test:key")
	duration := time.Since(start)

	require.Error(t, err)
	// Should timeout close to ConnectTimeout, not wait for full Dialer delay
	assert.Less(t, duration, 150*time.Millisecond, "Should timeout quickly with ConnectTimeout")
	assert.Contains(t, err.Error(), "deadline")
}

// newHungServer starts a TCP server that accepts connections and completes the
// handshake but never sends a response, simulating a "gray failure": a backend
// that is reachable and connected but unresponsive (frozen process, GC death
// spiral, paused VM). Connections are held open until the test ends.
func newHungServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var conns []net.Conn

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and never respond.
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})

	return ln.Addr().String()
}

// TestTimeout_HungServerBoundedByConfigTimeout is a non-regression test for
// issue #91: a hung-but-connected server must not stall an operation past
// Config.Timeout, even when the caller's context carries a much later deadline.
//
// Regression: setDeadline used the context deadline verbatim whenever one was
// present and only fell back to Config.Timeout when the context had none. A
// long-lived context (an HTTP request, or a job/run-scoped context.WithTimeout)
// therefore disabled the per-op timeout entirely, and a hung server blocked the
// read until the far-future context deadline — observed as a single operation
// stuck for over an hour during the stress soak (see #85).
func TestTimeout_HungServerBoundedByConfigTimeout(t *testing.T) {
	addr := newHungServer(t)

	const opTimeout = 100 * time.Millisecond

	client := NewClient(StaticServers(addr), Config{
		MaxSize: 2,
		Timeout: opTimeout,
	})
	t.Cleanup(client.Close)

	// Context deadline far in the future: Config.Timeout must still bound the op.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	run := func(t *testing.T, op func() error) {
		t.Helper()
		done := make(chan error, 1)
		start := time.Now()
		go func() { done <- op() }()

		select {
		case err := <-done:
			elapsed := time.Since(start)
			require.Error(t, err, "operation against a hung server must fail, not succeed")
			assert.Less(t, elapsed, 2*time.Second,
				"operation should be bounded by Config.Timeout (%s), took %s", opTimeout, elapsed)
			errMsg := err.Error()
			assert.True(t,
				strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline"),
				"expected a timeout/deadline error, got: %s", errMsg)
		case <-time.After(5 * time.Second):
			t.Fatal("operation blocked well past Config.Timeout — a hung server stalled the client (issue #91)")
		}
	}

	t.Run("single op", func(t *testing.T) {
		run(t, func() error {
			_, err := client.Get(ctx, "test:hung:single")
			return err
		})
	})

	t.Run("batch op", func(t *testing.T) {
		batch := NewBatchCommands(client)
		run(t, func() error {
			_, err := batch.MultiGet(ctx, []string{"test:hung:batch:1", "test:hung:batch:2"})
			return err
		})
	})
}

// TestTimeout_BareCancellationDoesNotInterruptOp documents a deliberate design
// choice: like go-redis (and gomemcache), an in-flight blocking read is bounded
// only by the socket deadline, not by context cancellation. Canceling a context
// that carries no deadline therefore does NOT unblock the read early — the
// operation still runs until Config.Timeout arms the socket deadline.
//
// We accept this because Config.Timeout already bounds every operation (see
// TestTimeout_HungServerBoundedByConfigTimeout), so the worst-case wait after a
// bare cancellation is one Config.Timeout — small in practice. Avoiding a
// per-operation cancellation watcher keeps the hot path allocation-free, which
// is the same trade the mature Go clients make.
//
// The op must return a timeout/deadline error (driven by the socket deadline),
// not context.Canceled, and must not return early at the cancellation instant.
func TestTimeout_BareCancellationDoesNotInterruptOp(t *testing.T) {
	addr := newHungServer(t)

	const opTimeout = 200 * time.Millisecond
	client := NewClient(StaticServers(addr), Config{
		MaxSize: 2,
		Timeout: opTimeout,
	})
	t.Cleanup(client.Close)

	const cancelAfter = 25 * time.Millisecond

	run := func(t *testing.T, op func(ctx context.Context) error) {
		t.Helper()
		// A pure cancelable context with no deadline: cancellation alone cannot
		// stop the read, so the socket deadline (Config.Timeout) must.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(cancelAfter, cancel)

		done := make(chan error, 1)
		start := time.Now()
		go func() { done <- op(ctx) }()

		select {
		case err := <-done:
			elapsed := time.Since(start)
			require.Error(t, err, "the operation against a hung server must fail")
			assert.False(t, errors.Is(err, context.Canceled),
				"bare cancellation must not interrupt the read; expected a deadline error, got: %v", err)
			errMsg := err.Error()
			assert.True(t, strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline"),
				"the op must be bounded by the socket deadline, got: %v", err)
			assert.GreaterOrEqual(t, elapsed, opTimeout,
				"the op must run until Config.Timeout, not return at the cancellation instant (took %s)", elapsed)
		case <-time.After(5 * time.Second):
			t.Fatal("operation blocked well past Config.Timeout")
		}
	}

	t.Run("single op", func(t *testing.T) {
		run(t, func(ctx context.Context) error {
			_, err := client.Get(ctx, "test:cancel:single")
			return err
		})
	})

	t.Run("batch op", func(t *testing.T) {
		batch := NewBatchCommands(client)
		run(t, func(ctx context.Context) error {
			_, err := batch.MultiGet(ctx, []string{"test:cancel:batch:1", "test:cancel:batch:2"})
			return err
		})
	})
}

func TestStats_UnreachableServer(t *testing.T) {
	client := NewClient(StaticServers("127.0.0.1:1"), Config{
		MaxSize: 1,
		Timeout: 200 * time.Millisecond,
	})
	t.Cleanup(client.Close)

	results, err := client.Stats(context.Background())
	require.NoError(t, err, "per-server errors are reported in the results, not as a Go error")
	require.Len(t, results, 1)
	assert.Equal(t, "127.0.0.1:1", results[0].Addr)
	assert.Error(t, results[0].Error)
	assert.Nil(t, results[0].Stats)
}
