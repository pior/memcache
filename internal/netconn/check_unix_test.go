//go:build unix

package netconn

import (
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// loopbackPair returns both ends of a loopback TCP connection, so a test can
// probe the client end while closing, resetting or writing to the server end.
func loopbackPair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()

	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	type accepted struct {
		conn *net.TCPConn
		err  error
	}
	accepts := make(chan accepted, 1)
	go func() {
		c, err := ln.AcceptTCP()
		accepts <- accepted{c, err}
	}()

	client, err = net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	a := <-accepts
	require.NoError(t, a.err)
	t.Cleanup(func() { _ = a.conn.Close() })

	return client, a.conn
}

// eventually polls CheckIdle until it returns an error matching target: what
// the peer does reaches the client socket asynchronously.
func eventually(t *testing.T, conn net.Conn, target error) {
	t.Helper()
	require.Eventually(t, func() bool {
		return errors.Is(CheckIdle(conn), target)
	}, time.Second, 5*time.Millisecond, "CheckIdle never returned %v", target)
}

func TestCheckIdle(t *testing.T) {
	t.Run("quiet open connection is healthy", func(t *testing.T) {
		client, _ := loopbackPair(t)

		for range 3 {
			require.NoError(t, CheckIdle(client))
		}
	})

	t.Run("peer close is reported as EOF", func(t *testing.T) {
		client, server := loopbackPair(t)
		require.NoError(t, server.Close())

		eventually(t, client, io.EOF)
	})

	t.Run("peer reset is reported as the socket error", func(t *testing.T) {
		client, server := loopbackPair(t)
		// A zero linger makes Close send a RST instead of a FIN.
		require.NoError(t, server.SetLinger(0))
		require.NoError(t, server.Close())

		eventually(t, client, syscall.ECONNRESET)
	})

	t.Run("unsolicited data is reported as unexpected read", func(t *testing.T) {
		client, server := loopbackPair(t)
		_, err := server.Write([]byte("X"))
		require.NoError(t, err)

		eventually(t, client, ErrUnexpectedRead)
	})

	t.Run("connection without a raw fd is trusted", func(t *testing.T) {
		// A net.Pipe conn does not implement syscall.Conn, standing in for a
		// *tls.Conn: it cannot be peeked, so it is reported healthy.
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		require.NoError(t, server.Close())

		require.NoError(t, CheckIdle(client))
	})
}
