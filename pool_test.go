package memcache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestNewPoolConnectionFailure(t *testing.T) {
	// Try to create pool to non-existent address
	config := &PoolConfig{
		MinConnections: 1,
		MaxConnections: 5,
		ConnTimeout:    10 * time.Millisecond,
		IdleTimeout:    time.Minute,
	}

	_, err := NewPool("127.0.0.1:1", config)
	if err == nil {
		t.Error("NewPool() should fail when connecting to non-existent address")
	}
}

func TestPoolGet(t *testing.T) {
	addr := createListener(t, nil)

	config := &PoolConfig{
		MinConnections: 1,
		MaxConnections: 3,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool, err := NewPool(addr, config)
	assertNoError(t, err)
	defer pool.Close()

	// Test that we can use connections from the pool
	var conn1, conn2 *Connection
	err = pool.With(func(conn *Connection) error {
		conn1 = conn
		return nil
	})
	if err != nil {
		t.Fatalf("Pool.With() error = %v", err)
	}

	if conn1 == nil {
		t.Error("Pool.With() provided nil connection")
	}

	// Test another connection usage - should be able to access connections up to max
	err = pool.With(func(conn *Connection) error {
		conn2 = conn
		return nil
	})
	if err != nil {
		t.Fatalf("Pool.With() second call error = %v", err)
	}

	if conn2 == nil {
		t.Error("Pool.With() second call provided nil connection")
	}
}

func TestPoolWithAfterClose(t *testing.T) {
	addr := createListener(t, nil)

	pool, err := NewPool(addr, nil)
	assertNoError(t, err)

	// Close the pool
	pool.Close()

	// Try to use connection from closed pool
	err = pool.With(func(conn *Connection) error {
		return nil
	})
	if err != ErrPoolClosed {
		t.Errorf("Pool.With() on closed pool error = %v, want %v", err, ErrPoolClosed)
	}
}

func TestPoolWith(t *testing.T) {
	addr := createListener(t, statusResponder("EN\r\n"))

	pool, err := NewPool(addr, nil)
	assertNoError(t, err)
	defer pool.Close()

	cmd := NewGetCommand("test")
	ctx := context.Background()

	err = pool.With(func(conn *Connection) error {
		return conn.ExecuteBatch(ctx, []*protocol.Command{cmd})
	})
	assertNoError(t, err)

	_ = WaitAll(ctx, cmd)

	assertResponseStatus(t, cmd, protocol.StatusEN)
	assertResponseErrorIs(t, cmd, protocol.ErrCacheMiss)
}

func TestPoolStats(t *testing.T) {
	ctx := t.Context()

	releaseConn := make(chan struct{})
	defer close(releaseConn)

	addr := createListener(t, func(conn net.Conn) {
		<-releaseConn
	})

	config := &PoolConfig{
		MinConnections: 2,
		MaxConnections: 5,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool, err := NewPool(addr, config)
	assertNoError(t, err)
	defer pool.Close()

	t.Run("no connection", func(t *testing.T) {
		stats := pool.Stats()
		require.Equal(t, PoolStats{
			Address:           addr,
			TotalConnections:  2,
			ActiveConnections: 2,
			TotalInFlight:     0,
		}, stats)
	})

	var commands []*protocol.Command

	t.Run("open one connection", func(t *testing.T) {
		commands = []*protocol.Command{NewNoOpCommand()}

		pool.With(func(conn *Connection) error {
			conn.ExecuteBatch(ctx, commands)
			return nil
		})

		want := PoolStats{
			Address:           addr,
			TotalConnections:  2,
			ActiveConnections: 2,
			TotalInFlight:     1,
		}
		require.Equal(t, want, pool.Stats())
	})

	t.Run("close one connection", func(t *testing.T) {
		releaseConn <- struct{}{}
		WaitAll(ctx, commands...) // wait for previous command to finish

		want := PoolStats{
			Address:           addr,
			TotalConnections:  2,
			ActiveConnections: 1,
			TotalInFlight:     0,
		}
		require.Equal(t, want, pool.Stats())
	})
}
