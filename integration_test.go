package memcache

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

var (
	memcachedHost = "127.0.0.1:11211"
)

func newIntegrationTestConn(t *testing.T) *Conn {
	nc, err := net.DialTimeout("tcp", memcachedHost, time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to memcached at %s: %v. Ensure memcached is running.", memcachedHost, err)
	}

	conn := NewConn(nc)
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("Failed to close connection: %v", err)
		}
	})

	return conn
}

// uniqueKey generates a unique key for testing to avoid collisions.
func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func TestIntegrationMetaSetGetDelete(t *testing.T) {
	conn := newIntegrationTestConn(t)

	key := uniqueKey("test_setgetdel")
	value := []byte("hello integration world")
	flagsSet := []MetaFlag{FlagSetTTL(60)} // Set with a TTL of 60 seconds

	// MetaSet
	resp, err := conn.MetaSet(key, value, flagsSet...)
	if err != nil {
		t.Fatalf("MetaSet(%q) error: %v", key, err)
	}
	if resp.Code != "HD" { // HD is typical for a successful set without special flags like CAS
		t.Errorf("MetaSet(%q) code = %q; want HD", key, resp.Code)
	}

	// MetaGet
	flagsGet := []MetaFlag{FlagReturnValue()}
	gResp, err := conn.MetaGet(key, flagsGet...)
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
	dResp, err := conn.MetaDelete(key)
	if err != nil {
		t.Fatalf("MetaDelete(%q) error: %v", key, err)
	}
	if dResp.Code != "HD" && dResp.Code != "OK" { // Some servers might return OK for delete
		t.Errorf("MetaDelete(%q) code = %q; want HD or OK", key, dResp.Code)
	}

	// MetaGet after delete
	gAfterDeleteResp, err := conn.MetaGet(key, flagsGet...)
	if err != nil {
		// It's possible an error is returned if the key truly doesn't exist,
		// but memcached usually returns "EN"
		// Let's check the code first.
	}
	if gAfterDeleteResp.Code != "EN" {
		t.Errorf("MetaGet(%q) after delete: code = %q, err = %v; want EN", key, gAfterDeleteResp.Code, err)
	}
}

func TestIntegrationMetaArithmetic(t *testing.T) {
	conn := newIntegrationTestConn(t)

	key := uniqueKey("test_arith")
	initialValue := "10"
	initialValueBytes := []byte(initialValue)

	// Set initial value
	sResp, err := conn.MetaSet(key, initialValueBytes, FlagSetTTL(60))
	if err != nil {
		t.Fatalf("MetaSet(%q, %q) for arithmetic error: %v", key, initialValue, err)
	}
	if sResp.Code != "HD" {
		t.Fatalf("MetaSet(%q, %q) for arithmetic code = %q; want HD", key, initialValue, sResp.Code)
	}

	// MetaArithmetic Increment
	// Increment by 5. Expected: 10 + 5 = 15
	incrFlags := []MetaFlag{FlagModeIncr(), FlagDelta(5), FlagReturnValue()}
	aResp, err := conn.MetaArithmetic(key, incrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) error: %v", key, err)
	}
	if aResp.Code != "VA" { // VA indicates value returned
		t.Errorf("MetaArithmetic[Incr](%q) code = %q; want VA", key, aResp.Code)
	}
	valStr := string(aResp.Data)
	valInt, convErr := strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 15 {
		t.Errorf("MetaArithmetic[Incr](%q) data = %d (%q), want 15", key, valInt, valStr)
	}
	if aResp.Value != 15 {
		t.Errorf("MetaArithmetic[Incr](%q) parsed value = %d, want 15", key, aResp.Value)
	}

	// MetaArithmetic Decrement
	// Decrement by 3. Expected: 15 - 3 = 12
	decrFlags := []MetaFlag{FlagModeDecr(), FlagDelta(3), FlagReturnValue()}
	aResp, err = conn.MetaArithmetic(key, decrFlags...)
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
	if valInt != 12 {
		t.Errorf("MetaArithmetic[Decr](%q) data = %d (%q), want 12", key, valInt, valStr)
	}
	if aResp.Value != 12 {
		t.Errorf("MetaArithmetic[Decr](%q) parsed value = %d, want 12", key, aResp.Value)
	}

	// Clean up
	conn.MetaDelete(key)
}

func TestIntegrationMetaNoop(t *testing.T) {
	conn := newIntegrationTestConn(t)

	resp, err := conn.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop() error: %v", err)
	}
	if resp.Code != "MN" {
		t.Errorf("MetaNoop() code = %q; want MN", resp.Code)
	}
}

func TestIntegrationMetaGetMiss(t *testing.T) {
	conn := newIntegrationTestConn(t)

	key := uniqueKey("test_getmiss") // A key that certainly doesn't exist

	flagsGet := []MetaFlag{FlagReturnValue()}
	gResp, err := conn.MetaGet(key, flagsGet...)
	if err != nil {
		// Error is not expected for a simple miss, server should return EN
		t.Fatalf("MetaGet(%q) for miss error: %v", key, err)
	}
	if gResp.Code != "EN" {
		t.Errorf("MetaGet(%q) for miss code = %q; want EN", key, gResp.Code)
	}
}
