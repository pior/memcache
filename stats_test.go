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

	servers := NewStaticServers("localhost:11211")
	client, err := NewClient(servers, Config{
		MaxSize: 5,
		Dialer:  &mockDialer{mockConn, nil},
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

func TestPool_Exhaustion(t *testing.T) {
	// Create pool with MaxSize=2
	pool, err := NewChannelPool(func(ctx context.Context) (*Connection, error) {
		return NewConnection(&mockNetConn{}), nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	t.Run("timeout when pool exhausted", func(t *testing.T) {
		// Acquire both connections and hold them
		res1, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		res2, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// Verify pool is exhausted
		stats := pool.Stats()
		if stats.TotalConns != 2 {
			t.Errorf("Expected TotalConns=2, got %d", stats.TotalConns)
		}
		if stats.ActiveConns != 2 {
			t.Errorf("Expected ActiveConns=2, got %d", stats.ActiveConns)
		}
		if stats.IdleConns != 0 {
			t.Errorf("Expected IdleConns=0, got %d", stats.IdleConns)
		}

		// Try to acquire third connection with short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		startTime := time.Now()
		_, err = pool.Acquire(ctx)
		waitDuration := time.Since(startTime)

		// Should timeout after ~100ms
		if err != context.DeadlineExceeded {
			t.Errorf("Expected DeadlineExceeded, got %v", err)
		}
		if waitDuration < 90*time.Millisecond {
			t.Errorf("Expected wait duration ~100ms, got %v", waitDuration)
		}

		// Release connections
		res1.Release()
		res2.Release()
	})

	t.Run("request succeeds after connection released", func(t *testing.T) {
		// Acquire both connections
		res1, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		res2, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// Start goroutine to acquire third connection (will wait)
		type acquireResult struct {
			res Resource
			err error
		}
		acquireComplete := make(chan acquireResult, 1)
		go func() {
			res, err := pool.Acquire(context.Background())
			acquireComplete <- acquireResult{res, err}
		}()

		// Give the goroutine time to start waiting
		time.Sleep(50 * time.Millisecond)

		// Release one connection - waiting acquire should succeed
		res1.Release()

		// Verify third acquire succeeded
		select {
		case result := <-acquireComplete:
			if result.err != nil {
				t.Errorf("Expected acquire to succeed, got %v", result.err)
			} else {
				result.res.Release()
			}
		case <-time.After(1 * time.Second):
			t.Error("Acquire did not complete after connection was released")
		}

		// Clean up
		res2.Release()
	})

	t.Run("multiple waiters all succeed", func(t *testing.T) {
		// Acquire both connections
		res1, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		res2, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// Start 2 goroutines that will wait for connections
		const numWaiters = 2
		type acquireResult struct {
			res Resource
			err error
		}
		acquireResults := make(chan acquireResult, numWaiters)

		for i := 0; i < numWaiters; i++ {
			go func() {
				res, err := pool.Acquire(context.Background())
				acquireResults <- acquireResult{res, err}
			}()
		}

		// Give goroutines time to start waiting
		time.Sleep(50 * time.Millisecond)

		// Release both connections - waiters should acquire them
		res1.Release()
		res2.Release()

		// Verify all waiters succeeded and release their connections
		for i := 0; i < numWaiters; i++ {
			select {
			case result := <-acquireResults:
				if result.err != nil {
					t.Errorf("Waiter %d failed: %v", i, result.err)
				} else {
					result.res.Release()
				}
			case <-time.After(1 * time.Second):
				t.Errorf("Waiter %d did not complete", i)
			}
		}
	})
}
