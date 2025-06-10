package memcache

import (
	"context"
	"errors" // Added for errors.As
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ensure this matches the host used in integration_test.go or is configurable
var testMemcachedHost = "127.0.0.1:11211"

func newTestClient(t *testing.T, initialConns, maxConns int) Client {
	config := Config{
		Address:      testMemcachedHost,
		InitialConns: initialConns,
		MaxConns:     maxConns,
		DialTimeout:  5 * time.Second,
		IdleTimeout:  time.Minute, // Informational for fatih/pool NewChannelPool
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	return client
}

func TestPooledClient_MetaSetGetDelete(t *testing.T) {
	client := newTestClient(t, 2, 5)
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
	client := newTestClient(t, 1, 3)
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
	client := newTestClient(t, 1, 2)
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
	client := newTestClient(t, 1, 2)
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

	client := newTestClient(t, 5, 20) // Pool with more capacity
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
		Address:      testMemcachedHost,
		InitialConns: 1,
		MaxConns:     1,
		DialTimeout:  2 * time.Second,
		DialFunc:     customDialFunc,
	}

	client, err := NewClient(config)
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
		Address:      nonRoutableAddress,
		InitialConns: 1,
		MaxConns:     1,
		DialTimeout:  100 * time.Millisecond, // Very short timeout
	}

	client, err := NewClient(config)
	if err != nil {
		// This error is from pool creation, which might try to make an initial connection
		// depending on the pool library's behavior with InitialConns > 0.
		// fatih/pool with NewChannelPool and InitialConns > 0 will try to create them upfront.
		t.Logf("NewClient failed as expected due to dial timeout during pool init: %v", err)
		// Check if the error is a timeout error or wraps a timeout error
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			// This is a timeout error, as expected.
			return // Test ends here if pool creation fails due to timeout
		} else {
			// Check if it's wrapped in a pool error
			errStr := err.Error()
			if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout") {
				return // Test ends here if pool creation fails due to timeout
			}
			t.Errorf("Expected a timeout error during NewClient, got: %v", err)
		}
		return // Test ends here if pool creation fails due to timeout
	}
	defer client.Close()

	// If NewClient succeeded (e.g., pool doesn't dial upfront or initialConns was 0),
	// then an operation should fail with a timeout.
	startTime := time.Now()
	_, err = client.MetaNoop() // This should trigger a dial and timeout
	duration := time.Since(startTime)

	if err == nil {
		t.Fatalf("MetaNoop did not return an error when a dial timeout was expected")
	}

	netErr, ok := err.(net.Error)
	if !ok || !netErr.Timeout() {
		t.Errorf("Expected a timeout error, got: %v", err)
	}

	// Check if the operation timed out roughly within the DialTimeout duration
	// Allow some buffer for test execution overhead.
	if duration > config.DialTimeout+(200*time.Millisecond) {
		t.Errorf("Operation took too long (%v), expected to timeout near %v", duration, config.DialTimeout)
	}
	t.Logf("Dial timed out as expected in %v for MetaNoop. Error: %v", duration, err)
}
