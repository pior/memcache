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
	return NewConn(nc)
}

// uniqueKey generates a unique key for testing to avoid collisions.
func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func TestIntegrationMetaSetGetDelete(t *testing.T) {
	conn := newIntegrationTestConn(t)
	defer conn.Close()

	key := uniqueKey("test_setgetdel")
	value := []byte("hello integration world")
	flagsSet := []MetaFlag{FlagSetTTL(60)} // Set with a TTL of 60 seconds

	// MetaSet
	code, args, err := conn.MetaSet(key, value, flagsSet...)
	if err != nil {
		t.Fatalf("MetaSet(%q) error: %v", key, err)
	}
	if code != "HD" { // HD is typical for a successful set without special flags like CAS
		t.Errorf("MetaSet(%q) code = %q, args = %v; want HD", key, code, args)
	}

	// MetaGet
	flagsGet := []MetaFlag{FlagReturnValue()}
	gCode, gArgs, gData, err := conn.MetaGet(key, flagsGet...)
	if err != nil {
		t.Fatalf("MetaGet(%q) error: %v", key, err)
	}
	if gCode != "VA" {
		t.Errorf("MetaGet(%q) code = %q, want VA", key, gCode)
	}
	if len(gArgs) < 1 || fmt.Sprint(len(value)) != gArgs[0] {
		t.Errorf("MetaGet(%q) value length arg = %v, want %d", key, gArgs, len(value))
	}
	if string(gData) != string(value) {
		t.Errorf("MetaGet(%q) data = %q, want %q", key, string(gData), string(value))
	}

	// MetaDelete
	dCode, _, err := conn.MetaDelete(key)
	if err != nil {
		t.Fatalf("MetaDelete(%q) error: %v", key, err)
	}
	if dCode != "HD" && dCode != "OK" { // Some servers might return OK for delete
		t.Errorf("MetaDelete(%q) code = %q; want HD or OK", key, dCode)
	}

	// MetaGet after delete
	gAfterDeleteCode, _, _, err := conn.MetaGet(key, flagsGet...)
	if err != nil {
		// It's possible an error is returned if the key truly doesn't exist,
		// but memcached usually returns "EN"
		// Let's check the code first.
	}
	if gAfterDeleteCode != "EN" {
		t.Errorf("MetaGet(%q) after delete: code = %q, err = %v; want EN", key, gAfterDeleteCode, err)
	}
}

func TestIntegrationMetaArithmetic(t *testing.T) {
	conn := newIntegrationTestConn(t)
	defer conn.Close()

	key := uniqueKey("test_arith")
	initialValue := "10"
	initialValueBytes := []byte(initialValue)

	// Set initial value
	sCode, _, err := conn.MetaSet(key, initialValueBytes, FlagSetTTL(60))
	if err != nil {
		t.Fatalf("MetaSet(%q, %q) for arithmetic error: %v", key, initialValue, err)
	}
	if sCode != "HD" {
		t.Fatalf("MetaSet(%q, %q) for arithmetic code = %q; want HD", key, initialValue, sCode)
	}

	// MetaArithmetic Increment
	// Increment by 5. Expected: 10 + 5 = 15
	incrFlags := []MetaFlag{FlagModeIncr(), FlagDelta(5), FlagReturnValue()}
	aCode, _, aData, err := conn.MetaArithmetic(key, incrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) error: %v", key, err)
	}
	if aCode != "VA" { // VA indicates value returned
		t.Errorf("MetaArithmetic[Incr](%q) code = %q; want VA", key, aCode)
	}
	valStr := string(aData)
	valInt, convErr := strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 15 {
		t.Errorf("MetaArithmetic[Incr](%q) data = %d (%q), want 15", key, valInt, valStr)
	}

	// MetaArithmetic Decrement
	// Decrement by 3. Expected: 15 - 3 = 12
	decrFlags := []MetaFlag{FlagModeDecr(), FlagDelta(3), FlagReturnValue()}
	aCode, _, aData, err = conn.MetaArithmetic(key, decrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Decr](%q) error: %v", key, err)
	}
	if aCode != "VA" {
		t.Errorf("MetaArithmetic[Decr](%q) code = %q; want VA", key, aCode)
	}
	valStr = string(aData)
	valInt, convErr = strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Decr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 12 {
		t.Errorf("MetaArithmetic[Decr](%q) data = %d (%q), want 12", key, valInt, valStr)
	}

	// Clean up
	conn.MetaDelete(key)
}

func TestIntegrationMetaNoop(t *testing.T) {
	conn := newIntegrationTestConn(t)
	defer conn.Close()

	code, args, err := conn.MetaNoop()
	if err != nil {
		t.Fatalf("MetaNoop() error: %v", err)
	}
	if code != "MN" {
		t.Errorf("MetaNoop() code = %q, args = %v; want MN", code, args)
	}
	if len(args) != 0 {
		t.Errorf("MetaNoop() args = %v; want empty", args)
	}
}

func TestIntegrationMetaGetMiss(t *testing.T) {
	conn := newIntegrationTestConn(t)
	defer conn.Close()

	key := uniqueKey("test_getmiss") // A key that certainly doesn't exist

	flagsGet := []MetaFlag{FlagReturnValue()}
	gCode, _, _, err := conn.MetaGet(key, flagsGet...)
	if err != nil {
		// Error is not expected for a simple miss, server should return EN
		t.Fatalf("MetaGet(%q) for miss error: %v", key, err)
	}
	if gCode != "EN" {
		t.Errorf("MetaGet(%q) for miss code = %q; want EN", key, gCode)
	}
}
