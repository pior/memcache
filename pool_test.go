package memcache

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

// mockNetError is a utility for creating net.Error instances for testing.
type mockNetError struct {
	err     error
	timeout bool
	temp    bool
}

func (m *mockNetError) Error() string   { return m.err.Error() }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temp }

// startMockServer starts a simple TCP server on a local port.
func startMockServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen on a port: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// If the listener is closed, Accept will return an error.
				// We can check for net.ErrClosed here if we want to be specific.
				return
			}
			// For basic tests, just close the connection.
			// More complex tests might require reading/writing.
			go func(c net.Conn) {
				defer c.Close()
				// Optionally, read a byte to ensure client connected
				// b := make([]byte, 1)
				// c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				// _, _ = c.Read(b)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("Failed to close listener: %v", err)
		}
	})

	return ln.Addr().String()
}

func TestPool_With_Basic(t *testing.T) {
	addr := startMockServer(t)

	dialCount := 0
	cfg := Config{
		DialTimeout:  100 * time.Millisecond,
		MaxIdleConns: 1,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCount++
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
	}
	pool := newPool(addr, cfg)

	// 1. First call to With: should dial a new connection
	err := pool.With(func(conn net.Conn) error {
		if conn == nil {
			t.Error("Callback received nil connection on first call")
		}
		// Simple write to ensure connection is usable
		_, writeErr := conn.Write([]byte("ping"))
		if writeErr != nil {
			t.Errorf("Write to connection failed: %v", writeErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pool.With (first call) failed: %v", err)
	}
	if dialCount != 1 {
		t.Errorf("dialCount after first call = %d; want 1", dialCount)
	}
	if pool.connectionsCount() != 1 {
		t.Errorf("pool connections after first call = %d; want 1", pool.connectionsCount())
	}

	// 2. Second call to With: should reuse the connection
	err = pool.With(func(conn net.Conn) error {
		if conn == nil {
			t.Error("Callback received nil connection on second call")
		}
		_, writeErr := conn.Write([]byte("pong"))
		if writeErr != nil {
			t.Errorf("Write to connection failed: %v", writeErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pool.With (second call) failed: %v", err)
	}
	if dialCount != 1 { // Should not have dialed again
		t.Errorf("dialCount after second call = %d; want 1", dialCount)
	}
	if pool.connectionsCount() != 1 { // Connection should be back in pool
		t.Errorf("pool connections after second call = %d; want 1", pool.connectionsCount())
	}
}

func TestPool_With_CallbackError(t *testing.T) {
	addr := startMockServer(t)

	myError := errors.New("callback error")
	dialCount := 0

	cfg := Config{
		DialTimeout:  100 * time.Millisecond,
		MaxIdleConns: 1,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCount++
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
	}
	pool := newPool(addr, cfg)

	// resumableError currently returns false for any error.
	// So, the connection should be closed and not returned to the pool.
	err := pool.With(func(conn net.Conn) error {
		return myError
	})

	if !errors.Is(err, myError) {
		t.Fatalf("pool.With error = %v; want %v", err, myError)
	}
	if dialCount != 1 {
		t.Errorf("dialCount = %d; want 1", dialCount)
	}
	// Because resumableError is strict, the connection should have been closed, not pooled.
	count := pool.connectionsCount()
	if count != 0 {
		t.Errorf("pool connections after callback error = %d; want 0", count)
	}

	// Test with a nil error from callback (connection should be pooled)
	err = pool.With(func(conn net.Conn) error {
		return nil
	})
	if err != nil {
		t.Fatalf("pool.With (nil callback error) failed: %v", err)
	}
	if dialCount != 2 { // Should have dialed a new one
		t.Errorf("dialCount after nil callback error = %d; want 2", dialCount)
	}
	count = pool.connectionsCount()
	if count != 1 {
		t.Errorf("pool connections after nil callback error = %d; want 1", count)
	}
}

func concurrentConnection(t *testing.T, pool *Pool, count int) {
	t.Helper()

	done := make(chan struct{})
	var started sync.WaitGroup
	started.Add(count)
	var stopped sync.WaitGroup
	stopped.Add(count)

	for range count {
		go func() {
			pool.With(func(c net.Conn) error {
				started.Done()
				<-done
				return nil
			})
			stopped.Done()
		}()
	}

	started.Wait() // Wait for both goroutines to start and acquire connections
	close(done)    // Signal both goroutines to stop
	stopped.Wait() // Wait for both goroutines to finish and release connections
}

func TestPool_Close(t *testing.T) {
	addr := startMockServer(t)

	var dialCount int
	var connsClosed int32
	var activeConns []*net.Conn // To check if they are closed

	cfg := Config{
		DialTimeout:  100 * time.Millisecond,
		MaxIdleConns: 2,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Log("Dialing new connection")
			dialCount++
			var d net.Dialer
			conn, err := d.DialContext(ctx, network, address)
			if err == nil {
				// Wrap the connection to track its Close call
				mock := &mockConn{Conn: conn, t: t, closedCounter: &connsClosed}
				activeConns = append(activeConns, &mock.Conn) // Store original conn for later check
				return mock, nil
			}
			return nil, err
		},
	}
	pool := newPool(addr, cfg)

	concurrentConnection(t, pool, 2) // Start two goroutines to acquire connections concurrently

	count := pool.connectionsCount()
	if count != 2 {
		t.Fatalf("Expected 2 connections in pool, got %d", count)
	}

	err := pool.Close()
	if err != nil {
		t.Fatalf("pool.Close() failed: %v", err)
	}

	count = pool.connectionsCount()
	if count != 0 {
		t.Errorf("pool connections after Close = %d; want 0", count)
	}

	// Check if the actual net.Conns were closed.
	// This relies on the mockConn wrapper correctly incrementing connsClosed.
	// A more direct check would be to try using the connections.
	if connsClosed != 2 {
		t.Errorf("Number of connections reported closed by mockConn = %d; want 2", connsClosed)
	}

	// Verify Get() after Close() fails or behaves as expected (e.g. dials new if not fully closed state)
	// Current Pool.Close doesn't prevent new dials. If that's desired, Pool needs a 'closed' flag.
	// For now, With will just dial a new connection.
	// Let's test that With still works and dials a new connection.

	dialCount = 0 // Reset dial count for this test

	err = pool.With(func(c net.Conn) error { return nil })
	if err != nil {
		t.Errorf("pool.With after Close failed: %v", err)
	}
	if dialCount != 1 {
		t.Errorf("Dial count after Close and With = %d; want 1", dialCount)
	}
}

// mockConn wraps a net.Conn to track if Close() is called.
type mockConn struct {
	net.Conn
	t             *testing.T
	closeCalled   bool
	closedCounter *int32 // Pointer to a shared counter
	mu            sync.Mutex
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	if !m.closeCalled {
		m.closeCalled = true
		if m.closedCounter != nil {
			// Use atomic operation if multiple goroutines could call Close on same mock.
			// For this test structure, it's likely sequential from pool.Close().
			*m.closedCounter++
		}
	}
	m.mu.Unlock()
	return m.Conn.Close()
}

func TestPool_MaxIdleConns(t *testing.T) {
	addr := startMockServer(t)

	dialCount := 0
	cfg := Config{
		MaxIdleConns: 1,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCount++
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
	}
	pool := newPool(addr, cfg)

	concurrentConnection(t, pool, 3)

	if dialCount != 3 {
		t.Errorf("Dial count after call 1 = %d; want 1", dialCount)
	}
	if pool.connectionsCount() != 1 {
		t.Errorf("Pool size after call 1 = %d; want 1", pool.connectionsCount())
	}
}

func TestPool_IdleTimeout(t *testing.T) {
	addr := startMockServer(t)

	idleTimeout := 50 * time.Millisecond
	dialCount := 0

	cfg := Config{
		MaxIdleConns: 1,
		IdleTimeout:  idleTimeout,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCount++
			var d net.Dialer
			return d.DialContext(ctx, network, address)
		},
	}
	pool := newPool(addr, cfg)

	// Call 1: Dials, uses, returns to pool.
	err := pool.With(func(c net.Conn) error { return nil })
	if err != nil {
		t.Fatalf("With call 1 failed: %v", err)
	}
	if dialCount != 1 {
		t.Errorf("Dial count after call 1 = %d; want 1", dialCount)
	}
	if pool.connectionsCount() != 1 {
		t.Errorf("Pool size after call 1 = %d; want 1", pool.connectionsCount())
	}

	// Wait for idle timeout
	time.Sleep(idleTimeout + 20*time.Millisecond)

	// Call 2: Should find the connection stale, close it, and dial a new one.
	err = pool.With(func(c net.Conn) error { return nil })
	if err != nil {
		t.Fatalf("With call 2 (after timeout) failed: %v", err)
	}
	if dialCount != 2 { // Should have dialed again
		t.Errorf("Dial count after call 2 (after timeout) = %d; want 2", dialCount)
	}
	if pool.connectionsCount() != 1 { // New connection should be in pool
		t.Errorf("Pool size after call 2 (after timeout) = %d; want 1", pool.connectionsCount())
	}
}

// TestPool_ResumableError tests the resumableError logic, though it's very basic now.
func TestPool_ResumableError(t *testing.T) {
	pool := &Pool{} // Config doesn't matter for this direct test.

	tests := []struct {
		name   string
		err    error
		want   bool
		isTemp bool // for net.Error
		isTime bool // for net.Error
	}{
		{"nil error", nil, true, false, false},
		{"generic error", errors.New("generic"), false, false, false},                                        // Currently all errors are non-resumable
		{"io.EOF", io.EOF, false, false, false},                                                              // Should be non-resumable
		{"syscall.ECONNRESET", syscall.ECONNRESET, false, false, false},                                      // Should be non-resumable
		{"net.Error timeout", &mockNetError{err: errors.New("timeout"), timeout: true}, false, false, true},  // Should be resumable
		{"net.Error temporary", &mockNetError{err: errors.New("temporary"), temp: true}, false, true, false}, // Should be resumable
		{"net.Error other", &mockNetError{err: errors.New("other net")}, false, false, false},                // Should be non-resumable (unless logic changes)
	}

	// Update this part of the test once pool.resumableError is implemented fully
	t.Log("NOTE: TestPool_ResumableError expects current behavior where only nil is resumable.")
	t.Log("Update this test when resumableError handles specific network/memcached errors.")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Manually adjust expected 'want' based on current resumableError logic
			currentWant := false
			if tt.err == nil {
				currentWant = true
			}
			// TODO: When resumableError is updated, change `currentWant` based on `tt.want`
			// For now, we test the *actual* behavior.
			// if (tt.isTemp || tt.isTime) {
			//  currentWant = true // Assuming timeouts and temporary errors will be resumable
			// }

			if got := pool.resumableError(tt.err); got != currentWant {
				t.Errorf("pool.resumableError(%v) = %v; want %v (based on current implementation)", tt.err, got, currentWant)
			}
		})
	}
}
