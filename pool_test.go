package memcache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestPoolGet(t *testing.T) {
	addr := createListener(t, nil)

	config := PoolConfig{
		MinConnections: 1,
		MaxConnections: 3,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool := NewPool(addr, config)
	defer pool.Close()

	// Test that we can use connections from the pool
	var conn1, conn2 *Connection
	err := pool.With(func(conn *Connection) error {
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

	pool := NewPool(addr, DefaultPoolConfig())

	// Close the pool
	pool.Close()

	// Try to use connection from closed pool
	err := pool.With(func(conn *Connection) error {
		return nil
	})
	if err != ErrPoolClosed {
		t.Errorf("Pool.With() on closed pool error = %v, want %v", err, ErrPoolClosed)
	}
}

func TestPoolWith(t *testing.T) {
	addr := createListener(t, statusResponder("EN\r\n"))

	pool := NewPool(addr, DefaultPoolConfig())
	defer pool.Close()

	cmd := NewGetCommand("test")
	ctx := context.Background()

	err := pool.With(func(conn *Connection) error {
		return conn.Execute(ctx, []*protocol.Command{cmd})
	})
	require.NoError(t, err)

	_ = cmd.Wait(ctx)

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

	config := PoolConfig{
		MinConnections: 2,
		MaxConnections: 5,
		ConnTimeout:    time.Second,
		IdleTimeout:    time.Minute,
	}

	pool := NewPool(addr, config)
	defer pool.Close()

	t.Run("no connection", func(t *testing.T) {
		stats := pool.Stats()
		require.Equal(t, PoolStats{
			Address:           addr,
			TotalConnections:  0,
			ActiveConnections: 0,
			TotalInFlight:     0,
		}, stats)
	})

	var commands []*protocol.Command

	t.Run("open one connection", func(t *testing.T) {
		commands = []*protocol.Command{NewNoOpCommand()}

		pool.With(func(conn *Connection) error {
			conn.Execute(ctx, commands)
			return nil
		})

		want := PoolStats{
			Address:           addr,
			TotalConnections:  1,
			ActiveConnections: 1,
			TotalInFlight:     1,
		}
		require.Equal(t, want, pool.Stats())
	})

	t.Run("close one connection", func(t *testing.T) {
		releaseConn <- struct{}{}
		WaitAll(ctx, commands...) // wait for previous command to finish

		want := PoolStats{
			Address:           addr,
			TotalConnections:  1,
			ActiveConnections: 0,
			TotalInFlight:     0,
		}
		require.Equal(t, want, pool.Stats())
	})
}
