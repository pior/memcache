package memcache

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Ensure this matches the host used in integration_test.go or is configurable
var testMemcachedHost = "127.0.0.1:11211"

func TestPooledClient_MetaSetGetDelete(t *testing.T) {
	client, err := NewClient(testMemcachedHost, 2, 5, time.Minute)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	key := fmt.Sprintf("test_pooled_setgetdel_%d", time.Now().UnixNano())
	value := []byte("hello pooled world")
	flagsSet := []MetaFlag{FlagSetTTL(60)}

	// MetaSet
	code, args, err := client.MetaSet(key, value, flagsSet...)
	if err != nil {
		t.Fatalf("MetaSet(%q) error: %v", key, err)
	}
	if code != "HD" {
		t.Errorf("MetaSet(%q) code = %q, args = %v; want HD", key, code, args)
	}

	// MetaGet
	flagsGet := []MetaFlag{FlagReturnValue()}
	gCode, gArgs, gData, err := client.MetaGet(key, flagsGet...)
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
	dCode, _, err := client.MetaDelete(key)
	if err != nil {
		t.Fatalf("MetaDelete(%q) error: %v", key, err)
	}
	if dCode != "HD" && dCode != "OK" {
		t.Errorf("MetaDelete(%q) code = %q; want HD or OK", key, dCode)
	}

	// MetaGet after delete
	gAfterDeleteCode, _, _, err := client.MetaGet(key, flagsGet...)
	if err != nil {
		// Error is not strictly expected by memcached protocol for a miss (EN is expected)
		// but let's log it if it happens.
		t.Logf("MetaGet(%q) after delete returned error: %v", key, err)
	}
	if gAfterDeleteCode != "EN" {
		t.Errorf("MetaGet(%q) after delete: code = %q; want EN", key, gAfterDeleteCode)
	}
}

func TestPooledClient_MetaArithmetic(t *testing.T) {
	client, err := NewClient(testMemcachedHost, 1, 3, time.Minute)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	key := fmt.Sprintf("test_pooled_arith_%d", time.Now().UnixNano())
	initialValue := "100"
	initialValueBytes := []byte(initialValue)

	// Set initial value
	sCode, _, err := client.MetaSet(key, initialValueBytes, FlagSetTTL(60))
	if err != nil {
		t.Fatalf("MetaSet(%q, %q) for arithmetic error: %v", key, initialValue, err)
	}
	if sCode != "HD" {
		t.Fatalf("MetaSet(%q, %q) for arithmetic code = %q; want HD", key, initialValue, sCode)
	}

	// MetaArithmetic Increment
	incrFlags := []MetaFlag{FlagModeIncr(), FlagDelta(5), FlagReturnValue()}
	aCode, _, aData, err := client.MetaArithmetic(key, incrFlags...)
	if err != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) error: %v", key, err)
	}
	if aCode != "VA" {
		t.Errorf("MetaArithmetic[Incr](%q) code = %q; want VA", key, aCode)
	}
	valStr := string(aData)
	valInt, convErr := strconv.Atoi(valStr)
	if convErr != nil {
		t.Fatalf("MetaArithmetic[Incr](%q) Atoi conversion error for %q: %v", key, valStr, convErr)
	}
	if valInt != 105 {
		t.Errorf("MetaArithmetic[Incr](%q) data = %d (%q), want 105", key, valInt, valStr)
	}

	// MetaArithmetic Decrement
	decrFlags := []MetaFlag{FlagModeDecr(), FlagDelta(3), FlagReturnValue()}
	aCode, _, aData, err = client.MetaArithmetic(key, decrFlags...)
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
	if valInt != 102 {
		t.Errorf("MetaArithmetic[Decr](%q) data = %d (%q), want 102", key, valInt, valStr)
	}

	// Clean up
	client.MetaDelete(key)
}

func TestPooledClient_MetaNoop(t *testing.T) {
	client, err := NewClient(testMemcachedHost, 1, 2, time.Minute)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	code, args, err := client.MetaNoop()
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

func TestPooledClient_MetaGetMiss(t *testing.T) {
	client, err := NewClient(testMemcachedHost, 1, 2, time.Minute)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	key := fmt.Sprintf("test_pooled_getmiss_%d", time.Now().UnixNano())

	flagsGet := []MetaFlag{FlagReturnValue()}
	gCode, _, _, err := client.MetaGet(key, flagsGet...)
	if err != nil {
		t.Fatalf("MetaGet(%q) for miss error: %v", key, err)
	}
	if gCode != "EN" {
		t.Errorf("MetaGet(%q) for miss code = %q; want EN", key, gCode)
	}
}

// TestPooledClient_ConcurrentAccess tests concurrent access to the client.
func TestPooledClient_ConcurrentAccess(t *testing.T) {
	// Ensure memcached is running for this test
	// You might need to skip this test if a memcached instance is not available in the CI environment
	if os.Getenv("CI") != "" {
		t.Skip("Skipping concurrent client test in CI environment without guaranteed memcached")
	}

	client, err := NewClient(testMemcachedHost, 5, 20, time.Minute) // Pool with more capacity
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
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
			_, _, errSet := client.MetaSet(key, value, FlagSetTTL(60))
			if errSet != nil {
				t.Errorf("Goroutine %d: MetaSet error: %v", idx, errSet)
				return
			}

			// Get
			_, _, data, errGet := client.MetaGet(key, FlagReturnValue())
			if errGet != nil {
				t.Errorf("Goroutine %d: MetaGet error: %v", idx, errGet)
				return
			}
			if string(data) != string(value) {
				t.Errorf("Goroutine %d: MetaGet data mismatch: got %q, want %q", idx, string(data), string(value))
			}

			// Delete
			_, _, errDel := client.MetaDelete(key)
			if errDel != nil {
				t.Errorf("Goroutine %d: MetaDelete error: %v", idx, errDel)
			}
		}(i)
	}

	wg.Wait()
}
