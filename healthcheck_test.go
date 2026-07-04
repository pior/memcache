package memcache

import (
	"context"
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
