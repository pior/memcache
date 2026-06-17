package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/stretchr/testify/assert"
)

// fakeResource implements Resource with controllable times, to unit test the
// health check decisions in checkPoolConnections.
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

// fakePool implements Pool, handing out a fixed set of idle resources.
type fakePool struct {
	idle []*fakeResource
}

func (p *fakePool) Acquire(ctx context.Context) (Resource, error) { panic("not used") }
func (p *fakePool) Close()                                        {}
func (p *fakePool) Metrics() ConnPoolMetrics                      { return ConnPoolMetrics{} }

func (p *fakePool) AcquireAllIdle() []Resource {
	resources := make([]Resource, len(p.idle))
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

func TestCheckPoolConnections(t *testing.T) {
	newClientWithConfig := func(config Config) *Client {
		client := NewClient(StaticServers("unused:11211"), config)
		t.Cleanup(client.Close)
		return client
	}

	t.Run("healthy connection is released", func(t *testing.T) {
		client := newClientWithConfig(Config{Timeout: time.Second})
		res := newFakeResource("MN\r\n")

		client.checkPoolConnections(&fakePool{idle: []*fakeResource{res}})

		assert.True(t, res.released)
		assert.False(t, res.destroyed)
	})

	t.Run("expired lifetime is destroyed without pinging", func(t *testing.T) {
		client := newClientWithConfig(Config{Timeout: time.Second, MaxConnLifetime: time.Minute})
		res := newFakeResource() // no response available: a ping would fail loudly
		res.creationTime = time.Now().Add(-2 * time.Minute)

		client.checkPoolConnections(&fakePool{idle: []*fakeResource{res}})

		assert.True(t, res.destroyed)
		assert.False(t, res.released)
	})

	t.Run("idle too long is destroyed", func(t *testing.T) {
		client := newClientWithConfig(Config{Timeout: time.Second, MaxConnIdleTime: time.Minute})
		res := newFakeResource()
		res.idleDuration = 2 * time.Minute

		client.checkPoolConnections(&fakePool{idle: []*fakeResource{res}})

		assert.True(t, res.destroyed)
	})

	t.Run("failed ping is destroyed", func(t *testing.T) {
		client := newClientWithConfig(Config{Timeout: time.Second})
		res := newFakeResource() // empty read buffer -> ping gets EOF

		client.checkPoolConnections(&fakePool{idle: []*fakeResource{res}})

		assert.True(t, res.destroyed)
		assert.False(t, res.released)
	})

	t.Run("within limits is pinged and released", func(t *testing.T) {
		client := newClientWithConfig(Config{
			Timeout:         time.Second,
			MaxConnLifetime: time.Hour,
			MaxConnIdleTime: time.Hour,
		})
		res := newFakeResource("MN\r\n")
		res.creationTime = time.Now().Add(-time.Minute)
		res.idleDuration = time.Minute

		client.checkPoolConnections(&fakePool{idle: []*fakeResource{res}})

		assert.True(t, res.released)
		assert.False(t, res.destroyed)
	})
}
