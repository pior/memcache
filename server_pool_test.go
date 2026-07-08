package memcache

import (
	"context"
	"errors"
	"math"
	"net"
	"os"
	"testing"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardResponse is a consume callback for tests that only assert on errors.
func discardResponse(*meta.Response) error { return nil }

// tripFastBreaker opens the breaker once 2 operations have been observed and
// all of them failed.
func tripFastBreaker() BreakerConfig {
	return BreakerConfig{
		Enabled:          true,
		TripMinRequests:  2,
		TripFailureRatio: 1,
	}
}

func newBreakerServerPool(t *testing.T, dialer Dialer) *ServerPool {
	t.Helper()
	config := Config{
		MaxSize: 2,
		Timeout: time.Second,
		Dialer:  dialer,
		Breaker: tripFastBreaker(),
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
		err := sp.Execute(context.Background(), req, discardResponse)
		require.Error(t, err)
	}

	assert.Equal(t, gobreaker.StateOpen, sp.breaker.State(),
		"repeated dial failures must open the breaker")

	err := sp.Execute(context.Background(), req, discardResponse)
	assert.ErrorIs(t, err, ErrBreakerOpen)
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
		err := sp.Execute(ctx, req, discardResponse)
		require.ErrorIs(t, err, context.Canceled)
	}

	assert.Equal(t, gobreaker.StateClosed, sp.breaker.State(),
		"canceled contexts must not open the breaker")
}

// A caller's own deadline expiring (e.g. while waiting for a connection from a
// saturated pool) is client-side and must not count as a server failure.
func TestServerPool_BreakerIgnoresCallerDeadline(t *testing.T) {
	dialer := &mockDialer{error: net.ErrClosed} // dial would fail, but we never get there
	sp := newBreakerServerPool(t, dialer)
	req := meta.NewRequest(meta.CmdGet, "key", nil)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	for range 5 {
		err := sp.Execute(ctx, req, discardResponse)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}

	assert.Equal(t, gobreaker.StateClosed, sp.breaker.State(),
		"expired caller deadlines must not open the breaker")
}

type acquireFuncPool struct {
	acquire func(context.Context) (poolResource, error)
}

func (p *acquireFuncPool) Acquire(ctx context.Context) (poolResource, error) {
	return p.acquire(ctx)
}

func (*acquireFuncPool) AcquireAllIdle() []poolResource { return nil }
func (*acquireFuncPool) Close()                         {}
func (*acquireFuncPool) Metrics() ConnPoolMetrics       { return ConnPoolMetrics{} }

func newBreakerServerPoolWithAcquire(t *testing.T, breaker BreakerConfig, acquire func(context.Context) (poolResource, error)) *ServerPool {
	t.Helper()
	sp, err := NewServerPool("test:11211", Config{
		MaxSize: 1,
		Timeout: time.Second,
		Dialer:  &mockDialer{error: net.ErrClosed},
		Breaker: breaker,
	})
	require.NoError(t, err)
	sp.pool.Close()
	sp.pool = &acquireFuncPool{acquire: acquire}
	return sp
}

func TestServerPool_BreakerExclusionsPreserveFailureCounts(t *testing.T) {
	call := 0
	sp := newBreakerServerPoolWithAcquire(t, tripFastBreaker(), func(ctx context.Context) (poolResource, error) {
		call++
		if call == 2 {
			return nil, ctx.Err()
		}
		return nil, net.ErrClosed
	})
	req := getReq("key")

	require.ErrorIs(t, sp.Execute(context.Background(), req, discardResponse), net.ErrClosed)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sp.Execute(canceled, req, discardResponse), context.Canceled)

	require.ErrorIs(t, sp.Execute(context.Background(), req, discardResponse), net.ErrClosed)
	assert.Equal(t, gobreaker.StateOpen, sp.breaker.State(),
		"an excluded request must not dilute the failure counts")
}

func TestServerPool_BreakerExclusionDoesNotCloseHalfOpenState(t *testing.T) {
	breaker := BreakerConfig{
		Enabled:          true,
		TripMinRequests:  1,
		TripFailureRatio: 1,
		OpenDuration:     20 * time.Millisecond,
	}
	sp := newBreakerServerPoolWithAcquire(t, breaker, func(ctx context.Context) (poolResource, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	})
	req := getReq("key")

	require.ErrorIs(t, sp.Execute(context.Background(), req, discardResponse), net.ErrClosed)
	require.Equal(t, gobreaker.StateOpen, sp.breaker.State())
	require.Eventually(t, func() bool {
		return sp.breaker.State() == gobreaker.StateHalfOpen
	}, time.Second, time.Millisecond)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sp.Execute(canceled, req, discardResponse), context.Canceled)

	assert.Equal(t, gobreaker.StateHalfOpen, sp.breaker.State(),
		"an excluded request must not count as the success that closes a half-open breaker")
	counts := sp.breaker.Counts()
	assert.Zero(t, counts.TotalSuccesses)
	assert.Zero(t, counts.TotalFailures)
	assert.Zero(t, counts.ConsecutiveSuccesses)
	assert.Zero(t, counts.ConsecutiveFailures)
}

func TestServerPool_BreakerAttributesIOTimeout(t *testing.T) {
	tests := []struct {
		name              string
		newContext        func(t *testing.T) context.Context
		connectionTimeout time.Duration
		wantContextError  bool
		wantFailures      uint32
	}{
		{
			name: "caller deadline is excluded",
			newContext: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			connectionTimeout: time.Second,
			wantContextError:  true,
			wantFailures:      0,
		},
		{
			name: "operator timeout is a failure",
			newContext: func(t *testing.T) context.Context {
				return context.Background()
			},
			connectionTimeout: 20 * time.Millisecond,
			wantContextError:  false,
			wantFailures:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() {
				_ = client.Close()
				_ = server.Close()
			})
			// A trip volume no test can reach: only the counts are asserted.
			neverTrips := BreakerConfig{Enabled: true, TripMinRequests: math.MaxUint32}
			sp, err := NewServerPool("test:11211", Config{
				MaxSize: 1,
				Timeout: tt.connectionTimeout,
				Dialer:  &mockDialer{conn: client},
				Breaker: neverTrips,
			})
			require.NoError(t, err)
			t.Cleanup(sp.pool.Close)

			err = sp.Execute(tt.newContext(t), getReq("key"), discardResponse)

			require.Error(t, err)
			assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
			assert.Equal(t, tt.wantContextError, errors.Is(err, context.DeadlineExceeded))
			assert.Equal(t, tt.wantFailures, sp.breaker.Counts().TotalFailures)
		})
	}
}

// An invalid request is rejected client-side: not a server failure.
func TestServerPool_BreakerIgnoresInvalidRequest(t *testing.T) {
	dialer := &mockDialer{conn: newPingableMockConn()}
	sp := newBreakerServerPool(t, dialer)
	req := meta.NewRequest(meta.CmdGet, "bad key", nil)

	for range 5 {
		err := sp.Execute(context.Background(), req, discardResponse)
		var invalidRequest *meta.InvalidRequestError
		require.ErrorAs(t, err, &invalidRequest)
	}

	assert.Equal(t, gobreaker.StateClosed, sp.breaker.State(),
		"invalid requests must not open the breaker")
}

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

func newPingableMockConn() net.Conn {
	return idleNetConn{}
}

func TestServerPool_Address(t *testing.T) {
	sp, err := NewServerPool("host:11211", Config{MaxSize: 1, Dialer: &net.Dialer{}})
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
		assert.Equal(t, gobreaker.StateOpen, sp.breaker.State())

		_, err := sp.ExecuteBatch(context.Background(), reqs)
		assert.ErrorIs(t, err, ErrBreakerOpen)
	})
}

func TestOpError_Message(t *testing.T) {
	tests := []struct {
		name string
		err  *OpError
		want string
	}{
		{
			name: "op with server",
			err:  &OpError{Op: "mg", Server: "cache1:11211", Err: errors.New("timeout")},
			want: "memcache: mg on cache1:11211: timeout",
		},
		{
			name: "batch",
			err:  &OpError{Op: OpBatch, Server: "cache1:11211", Err: errors.New("timeout")},
			want: "memcache: batch on cache1:11211: timeout",
		},
		{
			name: "no server",
			err:  &OpError{Op: "mg", Err: errors.New("boom")},
			want: "memcache: mg: boom",
		},
		{
			// Keys often carry PII and have unbounded cardinality: they are
			// available in the Key field but kept out of the message.
			name: "key is not part of the message",
			err:  &OpError{Op: "ms", Key: "user:42:email", Server: "s:1", Err: errors.New("x")},
			want: "memcache: ms on s:1: x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}

func TestOpError_Wrapping(t *testing.T) {
	t.Run("server and op context on dial failure", func(t *testing.T) {
		dialer := &mockDialer{error: net.ErrClosed}
		sp := newBreakerServerPool(t, dialer)

		err := sp.Execute(context.Background(), meta.NewRequest(meta.CmdGet, "key", nil), discardResponse)

		var opErr *OpError
		require.ErrorAs(t, err, &opErr)
		assert.Equal(t, "mg", opErr.Op)
		assert.Equal(t, "key", opErr.Key)
		assert.Equal(t, "test:11211", opErr.Server)
		assert.ErrorIs(t, err, net.ErrClosed, "the cause must stay reachable")
	})

	t.Run("breaker rejection carries server context", func(t *testing.T) {
		dialer := &mockDialer{error: net.ErrClosed}
		sp := newBreakerServerPool(t, dialer)
		req := meta.NewRequest(meta.CmdGet, "key", nil)

		for range 3 {
			_ = sp.Execute(context.Background(), req, discardResponse)
		}
		err := sp.Execute(context.Background(), req, discardResponse)
		require.ErrorIs(t, err, ErrBreakerOpen)

		var opErr *OpError
		require.ErrorAs(t, err, &opErr)
		assert.Equal(t, "test:11211", opErr.Server)
	})

	t.Run("no double wrapping", func(t *testing.T) {
		dialer := &mockDialer{error: net.ErrClosed}
		sp := newBreakerServerPool(t, dialer)

		err := sp.Execute(context.Background(), meta.NewRequest(meta.CmdGet, "key", nil), discardResponse)

		var opErr *OpError
		require.ErrorAs(t, err, &opErr)
		_, stillWrapped := opErr.Err.(*OpError)
		assert.False(t, stillWrapped, "the cause must not be another OpError")
	})
}
