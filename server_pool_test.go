package memcache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tripFastSettings opens the breaker after 2 consecutive failures.
func tripFastSettings() *gobreaker.Settings {
	return &gobreaker.Settings{
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	}
}

func newBreakerServerPool(t *testing.T, dialer Dialer) *ServerPool {
	t.Helper()
	config := Config{
		MaxSize:                2,
		Timeout:                time.Second,
		Dialer:                 dialer,
		NewPool:                NewPuddlePool,
		CircuitBreakerSettings: tripFastSettings(),
	}
	sp, err := NewServerPool("test:11211", config)
	require.NoError(t, err)
	t.Cleanup(sp.pool.Close)
	return sp
}

func TestServerPool_BreakerOpensOnDialFailures(t *testing.T) {
	dialer := &mockDialer{error: net.ErrClosed}
	sp := newBreakerServerPool(t, dialer)
	req := meta.NewRequest(meta.CmdGet, "key", nil)

	for range 3 {
		_, err := sp.Execute(context.Background(), req)
		require.Error(t, err)
	}

	assert.Equal(t, gobreaker.StateOpen, sp.circuitBreaker.State(),
		"repeated dial failures must open the breaker")

	_, err := sp.Execute(context.Background(), req)
	assert.ErrorIs(t, err, gobreaker.ErrOpenState)
}

// A caller canceling its context says nothing about the server: it must not
// count as a failure and open the breaker.
func TestServerPool_BreakerIgnoresCanceledContext(t *testing.T) {
	dialer := &mockDialer{error: net.ErrClosed} // dial would fail, but we never get there
	sp := newBreakerServerPool(t, dialer)
	req := meta.NewRequest(meta.CmdGet, "key", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 5 {
		_, err := sp.Execute(ctx, req)
		require.ErrorIs(t, err, context.Canceled)
	}

	assert.Equal(t, gobreaker.StateClosed, sp.circuitBreaker.State(),
		"canceled contexts must not open the breaker")
}

// An invalid key is rejected client-side: not a server failure.
func TestServerPool_BreakerIgnoresInvalidKey(t *testing.T) {
	dialer := &mockDialer{conn: newPingableMockConn()}
	sp := newBreakerServerPool(t, dialer)
	req := meta.NewRequest(meta.CmdGet, "bad key", nil)

	for range 5 {
		_, err := sp.Execute(context.Background(), req)
		var invalidKey *meta.InvalidKeyError
		require.ErrorAs(t, err, &invalidKey)
	}

	assert.Equal(t, gobreaker.StateClosed, sp.circuitBreaker.State(),
		"invalid keys must not open the breaker")
}

func newPingableMockConn() net.Conn {
	return idleNetConn{}
}

func TestServerPool_Address(t *testing.T) {
	sp, err := NewServerPool("host:11211", Config{MaxSize: 1, Dialer: &net.Dialer{}, NewPool: NewPuddlePool})
	require.NoError(t, err)
	t.Cleanup(sp.pool.Close)
	assert.Equal(t, "host:11211", sp.Address())
}

func TestServerPool_ExecuteBatch_WithBreaker(t *testing.T) {
	t.Run("empty batch", func(t *testing.T) {
		sp := newBreakerServerPool(t, &mockDialer{conn: idleNetConn{}})
		resps, err := sp.ExecuteBatch(context.Background(), nil)
		require.NoError(t, err)
		assert.Nil(t, resps)
	})

	t.Run("dial failures open the breaker", func(t *testing.T) {
		sp := newBreakerServerPool(t, &mockDialer{error: net.ErrClosed})
		reqs := []*meta.Request{meta.NewRequest(meta.CmdGet, "key", nil)}

		for range 3 {
			_, err := sp.ExecuteBatch(context.Background(), reqs)
			require.Error(t, err)
		}
		assert.Equal(t, gobreaker.StateOpen, sp.circuitBreaker.State())

		_, err := sp.ExecuteBatch(context.Background(), reqs)
		assert.ErrorIs(t, err, gobreaker.ErrOpenState)
	})
}
