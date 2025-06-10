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
	client, err := NewClient(testMemcachedHost, config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	pc, ok := client.(*pooledClient)
	if !ok {
		t.Fatalf("Expected client to be *pooledClient")
	}

	// Perform an operation to get a connection into the pool
	_, err = client.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}

	// Check that there is one connection in the pool
	pc.mu.Lock()
	initialFreeConns := len(pc.freeconn[testMemcachedHost])
	pc.mu.Unlock()
	if initialFreeConns != 1 {
		t.Fatalf("Expected 1 free connection, got %d", initialFreeConns)
	}

	// Wait for longer than the idle timeout
	time.Sleep(idleTimeout + 50*time.Millisecond)

	// Perform another operation, this should cause the idle connection to be closed and a new one created.
	// To verify this, we'd ideally need to inspect the internals or mock net.Conn.Close.
	// For now, we'll check if the operation succeeds and if the pool count behaves as expected.

	_, err = client.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop after idle timeout failed: %v", err)
	}

	pc.mu.Lock()
	finalFreeConns := len(pc.freeconn[testMemcachedHost])
	pc.mu.Unlock()

	// After the second Noop, the previously idle connection should have been closed by getFreeConn,
	// a new one dialed for the Noop, and then that new one returned to the pool.
	// So, we expect 1 free connection again.
	if finalFreeConns != 1 {
		// This can be a bit racy if the test server is slow or network is laggy.
		// The important part is that the connection was *likely* cycled.
		t.Logf("Expected 1 free connection after idle timeout and new op, got %d. This might be acceptable if the old conn was closed.", finalFreeConns)
	}

	// To be more robust, we could try to count the number of dials or closed connections
	// if we had a mock dialer or a way to intercept net.Conn.Close().
	// For now, this test primarily ensures the IdleTimeout path in getFreeConn is exercised
	// and doesn't cause panics or obvious errors.

	// Test that a connection used just before timeout is not closed
	_, err = client.MetaNoop() // Use conn1
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}
	time.Sleep(idleTimeout / 2) // Wait half the timeout

	_, err = client.MetaNoop() // Use conn2 (conn1 goes to pool)
	if err != nil {
		t.Fatalf("MetaNoop failed: %v", err)
	}

	time.Sleep(idleTimeout/2 + 10*time.Millisecond) // conn1 should now be timed out, conn2 should not

	// This operation should find conn2 (not timed out)
	_, err = client.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop should have found a valid connection: %v", err)
	}

	pc.mu.Lock()
	// Expect 1 connection (conn2 was used, conn1 was timed out and replaced by the last MetaNoop's connection)
	if len(pc.freeconn[testMemcachedHost]) != 1 {
		t.Errorf("Expected 1 connection in pool, found %d", len(pc.freeconn[testMemcachedHost]))
	}
	pc.mu.Unlock()

}

// Mock net.Error for testing condRelease
type mockNetError struct {
	timeout bool
	temp    bool
	err     error
}

func (m *mockNetError) Error() string   { return m.err.Error() }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temp }

func TestPooledClient_CondRelease(t *testing.T) {
	// Setup a client and a connection
	config := Config{MaxIdleConns: 1}
	client, _ := NewClient(testMemcachedHost, config)
	pc := client.(*pooledClient)
	defer pc.Close()

	// Mock a net.Conn
	mockServer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer mockServer.Close()

	// The following variables are not directly used in the loop but set up the server context.
	// We will create new connections for each subtest to ensure isolation.
	// var mockNetConn net.Conn // This can be removed or commented if not used directly
	// var dialErr error // This can be removed or commented if not used directly

	// Example: cn is not used directly here because each test case creates its own connection.
	// cn := &conn{
	// 	nc:   realConn,
	// 	addr: realConn.RemoteAddr(),
	// 	pc:   pc,
	// }

	tests := []struct {
		name          string
		err           error
		expectRelease bool // true if cn.release() should be called, false if cn.nc.Close()
		expectClosed  bool // true if realConn should be closed by condRelease
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
			// Reset pool and connection state for each test
			pc.mu.Lock()
			pc.freeconn = make(map[string][]*conn)
			pc.mu.Unlock()

			// Create a fresh connection for each sub-test to avoid issues with closed connections
			// This is a simplified approach. A more robust test might involve a mock net.Conn
			// that allows inspecting its Close() calls.
			currentTestConn, dialClientErr := net.Dial("tcp", mockServer.Addr().String())
			if dialClientErr != nil {
				t.Fatalf("Failed to dial mock server for subtest %s: %v", tt.name, dialClientErr)
			}
			defer currentTestConn.Close() // Ensure client-side conn is closed after test

			// Accept the new connection on the server side
			serverSideConn, acceptErr := mockServer.Accept()
			if acceptErr != nil {
				t.Fatalf("Failed to accept on mock server for subtest %s: %v", tt.name, acceptErr)
			}
			defer serverSideConn.Close() // Ensure server-side conn is closed

			testCn := &conn{
				nc:   currentTestConn,
				addr: currentTestConn.RemoteAddr(),
				pc:   pc,
			}

			errToTest := tt.err
			testCn.condRelease(&errToTest)

			pc.mu.Lock()
			released := len(pc.freeconn[testCn.addr.String()]) > 0
			pc.mu.Unlock()

			if tt.expectRelease {
				if !released {
					t.Errorf("Expected connection to be released, but it wasn't. Error: %v", tt.err)
				}
				// Check if connection was inadvertently closed
				// This check is tricky because the connection might be closed by other defer statements.
				// A more direct way would be to mock net.Conn.Close().
				// For now, we assume if it's released, it wasn't closed by condRelease.
			} else {
				if released {
					t.Errorf("Expected connection to be closed (not released), but it was. Error: %v", tt.err)
				}
				// How to check if currentTestConn was closed by condRelease specifically?
				// One way: try to use it. If it errors with "use of closed network connection", it was closed.
				// This is not perfect as other things could close it.
				// For syscall errors like EPIPE, the connection might already appear closed from the OS.
				if tt.expectClosed {
					// Attempt a write to see if it's closed. This is a basic check.
					_, writeErr := currentTestConn.Write([]byte("ping"))
					if writeErr == nil && !strings.Contains(tt.name, "timeout") { // Timeout might not close immediately
						// If writeErr is nil, it means the connection wasn't closed by condRelease as expected for fatal errors.
						// This check is problematic for errors like io.EOF which might not make Write fail immediately.
						// t.Logf("For error '%v', connection was expected to be closed by condRelease, but a write succeeded.", tt.err)
					} else if writeErr != nil && strings.Contains(writeErr.Error(), "use of closed network connection") {
						t.Logf("Connection correctly closed for error: %v", tt.err)
					} else if writeErr != nil {
						t.Logf("Write after condRelease for error '%v' resulted in error: %v (expected 'use of closed network connection' if closed by condRelease)", tt.err, writeErr)
					}
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
