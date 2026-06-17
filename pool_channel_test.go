package memcache

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idleNetConn is a net.Conn stub whose Read blocks forever, for pool tests
// that never perform I/O.
type idleNetConn struct{}

func (idleNetConn) Read(b []byte) (int, error)         { select {} }
func (idleNetConn) Write(b []byte) (int, error)        { return len(b), nil }
func (idleNetConn) Close() error                       { return nil }
func (idleNetConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (idleNetConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (idleNetConn) SetDeadline(t time.Time) error      { return nil }
func (idleNetConn) SetReadDeadline(t time.Time) error  { return nil }
func (idleNetConn) SetWriteDeadline(t time.Time) error { return nil }

func newIdleChannelPool(t *testing.T, maxSize int32) Pool {
	t.Helper()
	pool, err := NewChannelPool(func(ctx context.Context) (*Connection, error) {
		return NewConnection(idleNetConn{}, 0), nil
	}, maxSize)
	require.NoError(t, err)
	return pool
}

func TestChannelPool_AcquireAfterClose(t *testing.T) {
	pool := newIdleChannelPool(t, 2)
	pool.Close()

	res, err := pool.Acquire(context.Background())
	assert.ErrorIs(t, err, ErrPoolClosed)
	assert.Nil(t, res)
}

func TestChannelPool_CloseIsIdempotent(t *testing.T) {
	pool := newIdleChannelPool(t, 2)
	pool.Close()
	pool.Close() // must not panic
}

func TestChannelPool_ReleaseAfterClose(t *testing.T) {
	pool := newIdleChannelPool(t, 2)

	res, err := pool.Acquire(context.Background())
	require.NoError(t, err)

	pool.Close()
	res.Release() // must not panic (send on closed channel)

	assert.Equal(t, int32(0), pool.Metrics().TotalConns, "all connections destroyed")
}

func TestChannelPool_CloseUnblocksWaitingAcquire(t *testing.T) {
	pool := newIdleChannelPool(t, 1)

	// Exhaust the pool.
	res, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	defer res.Release()

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(context.Background())
		acquireErr <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the goroutine block in Acquire
	pool.Close()

	select {
	case err := <-acquireErr:
		assert.ErrorIs(t, err, ErrPoolClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire still blocked after pool Close")
	}
}

// Concurrent acquire/release/close must not panic or race.
func TestChannelPool_CloseRace(t *testing.T) {
	for range 50 {
		pool := newIdleChannelPool(t, 4)

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := pool.Acquire(context.Background())
				if err != nil {
					return
				}
				if res == nil {
					t.Error("Acquire returned nil resource with nil error")
					return
				}
				res.Release()
			}()
		}
		pool.Close()
		wg.Wait()
	}
}

func TestChannelPool_AcquireAllIdle(t *testing.T) {
	pool := newIdleChannelPool(t, 4)
	t.Cleanup(pool.Close)

	// Create two connections and return them to the idle channel.
	res1, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	res2, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	res1.Release()
	res2.Release()

	idle := pool.AcquireAllIdle()
	assert.Len(t, idle, 2)

	// Resource accessors work and the resources can go back unused.
	for _, res := range idle {
		assert.NotNil(t, res.Value())
		assert.False(t, res.CreationTime().IsZero())
		assert.GreaterOrEqual(t, res.IdleDuration(), time.Duration(0))
		res.ReleaseUnused()
	}

	assert.Len(t, pool.AcquireAllIdle(), 2, "resources must be back in the pool")
}

func TestChannelPool_DestroyRemovesFromPool(t *testing.T) {
	pool := newIdleChannelPool(t, 2)
	t.Cleanup(pool.Close)

	res, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	res.Destroy()

	stats := pool.Metrics()
	assert.Equal(t, uint64(1), stats.DestroyedConns)
	assert.Equal(t, int32(0), stats.TotalConns)
}

// Gauges must stay consistent through a health-check-like cycle:
// AcquireAllIdle + ReleaseUnused previously inflated IdleConns on every pass.
func TestChannelPool_StatsConsistency(t *testing.T) {
	pool := newIdleChannelPool(t, 4)
	t.Cleanup(pool.Close)

	// Two connections, both idle.
	res1, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	res2, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	res1.Release()
	res2.Release()

	assertGauges := func(total, idle, active int32) {
		t.Helper()
		stats := pool.Metrics()
		assert.Equal(t, total, stats.TotalConns, "TotalConns")
		assert.Equal(t, idle, stats.IdleConns, "IdleConns")
		assert.Equal(t, active, stats.ActiveConns, "ActiveConns")
	}

	assertGauges(2, 2, 0)

	// Repeated health-check cycles must not drift the gauges.
	for range 5 {
		idle := pool.AcquireAllIdle()
		require.Len(t, idle, 2)
		assertGauges(2, 0, 2)
		for _, res := range idle {
			res.ReleaseUnused()
		}
		assertGauges(2, 2, 0)
	}

	// Destroying a held connection adjusts all gauges.
	res, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	assertGauges(2, 1, 1)
	res.Destroy()
	assertGauges(1, 1, 0)

	// Close drains the remaining idle connection.
	pool.Close()
	assertGauges(0, 0, 0)
}
