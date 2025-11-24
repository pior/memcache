package memcache

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
)

// mockNetConn is a minimal mock for testing
type mockNetConn struct {
	net.Conn
}

func (m *mockNetConn) Close() error {
	return nil
}

func newMockConn() *conn {
	return &conn{
		Conn:   &mockNetConn{},
		reader: bufio.NewReader(nil),
	}
}

func TestPoolStats_ChannelPool(t *testing.T) {
	pool, err := NewChannelPool(func(ctx context.Context) (*conn, error) {
		return newMockConn(), nil
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Initial stats should be zero
	stats := pool.Stats()
	if stats.TotalConns != 0 {
		t.Errorf("Expected TotalConns=0, got %d", stats.TotalConns)
	}
	if stats.AcquireCount != 0 {
		t.Errorf("Expected AcquireCount=0, got %d", stats.AcquireCount)
	}

	// Acquire a connection
	res, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	stats = pool.Stats()
	if stats.TotalConns != 1 {
		t.Errorf("Expected TotalConns=1, got %d", stats.TotalConns)
	}
	if stats.ActiveConns != 1 {
		t.Errorf("Expected ActiveConns=1, got %d", stats.ActiveConns)
	}
	if stats.IdleConns != 0 {
		t.Errorf("Expected IdleConns=0, got %d", stats.IdleConns)
	}
	if stats.AcquireCount != 1 {
		t.Errorf("Expected AcquireCount=1, got %d", stats.AcquireCount)
	}
	if stats.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1, got %d", stats.CreatedConns)
	}

	// Release the connection
	res.Release()

	stats = pool.Stats()
	if stats.TotalConns != 1 {
		t.Errorf("Expected TotalConns=1, got %d", stats.TotalConns)
	}
	if stats.ActiveConns != 0 {
		t.Errorf("Expected ActiveConns=0, got %d", stats.ActiveConns)
	}
	if stats.IdleConns != 1 {
		t.Errorf("Expected IdleConns=1, got %d", stats.IdleConns)
	}

	// Acquire again (should reuse existing connection)
	res, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	stats = pool.Stats()
	if stats.AcquireCount != 2 {
		t.Errorf("Expected AcquireCount=2, got %d", stats.AcquireCount)
	}
	if stats.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1 (reused), got %d", stats.CreatedConns)
	}

	// Destroy the connection
	res.Destroy()

	stats = pool.Stats()
	if stats.TotalConns != 0 {
		t.Errorf("Expected TotalConns=0, got %d", stats.TotalConns)
	}
	if stats.DestroyedConns != 1 {
		t.Errorf("Expected DestroyedConns=1, got %d", stats.DestroyedConns)
	}
}

func TestPoolStats_AverageWaitTime(t *testing.T) {
	stats := &PoolStats{
		AcquireWaitCount:  3,
		AcquireWaitTimeNs: uint64((100 * time.Millisecond).Nanoseconds()),
	}

	avg := stats.AverageWaitTime()
	expected := 100 * time.Millisecond / 3

	// Allow 1ns tolerance for rounding
	diff := avg - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Nanosecond {
		t.Errorf("Expected average wait time ~%v, got %v", expected, avg)
	}
}

func TestClientStats_HitRate(t *testing.T) {
	stats := &ClientStats{
		CacheHits:   75,
		CacheMisses: 25,
	}

	hitRate := stats.HitRate()
	expected := 0.75

	if hitRate != expected {
		t.Errorf("Expected hit rate %.2f, got %.2f", expected, hitRate)
	}

	// Test zero case
	stats = &ClientStats{}
	hitRate = stats.HitRate()
	if hitRate != 0 {
		t.Errorf("Expected hit rate 0 for no operations, got %.2f", hitRate)
	}
}

func TestClientStats_Operations(t *testing.T) {
	// Create mock with appropriate responses for each operation
	mockConn := testutils.NewConnectionMock(
		"HD\r\n" + // Set response
			"VA 6\r\nvalue1\r\n" + // Get response (hit)
			"EN\r\n" + // Get response (miss)
			"HD\r\n" + // Delete response
			"HD\r\n" + // Add response
			"VA 1\r\n1\r\n", // Increment response
	)

	constructor := func(ctx context.Context) (*conn, error) {
		return &conn{
			Conn:   mockConn,
			reader: bufio.NewReader(mockConn),
		}, nil
	}

	client, err := NewClient("localhost:11211", Config{
		MaxSize:     5,
		constructor: constructor,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test Set
	err = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	if err != nil {
		t.Fatal(err)
	}

	stats := client.Stats()
	if stats.Sets != 1 {
		t.Errorf("Expected Sets=1, got %d", stats.Sets)
	}

	// Test Get (hit)
	_, err = client.Get(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}

	stats = client.Stats()
	if stats.Gets != 1 {
		t.Errorf("Expected Gets=1, got %d", stats.Gets)
	}
	if stats.CacheHits != 1 {
		t.Errorf("Expected CacheHits=1, got %d", stats.CacheHits)
	}

	// Test Get (miss)
	_, err = client.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}

	stats = client.Stats()
	if stats.Gets != 2 {
		t.Errorf("Expected Gets=2, got %d", stats.Gets)
	}
	if stats.CacheMisses != 1 {
		t.Errorf("Expected CacheMisses=1, got %d", stats.CacheMisses)
	}

	// Test Delete
	err = client.Delete(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}

	stats = client.Stats()
	if stats.Deletes != 1 {
		t.Errorf("Expected Deletes=1, got %d", stats.Deletes)
	}

	// Test Add
	err = client.Add(ctx, Item{Key: "key2", Value: []byte("value2")})
	if err != nil {
		t.Fatal(err)
	}

	stats = client.Stats()
	if stats.Adds != 1 {
		t.Errorf("Expected Adds=1, got %d", stats.Adds)
	}

	// Test Increment
	_, err = client.Increment(ctx, "counter", 1, NoTTL)
	if err != nil {
		t.Fatal(err)
	}

	stats = client.Stats()
	if stats.Increments != 1 {
		t.Errorf("Expected Increments=1, got %d", stats.Increments)
	}

	// Test hit rate
	hitRate := stats.HitRate()
	expectedRate := 0.5 // 1 hit out of 2 gets
	if hitRate != expectedRate {
		t.Errorf("Expected hit rate %.2f, got %.2f", expectedRate, hitRate)
	}
}

func TestClientStats_PoolStats(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")

	constructor := func(ctx context.Context) (*conn, error) {
		return &conn{
			Conn:   mockConn,
			reader: bufio.NewReader(mockConn),
		}, nil
	}

	client, err := NewClient("localhost:11211", Config{
		MaxSize:     5,
		constructor: constructor,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Perform some operations to create connections
	err = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	if err != nil {
		t.Fatal(err)
	}

	// Check pool stats
	poolStats := client.PoolStats()
	if poolStats.TotalConns != 1 {
		t.Errorf("Expected TotalConns=1, got %d", poolStats.TotalConns)
	}
	if poolStats.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1, got %d", poolStats.CreatedConns)
	}
}
