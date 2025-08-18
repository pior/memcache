package memcache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestConnectionClose(t *testing.T) {
	addr := createListener(t, nil)

	conn, err := NewConnection(addr, time.Second)
	require.NoError(t, err)

	// Test that connection is not closed initially
	require.False(t, conn.IsClosed(), "New connection should not be closed")

	// Close the connection
	err = conn.Close()
	require.NoError(t, err, "Close() error")

	// Test that connection is now closed
	require.True(t, conn.IsClosed(), "Connection should be closed after Close()")

	// Test that closing again doesn't error
	err = conn.Close()
	require.NoError(t, err, "Closing an already closed connection should not return an error")
}

func TestConnectionExecuteOnClosedConnection(t *testing.T) {
	addr := createListener(t, nil)

	conn, err := NewConnection(addr, time.Second)
	require.NoError(t, err)

	// Close the connection
	conn.Close()

	// Try to execute command on closed connection
	cmd := NewGetCommand("test")
	ctx := context.Background()

	err = conn.Execute(ctx, []*protocol.Command{cmd})
	require.ErrorIs(t, err, ErrConnectionClosed)
}

func TestConnectionExecuteBatchEmptyCommands(t *testing.T) {
	addr := createListener(t, nil)

	conn, err := NewConnection(addr, time.Second)
	require.NoError(t, err)
	defer conn.Close()

	ctx := context.Background()
	err = conn.Execute(ctx, []*protocol.Command{})
	require.NoError(t, err)
}

func TestConnectionPing(t *testing.T) {
	ctx := context.Background()

	t.Run("ping success", func(t *testing.T) {
		addr := createListener(t, func(conn net.Conn) {
			_, _ = conn.Write([]byte("MN\r\n"))
		})

		conn, err := NewConnection(addr, time.Second)
		require.NoError(t, err)

		defer conn.Close()

		err = conn.Ping(ctx)
		require.NoError(t, err)
	})

	t.Run("ping on closed connection", func(t *testing.T) {
		addr := createListener(t, func(conn net.Conn) {
			conn.Close() // Immediately close the connection
		})

		conn, err := NewConnection(addr, time.Second)
		require.NoError(t, err)

		// Ping should fail because server closes connection
		err = conn.Ping(ctx)
		require.Error(t, err)

		// Connection should be marked as closed after failed ping
		require.True(t, conn.IsClosed())
	})
}

func TestConnectionLastUsed(t *testing.T) {
	addr := createListener(t, nil)

	before := time.Now()

	conn, err := NewConnection(addr, time.Second)
	require.NoError(t, err)

	defer conn.Close()

	after := time.Now()

	lastUsed := conn.LastUsed()
	if lastUsed.Before(before) || lastUsed.After(after) {
		t.Errorf("LastUsed() = %v, want between %v and %v", lastUsed, before, after)
	}
}

func TestConnectionDeadlineHandling(t *testing.T) {
	addr := createListener(t, func(conn net.Conn) {
		buf := make([]byte, 1024)
		for {
			// Set a short read timeout to avoid hanging
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, err := conn.Read(buf)
			if err != nil {
				return
			}

			conn.Write([]byte("EN\r\n")) // cache miss
		}
	})

	conn, err := NewConnection(addr, time.Second)
	require.NoError(t, err)

	defer conn.Close()

	t.Run("ContextWithDeadline", func(t *testing.T) {
		// Test with context that has a deadline
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Create a simple command
		command := NewGetCommand("test_key")

		// ExecuteBatch should succeed and set deadline on connection
		err := conn.Execute(ctx, []*protocol.Command{command})
		require.NoError(t, err)
	})

	t.Run("ContextWithoutDeadline", func(t *testing.T) {
		// Test with context that has no deadline
		ctx := context.Background()

		// Create a simple command
		command := NewGetCommand("test_key2")

		// ExecuteBatch should succeed and clear deadline on connection
		err := conn.Execute(ctx, []*protocol.Command{command})
		require.NoError(t, err)
	})

	t.Run("AlternatingContexts", func(t *testing.T) {
		// Test alternating between contexts with and without deadlines
		// This simulates the real-world scenario where deadline behavior was broken

		// First use context with deadline
		ctxWithDeadline, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()

		command1 := NewGetCommand("test_key3")
		err := conn.Execute(ctxWithDeadline, []*protocol.Command{command1})
		require.NoError(t, err)

		// Then use context without deadline - this should clear the previous deadline
		ctxWithoutDeadline := context.Background()

		command2 := NewGetCommand("test_key4")
		err = conn.Execute(ctxWithoutDeadline, []*protocol.Command{command2})
		require.NoError(t, err)

		// Use context with deadline again
		ctxWithDeadline2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()

		command3 := NewGetCommand("test_key5")
		err = conn.Execute(ctxWithDeadline2, []*protocol.Command{command3})
		require.NoError(t, err)
	})
}
