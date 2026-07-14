package memcache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResource implements poolResource with controllable times, to unit test
// the health check decisions in checkIdleConnections.
type fakeResource struct {
	conn         *Connection
	creationTime time.Time
	idleDuration time.Duration

	destroyed bool
	released  bool
}

func (r *fakeResource) Value() *Connection          { return r.conn }
func (r *fakeResource) Release()                    { r.released = true }
func (r *fakeResource) ReleaseUnused()              { r.released = true }
func (r *fakeResource) Destroy()                    { r.destroyed = true }
func (r *fakeResource) CreationTime() time.Time     { return r.creationTime }
func (r *fakeResource) IdleDuration() time.Duration { return r.idleDuration }

// fakePool implements connPool, handing out a fixed set of idle resources.
type fakePool struct {
	idle []*fakeResource
}

func (p *fakePool) Acquire(ctx context.Context) (poolResource, error) { panic("not used") }
func (p *fakePool) Close()                                            {}
func (p *fakePool) Metrics() ConnPoolMetrics                          { return ConnPoolMetrics{} }

func (p *fakePool) AcquireAllIdle() []poolResource {
	resources := make([]poolResource, len(p.idle))
	for i, r := range p.idle {
		resources[i] = r
	}
	return resources
}

func newFakeResource(responses ...string) *fakeResource {
	mock := testutils.NewConnectionMock(responses...)
	return &fakeResource{
		conn:         NewConnection(mock, time.Second),
		creationTime: time.Now(),
	}
}

func TestCheckIdleConnections(t *testing.T) {
	// The scan is exercised on a ServerPool wired to a fakePool, with only the
	// fields checkIdleConnections reads.
	newServerPool := func(config Config, idle ...*fakeResource) *ServerPool {
		return &ServerPool{
			addr:            "unused:11211",
			pool:            &fakePool{idle: idle},
			maxConnLifetime: config.MaxConnLifetime,
			maxConnIdleTime: config.MaxConnIdleTime,
			pingTimeout:     time.Second,
		}
	}

	t.Run("healthy connection is released", func(t *testing.T) {
		res := newFakeResource("MN\r\n")

		newServerPool(Config{}, res).checkIdleConnections()

		assert.True(t, res.released)
		assert.False(t, res.destroyed)
	})

	t.Run("expired lifetime is destroyed without pinging", func(t *testing.T) {
		res := newFakeResource() // no response available: a ping would fail loudly
		res.creationTime = time.Now().Add(-2 * time.Minute)

		newServerPool(Config{MaxConnLifetime: time.Minute}, res).checkIdleConnections()

		assert.True(t, res.destroyed)
		assert.False(t, res.released)
	})

	t.Run("idle too long is destroyed", func(t *testing.T) {
		res := newFakeResource()
		res.idleDuration = 2 * time.Minute

		newServerPool(Config{MaxConnIdleTime: time.Minute}, res).checkIdleConnections()

		assert.True(t, res.destroyed)
	})

	t.Run("failed ping is destroyed", func(t *testing.T) {
		res := newFakeResource() // empty read buffer -> ping gets EOF

		newServerPool(Config{}, res).checkIdleConnections()

		assert.True(t, res.destroyed)
		assert.False(t, res.released)
	})

	t.Run("within limits is pinged and released", func(t *testing.T) {
		res := newFakeResource("MN\r\n")
		res.creationTime = time.Now().Add(-time.Minute)
		res.idleDuration = time.Minute

		newServerPool(Config{
			MaxConnLifetime: time.Hour,
			MaxConnIdleTime: time.Hour,
		}, res).checkIdleConnections()

		assert.True(t, res.released)
		assert.False(t, res.destroyed)
	})
}

// hungPingTimeout is the ping timeout used by the concurrency tests below.
// Each hung connection blocks its ping for the full timeout, so a sequential
// scan takes numConns × hungPingTimeout while a concurrent one takes about
// one hungPingTimeout; the assertions leave a wide margin between the two.
const hungPingTimeout = 100 * time.Millisecond

// newHungResource returns a resource whose connection never completes any
// I/O: net.Pipe is synchronous and the server end is never read, so a ping
// blocks until the deadline expires.
func newHungResource(t *testing.T) *fakeResource {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return &fakeResource{
		conn:         NewConnection(client, hungPingTimeout),
		creationTime: time.Now(),
	}
}

func TestCheckIdleConnectionsConcurrency(t *testing.T) {
	resources := make([]*fakeResource, 5)
	idle := make([]*fakeResource, len(resources))
	for i := range resources {
		resources[i] = newHungResource(t)
		idle[i] = resources[i]
	}

	sp := &ServerPool{
		addr:        "unused:11211",
		pool:        &fakePool{idle: idle},
		pingTimeout: hungPingTimeout,
	}

	start := time.Now()
	sp.checkIdleConnections()
	elapsed := time.Since(start)

	sequential := time.Duration(len(resources)) * hungPingTimeout
	assert.Less(t, elapsed, sequential/2, "hung connections should be pinged concurrently")
	for i, res := range resources {
		assert.True(t, res.destroyed, "resource %d should be destroyed after its ping timed out", i)
	}
}

func TestRunMaintenancePassConcurrency(t *testing.T) {
	const numPools = 5

	client := &Client{pools: newServerPools()}

	addrs := make([]string, numPools)
	resources := make([]*fakeResource, numPools)
	for i := range numPools {
		addrs[i] = fmt.Sprintf("server-%d:11211", i)
		resources[i] = newHungResource(t)

		_, err := client.pools.getOrCreate(addrs[i], func() (*ServerPool, error) {
			return &ServerPool{
				addr:        addrs[i],
				pool:        &fakePool{idle: []*fakeResource{resources[i]}},
				pingTimeout: hungPingTimeout,
			}, nil
		})
		assert.NoError(t, err)
	}

	// Every pool's address is live, so the reap step leaves them all in place.
	client.servers = StaticServers(addrs...)

	start := time.Now()
	client.runMaintenancePass()
	elapsed := time.Since(start)

	sequential := time.Duration(numPools) * hungPingTimeout
	assert.Less(t, elapsed, sequential/2, "pools should be checked concurrently")
	for i, res := range resources {
		assert.True(t, res.destroyed, "resource %d should be destroyed after its ping timed out", i)
	}
}

func TestIsIOTimeout(t *testing.T) {
	assert.False(t, isIOTimeout(nil))
	assert.False(t, isIOTimeout(errors.New("connection refused")))
	assert.True(t, isIOTimeout(os.ErrDeadlineExceeded))
	assert.True(t, isIOTimeout(fmt.Errorf("read tcp: %w", os.ErrDeadlineExceeded)))
	// A dial cut off by a context deadline is a net.Error timeout without
	// wrapping os.ErrDeadlineExceeded.
	assert.True(t, isIOTimeout(&net.OpError{Op: "dial", Err: context.DeadlineExceeded}))
}

func TestHealthCheck(t *testing.T) {
	errProbeTimeout := fmt.Errorf("read tcp: %w", os.ErrDeadlineExceeded)
	errProbeRefused := errors.New("dial tcp: connect: connection refused")

	// newHealthCheckPool wires a ServerPool with a scripted probe: each call
	// consumes the next error from script, and calls beyond it fail the test.
	// The pool has no idle connections, so the idle scan is a no-op, and its
	// Acquire panics, so a rejected operation provably never reaches it.
	newHealthCheckPool := func(t *testing.T, breakerEnabled bool, script ...error) (*ServerPool, *int) {
		probes := new(int)
		return &ServerPool{
			addr:    "test:11211",
			pool:    &fakePool{},
			breaker: newBreaker("test:11211", BreakerConfig{Enabled: breakerEnabled}),
			probe: func() error {
				require.Less(t, *probes, len(script), "more probes than scripted")
				err := script[*probes]
				*probes++
				return err
			},
		}, probes
	}

	t.Run("answered probe leaves the server unmarked", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true, nil)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 1, *probes)
	})

	t.Run("confirmed timeouts mark the server hung", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true,
			errProbeTimeout, errProbeTimeout, errProbeTimeout)

		sp.healthCheck()

		assert.True(t, sp.hung.Load())
		assert.Equal(t, 1+hungConfirmProbes, *probes)
	})

	t.Run("a timeout blip is not confirmed", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true, errProbeTimeout, nil)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 2, *probes)
	})

	t.Run("fast failure is not a hang", func(t *testing.T) {
		// A refused dial is live traffic's business (counted by the breaker
		// under any caller deadline), so it is not even confirmed.
		sp, probes := newHealthCheckPool(t, true, errProbeRefused)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 1, *probes)
	})

	t.Run("fast failure during confirmation aborts", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true, errProbeTimeout, errProbeRefused)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 2, *probes)
	})

	t.Run("marked server stays marked on a single timeout", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true, errProbeTimeout)
		sp.hung.Store(true)

		sp.healthCheck()

		assert.True(t, sp.hung.Load())
		assert.Equal(t, 1, *probes, "a persistent hang costs one probe per pass")
	})

	t.Run("answered probe clears the marker", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, true, nil)
		sp.hung.Store(true)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 1, *probes)
	})

	t.Run("no breaker, no probes", func(t *testing.T) {
		sp, probes := newHealthCheckPool(t, false)

		sp.healthCheck()

		assert.False(t, sp.hung.Load())
		assert.Equal(t, 0, *probes)
	})

	t.Run("marked server rejects operations before the pool", func(t *testing.T) {
		sp, _ := newHealthCheckPool(t, true)
		sp.hung.Store(true)

		err := sp.Execute(context.Background(), meta.NewRequest(meta.CmdGet, "key", nil), nil)
		assert.EqualError(t, err, "memcache: mg on test:11211: memcache: circuit breaker open")
		assert.ErrorIs(t, err, ErrBreakerOpen)

		_, err = sp.ExecuteBatch(context.Background(), []*meta.Request{meta.NewRequest(meta.CmdGet, "key", nil)})
		assert.EqualError(t, err, "memcache: batch on test:11211: memcache: circuit breaker open")
		assert.ErrorIs(t, err, ErrBreakerOpen)
	})

	t.Run("metrics report the marker", func(t *testing.T) {
		sp, _ := newHealthCheckPool(t, true)
		assert.False(t, sp.Metrics().Breaker.Hung)

		sp.hung.Store(true)
		assert.True(t, sp.Metrics().Breaker.Hung)
		assert.Equal(t, "closed", sp.Metrics().Breaker.State,
			"the marker is independent of the breaker state")
	})
}

// hungMemcachedServer accepts TCP connections and, until woken with
// setResponding, never answers — the signature of a hung memcached. Once
// responding, it answers each request line: a miss (EN) for gets, MN for
// everything else (noop pings).
type hungMemcachedServer struct {
	listener   net.Listener
	responding atomic.Bool
}

func newHungMemcachedServer(t *testing.T) *hungMemcachedServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	server := &hungMemcachedServer{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (s *hungMemcachedServer) serve(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if !s.responding.Load() {
			continue // swallow the request: the caller's read times out
		}
		response := "MN\r\n"
		if strings.HasPrefix(scanner.Text(), "mg") {
			response = "EN\r\n"
		}
		if _, err := conn.Write([]byte(response)); err != nil {
			return
		}
	}
	// The scan ends when the peer closes or resets the connection, both
	// normal here (timed-out operations destroy their connection).
	_ = scanner.Err()
}

// TestHealthCheck_HungServer runs the full lifecycle against a real hung TCP
// server: live operations time out, the maintenance loop marks the server
// hung and operations shed with ErrBreakerOpen, then the server recovers and
// the marker clears without any live traffic needed.
func TestHealthCheck_HungServer(t *testing.T) {
	server := newHungMemcachedServer(t)

	client := NewClient(StaticServers(server.listener.Addr().String()), Config{
		Timeout:             50 * time.Millisecond,
		MaintenanceInterval: 30 * time.Millisecond,
		Breaker:             BreakerConfig{Enabled: true},
	})
	t.Cleanup(client.Close)

	waitForHung := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if client.PoolMetrics()[0].Breaker.Hung == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("hung marker never became %v", want)
	}

	// The server swallows requests: a live operation pays its full budget.
	_, err := client.Get(context.Background(), "key")
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)

	// The maintenance loop confirms the hang and sheds subsequent operations
	// — while the ratio breaker, with no countable evidence, stays closed.
	waitForHung(true)
	_, err = client.Get(context.Background(), "key")
	assert.ErrorIs(t, err, ErrBreakerOpen)
	assert.Equal(t, "closed", client.PoolMetrics()[0].Breaker.State)

	// Recovery needs no live traffic: the next answered probe clears the
	// marker and operations flow again.
	server.responding.Store(true)
	waitForHung(false)

	item, err := client.Get(context.Background(), "key")
	require.NoError(t, err)
	assert.False(t, item.Found)
}
