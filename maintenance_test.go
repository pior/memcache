package memcache

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
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

// hungPingTimeout is the connection operation timeout used by the concurrency
// tests below. Each hung connection blocks its ping for the full timeout, so a
// sequential scan takes numConns × hungPingTimeout while a concurrent one takes
// about one hungPingTimeout; the assertions leave a wide margin between the two.
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
		addr: "unused:11211",
		pool: &fakePool{idle: idle},
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

// probeStub returns a probe that counts its calls and returns err.
func probeStub(err error) (calls *atomic.Int32, probe func() error) {
	var n atomic.Int32
	return &n, func() error {
		n.Add(1)
		return err
	}
}

func TestHealthCheck(t *testing.T) {
	// The burst size matches the trip policy's volume floor, as NewServerPool
	// wires it.
	const tripProbes = 3

	breakerOn := BreakerConfig{Enabled: true, TripMinRequests: tripProbes}

	newServerPool := func(breakerConfig BreakerConfig, probe func() error, idle ...*fakeResource) *ServerPool {
		return &ServerPool{
			addr:       "unused:11211",
			pool:       &fakePool{idle: idle},
			breaker:    newBreaker("unused:11211", breakerConfig),
			probe:      probe,
			hungProbes: tripProbes,
		}
	}

	t.Run("answered idle ping ends the pass", func(t *testing.T) {
		calls, probe := probeStub(nil)
		res := newFakeResource("MN\r\n")

		sp := newServerPool(breakerOn, probe, res)
		sp.healthCheck()

		assert.True(t, res.released)
		assert.Equal(t, int32(0), calls.Load())
		assert.Equal(t, "closed", sp.Metrics().Breaker.State)
		assert.Equal(t, uint32(0), sp.Metrics().Breaker.Requests)
	})

	t.Run("hung idle connection is confirmed and trips the breaker", func(t *testing.T) {
		calls, probe := probeStub(os.ErrDeadlineExceeded)
		res := newHungResource(t)

		sp := newServerPool(breakerOn, probe, res)
		sp.healthCheck()

		assert.True(t, res.destroyed)
		assert.Equal(t, int32(tripProbes), calls.Load())
		assert.Equal(t, "open", sp.Metrics().Breaker.State)
	})

	t.Run("connection reset while idle is not hung-server evidence", func(t *testing.T) {
		calls, probe := probeStub(nil) // the server answers a fresh probe
		res := newFakeResource()       // empty read buffer -> ping fails fast with EOF

		sp := newServerPool(breakerOn, probe, res)
		sp.healthCheck()

		assert.True(t, res.destroyed)
		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, "closed", sp.Metrics().Breaker.State)
	})

	t.Run("empty pool with hung server trips the breaker", func(t *testing.T) {
		calls, probe := probeStub(os.ErrDeadlineExceeded)

		sp := newServerPool(breakerOn, probe)
		sp.healthCheck()

		assert.Equal(t, "open", sp.Metrics().Breaker.State)
		// One suspicion probe plus the burst — minus any burst probe the
		// breaker rejected because the others already tripped it.
		assert.GreaterOrEqual(t, calls.Load(), int32(tripProbes))
		assert.LessOrEqual(t, calls.Load(), int32(tripProbes+1))
	})

	t.Run("empty pool with healthy server records one success", func(t *testing.T) {
		calls, probe := probeStub(nil)

		sp := newServerPool(breakerOn, probe)
		sp.healthCheck()

		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, "closed", sp.Metrics().Breaker.State)
		assert.Equal(t, uint32(1), sp.Metrics().Breaker.TotalSuccesses)
	})

	t.Run("false alarm records successes and stays closed", func(t *testing.T) {
		calls, probe := probeStub(nil)
		res := newHungResource(t) // one hung idle connection, but fresh probes answer

		sp := newServerPool(breakerOn, probe, res)
		sp.healthCheck()

		assert.True(t, res.destroyed)
		assert.Equal(t, int32(tripProbes), calls.Load())
		assert.Equal(t, "closed", sp.Metrics().Breaker.State)
		assert.Equal(t, uint32(tripProbes), sp.Metrics().Breaker.TotalSuccesses)
	})

	t.Run("no breaker means no probing", func(t *testing.T) {
		calls, probe := probeStub(nil)
		res := newHungResource(t)

		sp := newServerPool(BreakerConfig{}, probe, res)
		sp.healthCheck()

		assert.True(t, res.destroyed)
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("open, half-open and recovery lifecycle", func(t *testing.T) {
		const openDuration = 20 * time.Millisecond
		config := BreakerConfig{
			Enabled:         true,
			TripMinRequests: tripProbes,
			OpenDuration:    openDuration,
		}
		calls, failing := probeStub(os.ErrDeadlineExceeded)

		sp := newServerPool(config, failing)
		sp.healthCheck()
		require.Equal(t, "open", sp.Metrics().Breaker.State)

		// While the breaker is open, probes are rejected without running.
		before := calls.Load()
		sp.healthCheck()
		assert.Equal(t, before, calls.Load())

		// Half-open with the server still hung: the single probe runs, fails,
		// and reopens the breaker.
		time.Sleep(openDuration + 10*time.Millisecond)
		sp.healthCheck()
		assert.Equal(t, before+1, calls.Load())
		assert.Equal(t, "open", sp.Metrics().Breaker.State)

		// Half-open with the server recovered: the probe closes the breaker
		// without waiting for live traffic.
		_, healthy := probeStub(nil)
		sp.probe = healthy
		time.Sleep(openDuration + 10*time.Millisecond)
		sp.healthCheck()
		assert.Equal(t, "closed", sp.Metrics().Breaker.State)
	})
}

// TestHealthCheck_HungServer exercises the real probe path end to end: a
// server that accepts connections but never responds produces only timeouts,
// which live traffic attributes to caller deadlines; the maintenance health
// check probes with the operator's own deadlines and must open the breaker.
func TestHealthCheck_HungServer(t *testing.T) {
	addr := newHungServer(t)

	sp, err := NewServerPool(addr, Config{
		Dialer:  &net.Dialer{},
		MaxSize: 2,
		Timeout: 50 * time.Millisecond,
		Breaker: BreakerConfig{Enabled: true, TripMinRequests: 4},
	})
	require.NoError(t, err)
	t.Cleanup(sp.Close)

	sp.healthCheck()

	assert.Equal(t, "open", sp.Metrics().Breaker.State)
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
				addr: addrs[i],
				pool: &fakePool{idle: []*fakeResource{resources[i]}},
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
