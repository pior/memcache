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

	assert.Equal(t, int32(0), pool.Stats().TotalConns, "all connections destroyed")
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
