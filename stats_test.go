package memcache

import (
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

func TestPoolStats_ChannelPool(t *testing.T) {
	pool, err := NewChannelPool(func(ctx context.Context) (*Connection, error) {
		return NewConnection(&mockNetConn{}), nil
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

	// Calculate average wait time manually
	var avg time.Duration
	if stats.AcquireWaitCount > 0 {
		avg = time.Duration(stats.AcquireWaitTimeNs / stats.AcquireWaitCount)
	}
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

func TestClientStats_PoolStats(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")

	constructor := func(ctx context.Context) (*Connection, error) {
		return NewConnection(mockConn), nil
	}

	servers := NewStaticServers("localhost:11211")
	client, err := NewClient(servers, Config{
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
	allPoolStats := client.AllPoolStats()
	if len(allPoolStats) != 1 {
		t.Fatalf("Expected 1 pool, got %d", len(allPoolStats))
	}
	poolStats := allPoolStats[0].PoolStats
	if poolStats.TotalConns != 1 {
		t.Errorf("Expected TotalConns=1, got %d", poolStats.TotalConns)
	}
	if poolStats.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1, got %d", poolStats.CreatedConns)
	}
}
