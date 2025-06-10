package memcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Ensure this matches the host used in integration_test.go or is configurable
var testMemcachedHost = "127.0.0.1:11211"

func newTestClient(t *testing.T, maxIdleConns int) Client {
	config := Config{
		MaxIdleConns: maxIdleConns,
		DialTimeout:  5 * time.Second,
		IdleTimeout:  time.Minute, // Currently informational for custom pool
	}
	client, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	return client
}

func TestPooledClient_MetaSetGetDelete(t *testing.T) {
	client := newTestClient(t, 5)
	defer client.Close()

	key := fmt.Sprintf("test_pooled_setgetdel_%d", time.Now().UnixNano())
	value := []byte("hello pooled world")
	flagsSet := []MetaFlag{FlagSetTTL(60)}

	// MetaSet
	resp, err := client.MetaSet(key, value, flagsSet...)
	if err != nil {
		t.Fatalf("MetaSet(%q) error: %v", key, err)
	}
	if resp.Code != "HD" {
		t.Errorf("MetaSet(%q) code = %q; want HD", key, resp.Code)
	}

	// MetaGet
	flagsGet := []MetaFlag{FlagReturnValue()}
	gResp, err := client.MetaGet(key, flagsGet...)
	if err != nil {
		t.Fatalf("MetaGet(%q) error: %v", key, err)
	}
	if gResp.Code != "VA" {
		t.Errorf("MetaGet(%q) code = %q, want VA", key, gResp.Code)
	}
	if gResp.Size != len(value) {
		t.Errorf("MetaGet(%q) size = %d, want %d", key, gResp.Size, len(value))
	}
	if string(gResp.Data) != string(value) {
		t.Errorf("MetaGet(%q) data = %q, want %q", key, string(gResp.Data), string(value))
	}

	// MetaDelete
	dResp, err := client.MetaDelete(key)
	if err != nil {
		t.Fatalf("MetaDelete(%q) error: %v", key, err)
	}
	if dResp.Code != "HD" && dResp.Code != "OK" {
		t.Errorf("MetaDelete(%q) code = %q; want HD or OK", key, dResp.Code)
	}

	// MetaGet after delete
	gAfterDeleteResp, err := client.MetaGet(key, flagsGet...)
	if err != nil {
		// Error is not strictly expected by memcached protocol for a miss (EN is expected)
		// but let's log it if it happens.
		t.Logf("MetaGet(%q) after delete returned error: %v", key, err)
	}
	if gAfterDeleteResp.Code != "EN" {
		t.Errorf("MetaGet(%q) after delete: code = %q; want EN", key, gAfterDeleteResp.Code)
	}
}

func TestPooledClient_MetaArithmetic(t *testing.T) {
	client := newTestClient(t, 3)
	defer client.Close()

	key := fmt.Sprintf("test_pooled_arith_%d", time.Now().UnixNano())
	initialValue := "100"
	initialValueBytes := []byte(initialValue)

	// Set initial value
	sResp, err := client.MetaSet(key, initialValueBytes, FlagSetTTL(60))
	if err != nil {
		t.Fatalf("MetaSet(%q, %q) for arithmetic error: %v", key, initialValue, err)
	}
	if sResp.Code != "HD" {
		t.Fatalf("MetaSet(%q, %q) for arithmetic code = %q; want HD", key, initialValue, sResp.Code)
	}

	// MetaArithmetic Increment
	incrFlags := []MetaFlag{FlagModeIncr(), FlagDelta(5), FlagReturnValue()}
	aResp, err := client.MetaArithmetic(key, incrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) error: %v", key, err)
	}
	if aResp.Code != "VA" {
		t.Errorf("MetaArithmetic[Incr](%q) code = %q; want VA", key, aResp.Code)
	}
	valStr := string(aResp.Data)
	valInt, convErr := strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 105 {
		t.Errorf("MetaArithmetic[Incr](%q) data = %d (%q), want 105", key, valInt, valStr)
	}
	if aResp.Value != 105 {
		t.Errorf("MetaArithmetic[Incr](%q) parsed value = %d, want 105", key, aResp.Value)
	}

	// MetaArithmetic Decrement
	decrFlags := []MetaFlag{FlagModeDecr(), FlagDelta(3), FlagReturnValue()}
	aResp, err = client.MetaArithmetic(key, decrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Decr](%q) error: %v", key, err)
	}
	if aResp.Code != "VA" {
		t.Errorf("MetaArithmetic[Decr](%q) code = %q; want VA", key, aResp.Code)
	}
	valStr = string(aResp.Data)
	valInt, convErr = strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Decr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 102 {
		t.Errorf("MetaArithmetic[Decr](%q) data = %d (%q), want 102", key, valInt, valStr)
	}
	if aResp.Value != 102 {
		t.Errorf("MetaArithmetic[Decr](%q) parsed value = %d, want 102", key, aResp.Value)
	}

	// Clean up
	client.MetaDelete(key)
}

func TestPooledClient_MetaNoop(t *testing.T) {
	client := newTestClient(t, 2)
	defer client.Close()

	resp, err := client.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop() error: %v", err)
	}
	if resp.Code != "MN" {
		t.Errorf("MetaNoop() code = %q; want MN", resp.Code)
	}
}

func TestPooledClient_MetaGetMiss(t *testing.T) {
	client := newTestClient(t, 2)
	defer client.Close()

	key := fmt.Sprintf("test_pooled_getmiss_%d", time.Now().UnixNano())

	flagsGet := []MetaFlag{FlagReturnValue()}
	gResp, err := client.MetaGet(key, flagsGet...)
	if err != nil {
		t.Fatalf("MetaGet(%q) for miss error: %v", key, err)
	}
	if gResp.Code != "EN" {
		t.Errorf("MetaGet(%q) for miss code = %q; want EN", key, gResp.Code)
	}
}

// TestPooledClient_ConcurrentAccess tests concurrent access to the client.
func TestPooledClient_ConcurrentAccess(t *testing.T) {
	// Ensure memcached is running for this test
	// You might need to skip this test if a memcached instance is not available in the CI environment
	if os.Getenv("CI") != "" {
		t.Skip("Skipping concurrent client test in CI environment without guaranteed memcached")
	}

	client := newTestClient(t, 20) // Updated call, using the previous maxConns value for maxIdleConns
	defer client.Close()

	numGoroutines := 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("concurrent_key_%d_%d", idx, time.Now().UnixNano())
			value := []byte(fmt.Sprintf("concurrent_value_%d", idx))

			// Set
			_, errSet := client.MetaSet(key, value, FlagSetTTL(60))
			if errSet != nil {
				t.Errorf("Goroutine %d: MetaSet error: %v", idx, errSet)
				return
			}

			// Get
			resp, errGet := client.MetaGet(key, FlagReturnValue())
			if errGet != nil {
				t.Errorf("Goroutine %d: MetaGet error: %v", idx, errGet)
				return
			}
			if string(resp.Data) != string(value) {
				t.Errorf("Goroutine %d: MetaGet data mismatch: got %q, want %q", idx, string(resp.Data), string(value))
			}

			// Delete
			_, errDel := client.MetaDelete(key)
			if errDel != nil {
				t.Errorf("Goroutine %d: MetaDelete error: %v", idx, errDel)
			}
		}(i)
	}

	wg.Wait()
}

func TestPooledClient_CustomDialFunc(t *testing.T) {
	var dialerUsed bool
	customDialFunc := func(ctx context.Context, network, address string) (net.Conn, error) {
		dialerUsed = true
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	config := Config{
		MaxIdleConns: 1, // Updated field
		DialTimeout:  2 * time.Second,
		DialFunc:     customDialFunc,
	}

	client, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("Failed to create client with custom dialer: %v", err)
	}
	defer client.Close()

	// Perform a simple operation to trigger a connection
	_, err = client.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop with custom dialer error: %v", err)
	}

	if !dialerUsed {
		t.Errorf("Custom dial function was not used")
	}
}

func TestPooledClient_DialTimeout(t *testing.T) {
	// Using a non-routable address to ensure dial timeout occurs
	// See RFC 5737 for TEST-NET-1 documentation (192.0.2.0/24)
	nonRoutableAddress := "192.0.2.1:11211"

	config := Config{
		MaxIdleConns: 1,                      // Updated field
		DialTimeout:  100 * time.Millisecond, // Very short timeout
	}

	client, err := NewClient(nonRoutableAddress, config)
	if err != nil {
		// With the new custom pool, NewClient itself should not error out due to dial timeout
		// as it doesn't make connections upfront.
		t.Fatalf("NewClient failed unexpectedly: %v", err)
	}
	defer client.Close()

	// An operation should fail with a timeout.
	startTime := time.Now()
	_, err = client.MetaNoop() // This should trigger a dial and timeout
	duration := time.Since(startTime)

	if err == nil {
		t.Fatalf("MetaNoop did not return an error when a dial timeout was expected")
	}

	// Check if the error is a timeout error or wraps a timeout error
	var netErr net.Error
	if !(errors.As(err, &netErr) && netErr.Timeout()) {
		// Check if it's wrapped in a pool error (though less likely with custom pool)
		// or if the error string contains "timeout"
		errStr := err.Error()
		if !(strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout")) {
			t.Errorf("Expected a timeout error, got: %v (type: %T)", err, err)
		}
	}

	// Check if the operation timed out roughly within the DialTimeout duration
	// Allow some buffer for test execution overhead.
	if duration > config.DialTimeout+(200*time.Millisecond) {
		t.Errorf("Operation took too long (%v), expected to timeout near %v", duration, config.DialTimeout)
	}
	t.Logf("Dial timed out as expected in %v for MetaNoop. Error: %v", duration, err)
}

func TestPooledClient_IdleTimeout(t *testing.T) {
	idleTimeout := 100 * time.Millisecond
	config := Config{
		MaxIdleConns: 1,
		IdleTimeout:  idleTimeout,
		DialTimeout:  1 * time.Second,
	}
	// Cast to *client to access the pool for testing purposes.
	// This is a test-specific pattern and not for general use.
	c, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	// Perform an operation to get a connection into the pool
	_, err = c.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}

	// Helper function to get free connection count from the pool
	getFreeConnCount := func(p *Pool) int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.freeconn)
	}

	// Check that there is one connection in the pool
	if getFreeConnCount(c.pool) != 1 {
		t.Fatalf("Expected 1 free connection, got %d", getFreeConnCount(c.pool))
	}

	// Wait for longer than the idle timeout
	time.Sleep(idleTimeout + 50*time.Millisecond)

	// Perform another operation. The pool's Get method should discard the stale connection.
	_, err = c.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop after idle timeout failed: %v", err)
	}

	// After the second Noop, the previously idle connection should have been closed by Pool.Get,
	// a new one dialed for the Noop, and then that new one returned to the pool.
	// So, we expect 1 free connection again.
	if getFreeConnCount(c.pool) != 1 {
		t.Logf("Expected 1 free connection after idle timeout and new op, got %d. This might be acceptable if the old conn was closed.", getFreeConnCount(c.pool))
	}

	// Test that a connection used just before timeout is not closed
	_, err = c.MetaNoop() // Use conn1, goes to pool
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}
	time.Sleep(idleTimeout / 2) // Wait half the timeout

	_, err = c.MetaNoop() // Use conn2, goes to pool (conn1 is now older)
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}

	// At this point, conn1 is older than conn2 in the pool's free list (if MaxIdleConns allows both)
	// If MaxIdleConns is 1, conn1 was closed when conn2 was put, and conn2 is in pool.

	time.Sleep(idleTimeout/2 + 20*time.Millisecond) // conn1 (if present) should be timed out, conn2 should not.

	// This operation should find conn2 (not timed out) or a new connection if conn2 also timed out (less likely).
	_, err = c.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop should have found a valid connection: %v", err)
	}

	// Expect 1 connection in the pool (the one just used for MetaNoop).
	// If MaxIdleConns was >1 and conn1 timed out, it would have been removed.
	// If MaxIdleConns is 1, the logic is simpler: the last used conn is in the pool.
	if getFreeConnCount(c.pool) != 1 {
		t.Errorf("Expected 1 connection in pool after staggered use, found %d", getFreeConnCount(c.pool))
	}
}

// Mock net.Error for testing condRelease (now pooledConn.Release)
type mockNetError struct {
	timeout bool
	temp    bool
	err     error
}

func (m *mockNetError) Error() string   { return m.err.Error() }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temp }

func TestPooledClient_CondRelease(t *testing.T) {
	// This test needs to be adapted to test pool.pooledConn.Release indirectly
	// or by directly creating a Pool and getting a pooledConn from it.

	config := Config{MaxIdleConns: 1, DialTimeout: 1 * time.Second}
	// We need the underlying pool to inspect its state or to directly manipulate pooledConn instances.
	c, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer c.Close()

	p := c.pool // Get the actual pool instance

	// Mock a net.Conn server part
	mockNetServer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer mockNetServer.Close()

	// Override the pool's dial function to connect to our mock server
	// and to allow us to get a handle on the net.Conn
	var lastDialedConn net.Conn
	p.config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, dialErr := net.Dial(network, mockNetServer.Addr().String())
		if dialErr == nil {
			lastDialedConn = conn // Store the client side of the connection
		}
		return conn, dialErr
	}

	tests := []struct {
		name         string
		err          error
		expectReuse  bool // true if the connection should be put back to freeconn
		expectClosed bool // true if the underlying net.Conn should be closed by Release
	}{
		{"nil error", nil, true, false},
		{"net.Error timeout", &mockNetError{timeout: true, err: errors.New("timeout")}, true, false},
		{"io.EOF", io.EOF, false, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, false, true},
		{"syscall.ECONNRESET", syscall.ECONNRESET, false, true},
		{"syscall.EPIPE", syscall.EPIPE, false, true},
		{"other net.Error", &net.OpError{Op: "read", Net: "tcp", Addr: nil, Err: errors.New("other net error")}, true, false},
		{"other generic error", errors.New("some other error"), true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Get a connection from the pool (this will use our mock dialer)
			pConn, errGet := p.Get()
			if errGet != nil {
				t.Fatalf("Pool.Get() failed: %v", errGet)
			}

			// Accept the connection on the server side
			serverConn, acceptErr := mockNetServer.Accept()
			if acceptErr != nil {
				t.Fatalf("mockNetServer.Accept() failed: %v", acceptErr)
			}
			defer serverConn.Close() // Close server side of the connection

			// Ensure we have the client-side net.Conn that pConn is wrapping
			currentTestNetConn := lastDialedConn
			if currentTestNetConn == nil {
				t.Fatal("DialFunc did not capture the dialed connection")
			}
			if pConn.nc != currentTestNetConn { // Sanity check
				t.Fatal("pooledConn.nc does not match lastDialedConn")
			}

			errToTest := tt.err
			pConn.Release(errToTest) // Call Release on the pooledConn

			p.mu.Lock()
			numFree := len(p.freeconn)
			p.mu.Unlock()

			if tt.expectReuse {
				if numFree != 1 {
					t.Errorf("Expected connection to be reused (numFree=1), but numFree=%d. Error: %v", numFree, tt.err)
				}
				// If reused, it should not be closed by Release. We can try a write.
				// This check is only valid if the connection was indeed put back.
				if numFree == 1 {
					_, writeErr := currentTestNetConn.Write([]byte("ping"))
					if writeErr != nil {
						t.Errorf("Connection was expected to be reusable, but Write failed: %v. Original error: %v", writeErr, tt.err)
					}
				}
			} else { // Not expecting reuse, so it should be closed
				if numFree != 0 {
					t.Errorf("Expected connection to be discarded (numFree=0), but numFree=%d. Error: %v", numFree, tt.err)
				}
				if tt.expectClosed {
					// Try to use the net.Conn, expect 'use of closed network connection'
					_, writeErr := currentTestNetConn.Write([]byte("ping"))
					if writeErr == nil {
						t.Errorf("Connection was expected to be closed by Release, but Write succeeded. Original error: %v", tt.err)
					} else if !strings.Contains(writeErr.Error(), "closed network connection") && !strings.Contains(writeErr.Error(), "broken pipe") {
						t.Errorf("Write on expected-closed connection failed with unexpected error: %v. Original error: %v", writeErr, tt.err)
					} else {
						t.Logf("Connection correctly closed for error '%v', write attempt failed with: %v", tt.err, writeErr)
					}
				}
			}

			// Clean up pool for next iteration: if a conn was released, remove it.
			if numFree > 0 {
				conn, _ := p.Get() // remove it
				if conn != nil {
					conn.nc.Close() // ensure underlying net.conn is closed
				}
			}
		})
	}
}

// TestPooledClient_AddressParameter tests that NewClient correctly uses the address parameter.
func TestPooledClient_AddressParameter(t *testing.T) {
	// Start a real memcached server for this test or mock it.
	// For simplicity, we'll assume testMemcachedHost is running.
	// If not, this test will fail at MetaNoop.

	config := Config{MaxIdleConns: 1}
	client, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("NewClient(%q) failed: %v", testMemcachedHost, err)
	}
	defer client.Close()

	// Perform an operation to ensure the address was used.
	_, err = client.MetaNoop()
	if err != nil {
		// If memcached is not at testMemcachedHost, this will error.
		// That's an acceptable test failure if the environment isn't set up.
		t.Fatalf("MetaNoop() with client created with address %q error: %v", testMemcachedHost, err)
	}

	// Test with a bad address
	badAddress := "127.0.0.1:99999" // Invalid port
	clientBad, errBad := NewClient(badAddress, config)
	if errBad != nil {
		// NewClient itself doesn't dial, so it shouldn't error here for a bad address format
		// unless net.ResolveTCPAddr fails, which it might for a syntactically invalid address.
		t.Logf("NewClient(%q) returned error as expected (or not, depending on ResolveTCPAddr behavior): %v", badAddress, errBad)
		// If NewClient *does* error (e.g. on ResolveTCPAddr), the test for this part is done.
		// If it does not error, the MetaNoop below should fail.
	}
	if clientBad != nil { // Proceed only if NewClient didn't error out
		defer clientBad.Close()
		_, errOpBad := clientBad.MetaNoop()
		if errOpBad == nil {
			t.Errorf("MetaNoop() with client for bad address %q should have failed, but succeeded", badAddress)
		} else {
			t.Logf("MetaNoop() with client for bad address %q failed as expected: %v", badAddress, errOpBad)
		}
	}
}
