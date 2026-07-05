package memcache

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/stretchr/testify/assert"
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

func TestCheckAllPoolsConcurrency(t *testing.T) {
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
	client.checkAllPools()
	elapsed := time.Since(start)

	sequential := time.Duration(numPools) * hungPingTimeout
	assert.Less(t, elapsed, sequential/2, "pools should be checked concurrently")
	for i, res := range resources {
		assert.True(t, res.destroyed, "resource %d should be destroyed after its ping timed out", i)
	}
}
