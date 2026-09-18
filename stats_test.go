package memcache

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
)

// mockNetConn is a minimal mock for testing
type mockNetConn struct {
	net.Conn
}

func (m *mockNetConn) Close() error {
	return nil
}

func TestConnPoolMetrics(t *testing.T) {
	pool, err := newPuddlePool(func(ctx context.Context) (*Connection, error) {
		return NewConnection(&mockNetConn{}, 0), nil
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Initial stats should be zero
	stats := pool.Metrics()
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

	stats = pool.Metrics()
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

	stats = pool.Metrics()
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

	stats = pool.Metrics()
	if stats.AcquireCount != 2 {
		t.Errorf("Expected AcquireCount=2, got %d", stats.AcquireCount)
	}
	if stats.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1 (reused), got %d", stats.CreatedConns)
	}

	// Destroy the connection. Puddle destroys resources asynchronously, so the
	// counters converge rather than update in step with the call.
	res.Destroy()

	require.Eventually(t, func() bool {
		stats = pool.Metrics()
		return stats.TotalConns == 0 && stats.DestroyedConns == 1
	}, time.Second, time.Millisecond, "destroy must be reflected in the metrics: %+v", stats)
}

func TestClientStats_PoolMetrics(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")

	servers := StaticServers("localhost:11211")
	client := NewClient(servers, Config{
		MaxSize: 5,
		Dialer:  &mockDialer{mockConn, nil},
	})
	defer client.Close()

	ctx := context.Background()

	// Perform some operations to create connections
	_, err := client.Set(ctx, "key1", []byte("value1"))
	if err != nil {
		t.Fatal(err)
	}

	// Check pool stats
	allPoolMetrics := client.PoolMetrics()
	if len(allPoolMetrics) != 1 {
		t.Fatalf("Expected 1 pool, got %d", len(allPoolMetrics))
	}
	conns := allPoolMetrics[0].Conns
	if conns.TotalConns != 1 {
		t.Errorf("Expected TotalConns=1, got %d", conns.TotalConns)
	}
	if conns.CreatedConns != 1 {
		t.Errorf("Expected CreatedConns=1, got %d", conns.CreatedConns)
	}
}

type statsErrorServer struct {
	ln      net.Listener
	mu      sync.Mutex
	conns   []net.Conn
	accepts atomic.Int32
}

func newStatsErrorServer(t *testing.T) *statsErrorServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &statsErrorServer{ln: ln}
	go s.serve()

	t.Cleanup(func() {
		_ = ln.Close()
		s.closeConns()
	})
	return s
}

func (s *statsErrorServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.accepts.Add(1)
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *statsErrorServer) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "stats"):
			_, _ = conn.Write([]byte("STAT pid 1\r\nSERVER_ERROR out of memory\r\nSTAT uptime 2\r\nEND\r\n"))
		default:
			_, _ = conn.Write([]byte("EN\r\n"))
		}
	}
}

func (s *statsErrorServer) closeConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.conns = nil
}

func (s *statsErrorServer) addr() string       { return s.ln.Addr().String() }
func (s *statsErrorServer) acceptCount() int32 { return s.accepts.Load() }

func TestClientStats_DestroysConnectionOnError(t *testing.T) {
	server := newStatsErrorServer(t)
	client := NewClient(StaticServers(server.addr()), Config{
		MaxSize:                1,
		Timeout:                time.Second,
		IdleConnCheckThreshold: -1,
	})
	t.Cleanup(client.Close)

	ctx := context.Background()
	stats, err := client.Stats(ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	var serverErr *meta.ServerError
	require.ErrorAs(t, stats[0].Error, &serverErr)

	item, err := client.Get(ctx, "key")
	require.NoError(t, err)
	require.False(t, item.Found)
	require.Equal(t, int32(2), server.acceptCount(), "a fresh connection should have been established")
}

func TestPool_Exhaustion(t *testing.T) {
	// Create pool with MaxSize=2
	pool, err := newPuddlePool(func(ctx context.Context) (*Connection, error) {
		return NewConnection(&mockNetConn{}, 0), nil
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
		stats := pool.Metrics()
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
			res poolResource
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
			res poolResource
			err error
		}
		acquireResults := make(chan acquireResult, numWaiters)

		for range numWaiters {
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
		for i := range numWaiters {
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
