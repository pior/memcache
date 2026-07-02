//go:build unix

package memcache

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// dialLoopback returns a *Connection wrapping the client end of a loopback TCP
// connection, plus the raw server end so a test can close it or push bytes to
// simulate a dead or desynchronized peer.
func dialLoopback(t *testing.T) (*Connection, net.Conn) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	type accepted struct {
		conn net.Conn
		err  error
	}
	accepts := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		accepts <- accepted{c, err}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientConn.Close() })

	a := <-accepts
	require.NoError(t, a.err)
	t.Cleanup(func() { _ = a.conn.Close() })

	return NewConnection(clientConn, 0), a.conn
}

func TestConnection_checkAlive(t *testing.T) {
	t.Run("healthy idle connection passes", func(t *testing.T) {
		conn, _ := dialLoopback(t)

		// A quiet, open connection has nothing to read: the probe returns nil,
		// repeatably.
		for range 3 {
			require.NoError(t, conn.checkAlive())
		}
	})

	t.Run("peer closed is detected", func(t *testing.T) {
		conn, server := dialLoopback(t)
		require.NoError(t, server.Close())

		// The FIN arrives asynchronously; once it lands the probe reports the
		// connection dead and keeps doing so.
		require.Eventually(t, func() bool {
			return conn.checkAlive() != nil
		}, time.Second, 5*time.Millisecond)
	})

	t.Run("unsolicited data is treated as desync", func(t *testing.T) {
		conn, server := dialLoopback(t)
		_, err := server.Write([]byte("X"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return errors.Is(conn.checkAlive(), errUnexpectedRead)
		}, time.Second, 5*time.Millisecond)
	})

	t.Run("buffered bytes are treated as desync", func(t *testing.T) {
		conn, server := dialLoopback(t)
		_, err := server.Write([]byte("leftover\r\n"))
		require.NoError(t, err)

		// Pull the bytes off the socket into the bufio.Reader. The raw peek
		// would miss them, so checkAlive must catch them via Buffered().
		_, err = conn.Reader.Peek(1)
		require.NoError(t, err)

		require.ErrorIs(t, conn.checkAlive(), errUnexpectedRead)
	})

	t.Run("non-syscall connection is trusted", func(t *testing.T) {
		// A net.Pipe conn does not implement syscall.Conn, standing in for a
		// *tls.Conn: it cannot be peeked, so it is reported healthy.
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		t.Cleanup(func() { _ = server.Close() })

		conn := NewConnection(client, 0)
		require.NoError(t, conn.checkAlive())
	})
}

// livenessServer is a minimal in-process memcached that answers meta-get with a
// miss. A test can close the connections it has accepted to simulate a
// server-side close (a restart, or an LB/middlebox reset) while they sit idle in
// the client pool, and can read how many connections were established.
type livenessServer struct {
	ln      net.Listener
	mu      sync.Mutex
	conns   []net.Conn
	accepts atomic.Int32
}

func newLivenessServer(t *testing.T) *livenessServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &livenessServer{ln: ln}
	go s.serve()

	t.Cleanup(func() {
		_ = ln.Close()
		s.closeConns()
	})
	return s
}

func (s *livenessServer) serve() {
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

func (s *livenessServer) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "mn"):
			_, _ = conn.Write([]byte("MN\r\n"))
		default: // mg and anything else: report a miss
			_, _ = conn.Write([]byte("EN\r\n"))
		}
	}
}

func (s *livenessServer) closeConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
}

func (s *livenessServer) addr() string       { return s.ln.Addr().String() }
func (s *livenessServer) acceptCount() int32 { return s.accepts.Load() }

func TestServerPool_acquireHealthy_replacesDeadIdleConn(t *testing.T) {
	server := newLivenessServer(t)

	client := NewClient(StaticServers(server.addr()), Config{
		MaxSize:                1,
		Timeout:                time.Second,
		IdleConnCheckThreshold: time.Millisecond,
	})
	t.Cleanup(client.Close)

	ctx := context.Background()

	// First op establishes and pools a single connection.
	_, err := client.Get(ctx, "key")
	require.NoError(t, err)
	require.Equal(t, int32(1), server.acceptCount())

	// The server closes the pooled connection, as in a rolling restart.
	server.closeConns()

	// Let the FIN land and the idle threshold elapse. On the next
	// checkout the probe finds the connection dead, discards it, and a
	// fresh one is dialed transparently — the op succeeds.
	time.Sleep(50 * time.Millisecond)

	_, err = client.Get(ctx, "key")
	require.NoError(t, err)
	require.Equal(t, int32(2), server.acceptCount(), "a fresh connection should have been established")
}

// When the probe is not in play, a connection that died while idle is handed out
// and the operation fails — the behaviour the on-acquire check exists to prevent.
func TestServerPool_acquireHealthy_trustedDeadConnFailsOp(t *testing.T) {
	cases := map[string]time.Duration{
		"disabled":             -1,
		"idle below threshold": time.Hour,
	}

	for name, threshold := range cases {
		t.Run(name, func(t *testing.T) {
			server := newLivenessServer(t)

			client := NewClient(StaticServers(server.addr()), Config{
				MaxSize:                1,
				Timeout:                time.Second,
				IdleConnCheckThreshold: threshold,
			})
			t.Cleanup(client.Close)

			ctx := context.Background()

			_, err := client.Get(ctx, "key")
			require.NoError(t, err)

			server.closeConns()
			time.Sleep(50 * time.Millisecond)

			_, err = client.Get(ctx, "key")
			require.Error(t, err)
		})
	}
}

// MaxConnLifetime and MaxConnIdleTime are enforced when a connection is
// checked out, independently of the liveness probe (disabled here) and of the
// health check loop (not running here).
func TestServerPool_acquireHealthy_enforcesConnLimits(t *testing.T) {
	runTwoGets := func(t *testing.T, config Config) *livenessServer {
		t.Helper()
		server := newLivenessServer(t)

		config.MaxSize = 1
		config.Timeout = time.Second
		config.IdleConnCheckThreshold = -1 // prove the limits act on their own
		client := NewClient(StaticServers(server.addr()), config)
		t.Cleanup(client.Close)

		ctx := context.Background()

		// First op establishes and pools a single connection.
		_, err := client.Get(ctx, "key")
		require.NoError(t, err)
		require.Equal(t, int32(1), server.acceptCount())

		time.Sleep(60 * time.Millisecond)

		_, err = client.Get(ctx, "key")
		require.NoError(t, err)
		return server
	}

	t.Run("connection past MaxConnLifetime is replaced", func(t *testing.T) {
		server := runTwoGets(t, Config{MaxConnLifetime: 20 * time.Millisecond})
		require.Equal(t, int32(2), server.acceptCount(), "an expired connection must be replaced at checkout")
	})

	t.Run("connection past MaxConnIdleTime is replaced", func(t *testing.T) {
		server := runTwoGets(t, Config{MaxConnIdleTime: 20 * time.Millisecond})
		require.Equal(t, int32(2), server.acceptCount(), "an idled-out connection must be replaced at checkout")
	})

	t.Run("connection within limits is reused", func(t *testing.T) {
		server := runTwoGets(t, Config{MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Hour})
		require.Equal(t, int32(1), server.acceptCount(), "a connection within its limits must be reused")
	})
}

func TestConfig_idleConnCheckThresholdDefault(t *testing.T) {
	server := newLivenessServer(t)

	// A zero value must resolve to the default rather than staying disabled.
	client := NewClient(StaticServers(server.addr()), Config{MaxSize: 1})
	t.Cleanup(client.Close)

	require.Equal(t, defaultIdleConnCheckThreshold, client.config.IdleConnCheckThreshold)
}
