package memcache

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a test client with a mock connection
func newTestClient(tb testing.TB, mockConn *testutils.ConnectionMock) *Client {
	tb.Helper()
	servers := StaticServers("localhost:11211")
	client := NewClient(servers, Config{
		Dialer: &mockDialer{conn: mockConn},
	})
	tb.Cleanup(func() {
		client.Close()
	})
	return client
}

// NewClient must never leave operations unbounded: the per-operation cap
// cannot be disabled, so any non-positive OperationTimeout selects the conservative
// default.
func TestNewClient_TimeoutDefault(t *testing.T) {
	newClient := func(t *testing.T, config Config) *Client {
		t.Helper()
		client := NewClient(StaticServers("localhost:11211"), config)
		t.Cleanup(client.Close)
		return client
	}

	t.Run("zero selects the default", func(t *testing.T) {
		client := newClient(t, Config{})
		assert.Equal(t, DefaultOperationTimeout, client.config.OperationTimeout)
		assert.Equal(t, DefaultOperationTimeout, client.config.DialTimeout,
			"DialTimeout must inherit the defaulted Timeout")
	})

	t.Run("negative selects the default", func(t *testing.T) {
		client := newClient(t, Config{OperationTimeout: -time.Second})
		assert.Equal(t, DefaultOperationTimeout, client.config.OperationTimeout)
	})

	t.Run("explicit value is preserved", func(t *testing.T) {
		client := newClient(t, Config{OperationTimeout: 250 * time.Millisecond})
		assert.Equal(t, 250*time.Millisecond, client.config.OperationTimeout)
	})
}

func TestNewClient_DialTimeoutDefault(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   time.Duration
	}{
		{
			name:   "zero inherits the resolved OperationTimeout",
			config: Config{OperationTimeout: 500 * time.Millisecond},
			want:   500 * time.Millisecond,
		},
		{
			name:   "negative inherits the resolved OperationTimeout",
			config: Config{OperationTimeout: 500 * time.Millisecond, DialTimeout: -time.Second},
			want:   500 * time.Millisecond,
		},
		{
			name:   "negative OperationTimeout resolves before being inherited",
			config: Config{OperationTimeout: -time.Second},
			want:   DefaultOperationTimeout,
		},
		{
			name:   "explicit positive dial timeout is preserved",
			config: Config{OperationTimeout: time.Second, DialTimeout: 2 * time.Second},
			want:   2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(StaticServers("localhost:11211"), tt.config)
			t.Cleanup(client.Close)

			assert.Equal(t, tt.want, client.config.DialTimeout)
		})
	}
}

func TestNewClient_ServerSelectorDefault(t *testing.T) {
	selectorPointer := func(selector ServerSelector) uintptr {
		return reflect.ValueOf(selector).Pointer()
	}

	t.Run("nil selects rendezvous", func(t *testing.T) {
		client := NewClient(StaticServers("localhost:11211"), Config{})
		t.Cleanup(client.Close)

		require.Equal(t,
			selectorPointer(StableServerSelector),
			selectorPointer(client.config.ServerSelector),
		)
	})

	t.Run("explicit selector is preserved", func(t *testing.T) {
		client := NewClient(StaticServers("localhost:11211"), Config{
			ServerSelector: OrderedServerSelector,
		})
		t.Cleanup(client.Close)

		require.Equal(t,
			selectorPointer(OrderedServerSelector),
			selectorPointer(client.config.ServerSelector),
		)
	})
}

type mockDialer struct {
	conn  net.Conn
	error error
}

func (d *mockDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.conn, d.error
}

// assertRequest verifies the exact protocol request written to the connection
func assertRequest(t *testing.T, mockConn *testutils.ConnectionMock, expected string) {
	t.Helper()
	actual := mockConn.GetWrittenRequest()
	if actual != expected {
		t.Errorf("Request mismatch:\nExpected: %q\nActual:   %q", expected, actual)
	}
}

// =============================================================================
// =============================================================================
// Command Tests
// =============================================================================

func TestClient_Get(t *testing.T) {
	t.Run("hit returns value, flags and cas", func(t *testing.T) {
		mock := testutils.NewConnectionMock("VA 5 f7 c42\r\nhello\r\n")
		client := newTestClient(t, mock)

		item, err := client.Get(context.Background(), "k")

		require.NoError(t, err)
		assert.Equal(t, Item{Key: "k", Value: []byte("hello"), Flags: 7, CAS: 42, Found: true}, item)
		assertRequest(t, mock, "mg k v c f\r\n")
	})

	t.Run("miss", func(t *testing.T) {
		mock := testutils.NewConnectionMock("EN\r\n")
		client := newTestClient(t, mock)

		item, err := client.Get(context.Background(), "k")

		require.NoError(t, err)
		assert.Equal(t, Item{Key: "k"}, item)
		assert.False(t, item.Found)
	})

	t.Run("get-and-touch adds the T flag", func(t *testing.T) {
		mock := testutils.NewConnectionMock("VA 5\r\nhello\r\n")
		client := newTestClient(t, mock)

		item, err := client.Get(context.Background(), "k", GetOptions{TTL: ExpiresIn(60 * time.Second)})

		require.NoError(t, err)
		assert.True(t, item.Found)
		assertRequest(t, mock, "mg k v c f T60\r\n")
	})

	t.Run("server error", func(t *testing.T) {
		mock := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
		client := newTestClient(t, mock)

		_, err := client.Get(context.Background(), "k")

		require.ErrorContains(t, err, "SERVER_ERROR")
	})

	t.Run("a reply mg cannot produce", func(t *testing.T) {
		mock := testutils.NewConnectionMock("NS\r\n")
		client := newTestClient(t, mock)

		_, err := client.Get(context.Background(), "k")

		var parseErr *meta.ParseError
		require.ErrorAs(t, err, &parseErr)
		assert.EqualError(t, err, "memcache: mg on localhost:11211: parse error: unexpected NS reply to mg")
	})
}

// newSequenceClient returns a client whose dialer hands out conns in order, one
// per dial, and the number of dials made.
func newSequenceClient(t *testing.T, conns ...net.Conn) (*Client, *atomic.Int32) {
	t.Helper()
	var dials atomic.Int32
	dialer := dialFunc(func(context.Context, string, string) (net.Conn, error) {
		n := int(dials.Add(1))
		if n > len(conns) {
			return nil, errors.New("no more connections")
		}
		return conns[n-1], nil
	})
	client := NewClient(StaticServers("localhost:11211"), Config{Dialer: dialer})
	t.Cleanup(client.Close)
	return client, &dials
}

func destroyedConns(client *Client) uint64 {
	var n uint64
	for _, pm := range client.PoolMetrics() {
		n += pm.Conns.DestroyedConns
	}
	return n
}

// A reply left unread shifts every later reply on the connection by one. The
// first reply that its command cannot produce must destroy the connection, so
// the shift cannot serve another key's value. A reply the command can produce
// but the operation does not expect leaves the stream in sync.
func TestClient_ReplyValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("a shifted stream is not reused", func(t *testing.T) {
		// A stray HD sits in front of the reply to the first Get.
		shifted := testutils.NewConnectionMock("HD\r\n", "VA 1\r\nx\r\n")
		fresh := testutils.NewConnectionMock("VA 5\r\nfresh\r\n")
		client, dials := newSequenceClient(t, shifted, fresh)

		_, err := client.Get(ctx, "k1")
		var parseErr *meta.ParseError
		require.ErrorAs(t, err, &parseErr)
		var opErr *OpError
		require.ErrorAs(t, err, &opErr)
		assert.Equal(t, "mg k1", opErr.Op+" "+opErr.Key)

		item, err := client.Get(ctx, "k2")
		require.NoError(t, err)
		assert.Equal(t, "fresh", string(item.Value), "the shifted connection's leftover reply must not be served")
		assert.Equal(t, int32(2), dials.Load())
		assert.Eventually(t, func() bool { return destroyedConns(client) == 1 }, time.Second, time.Millisecond)
	})

	t.Run("a status the operation does not expect keeps the connection", func(t *testing.T) {
		// NS is an ms reply, but a plain Set does not expect it.
		conn := testutils.NewConnectionMock("NS\r\n", "HD\r\n")
		client, dials := newSequenceClient(t, conn)

		_, err := client.Set(ctx, "k1", []byte("v"))
		require.EqualError(t, err, "unexpected store status: NS")

		_, err = client.Set(ctx, "k2", []byte("v"))
		require.NoError(t, err)
		assert.Equal(t, int32(1), dials.Load(), "the connection must be reused")
		assert.Zero(t, destroyedConns(client))
	})
}

func TestClient_Store(t *testing.T) {
	tests := []struct {
		name       string
		op         func(Querier) (StoreResult, error)
		response   string
		wantReq    string
		wantStatus Status
		wantCAS    CAS
	}{
		{
			name:     "set",
			op:       func(q Querier) (StoreResult, error) { return q.Set(context.Background(), "k", []byte("val")) },
			response: "HD c9\r\n", wantReq: "ms k 3 c\r\nval\r\n", wantStatus: Applied, wantCAS: 9,
		},
		{
			name: "set with ttl, flags and cas",
			op: func(q Querier) (StoreResult, error) {
				return q.Set(context.Background(), "k", []byte("val"), StoreOptions{TTL: ExpiresIn(60 * time.Second), Flags: 7, CAS: 5})
			},
			response: "HD\r\n", wantReq: "ms k 3 c T60 F7 C5\r\nval\r\n", wantStatus: Applied,
		},
		{
			name: "set cas mismatch",
			op: func(q Querier) (StoreResult, error) {
				return q.Set(context.Background(), "k", []byte("val"), StoreOptions{CAS: 5})
			},
			response: "EX\r\n", wantReq: "ms k 3 c C5\r\nval\r\n", wantStatus: CASMismatch,
		},
		{
			name:     "add new",
			op:       func(q Querier) (StoreResult, error) { return q.Add(context.Background(), "k", []byte("val")) },
			response: "HD\r\n", wantReq: "ms k 3 c ME\r\nval\r\n", wantStatus: Applied,
		},
		{
			name:     "add existing",
			op:       func(q Querier) (StoreResult, error) { return q.Add(context.Background(), "k", []byte("val")) },
			response: "NS\r\n", wantReq: "ms k 3 c ME\r\nval\r\n", wantStatus: Exists,
		},
		{
			name:     "replace hit",
			op:       func(q Querier) (StoreResult, error) { return q.Replace(context.Background(), "k", []byte("val")) },
			response: "HD\r\n", wantReq: "ms k 3 c MR\r\nval\r\n", wantStatus: Applied,
		},
		{
			name:     "replace missing",
			op:       func(q Querier) (StoreResult, error) { return q.Replace(context.Background(), "k", []byte("val")) },
			response: "NS\r\n", wantReq: "ms k 3 c MR\r\nval\r\n", wantStatus: NotFound,
		},
		{
			name:     "append hit",
			op:       func(q Querier) (StoreResult, error) { return q.Append(context.Background(), "k", []byte("val")) },
			response: "HD\r\n", wantReq: "ms k 3 c MA\r\nval\r\n", wantStatus: Applied,
		},
		{
			name:     "append miss",
			op:       func(q Querier) (StoreResult, error) { return q.Append(context.Background(), "k", []byte("val")) },
			response: "NF\r\n", wantReq: "ms k 3 c MA\r\nval\r\n", wantStatus: NotFound,
		},
		{
			name: "append create-on-miss",
			op: func(q Querier) (StoreResult, error) {
				return q.Append(context.Background(), "k", []byte("val"), ConcatOptions{CreateOnMiss: true, TTL: ExpiresIn(60 * time.Second)})
			},
			response: "HD\r\n", wantReq: "ms k 3 c MA N60\r\nval\r\n", wantStatus: Applied,
		},
		{
			name:     "prepend hit",
			op:       func(q Querier) (StoreResult, error) { return q.Prepend(context.Background(), "k", []byte("val")) },
			response: "HD\r\n", wantReq: "ms k 3 c MP\r\nval\r\n", wantStatus: Applied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutils.NewConnectionMock(tt.response)
			client := newTestClient(t, mock)

			res, err := tt.op(client)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus.String(), res.Status.String())
			assert.Equal(t, tt.wantCAS, res.CAS)
			assertRequest(t, mock, tt.wantReq)
		})
	}
}

func TestClient_Store_ServerError(t *testing.T) {
	mock := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mock)

	_, err := client.Set(context.Background(), "k", []byte("v"))

	require.ErrorContains(t, err, "SERVER_ERROR")
}

func TestClient_Delete(t *testing.T) {
	tests := []struct {
		name     string
		opts     []DeleteOptions
		response string
		wantReq  string
		want     Status
	}{
		{name: "found", response: "HD\r\n", wantReq: "md k\r\n", want: Applied},
		{name: "not found", response: "NF\r\n", wantReq: "md k\r\n", want: NotFound},
		{name: "cas match", opts: []DeleteOptions{{CAS: 5}}, response: "HD\r\n", wantReq: "md k C5\r\n", want: Applied},
		{name: "cas mismatch", opts: []DeleteOptions{{CAS: 5}}, response: "EX\r\n", wantReq: "md k C5\r\n", want: CASMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutils.NewConnectionMock(tt.response)
			client := newTestClient(t, mock)

			status, err := client.Delete(context.Background(), "k", tt.opts...)

			require.NoError(t, err)
			assert.Equal(t, tt.want.String(), status.String())
			assertRequest(t, mock, tt.wantReq)
		})
	}
}

func TestClient_Delete_ServerError(t *testing.T) {
	mock := testutils.NewConnectionMock("SERVER_ERROR boom\r\n")
	client := newTestClient(t, mock)

	_, err := client.Delete(context.Background(), "k")

	require.ErrorContains(t, err, "SERVER_ERROR")
}

func TestClient_Increment_ExistingKey(t *testing.T) {
	mock := testutils.NewConnectionMock("VA 1\r\n5\r\n")
	client := newTestClient(t, mock)

	value, err := client.Increment(context.Background(), "key", 5)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 5, Status: Applied}, value)
	assert.True(t, value.Found())
	assertRequest(t, mock, "ma key v c D5\r\n")
}

func TestClient_Increment_CreateOnMiss(t *testing.T) {
	mock := testutils.NewConnectionMock("VA 1\r\n1\r\n")
	client := newTestClient(t, mock)

	value, err := client.Increment(context.Background(), "key", 1,
		CounterOptions{Create: true, Initial: 1, TTL: ExpiresIn(60 * time.Second)})

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 1, Status: Applied}, value)
	assertRequest(t, mock, "ma key v c D1 J1 N60 T60\r\n")
}

func TestClient_Increment_MissWithoutInitial(t *testing.T) {
	mock := testutils.NewConnectionMock("NF\r\n")
	client := newTestClient(t, mock)

	value, err := client.Increment(context.Background(), "key", 5)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Status: NotFound}, value)
	assert.False(t, value.Found())
	assertRequest(t, mock, "ma key v c D5\r\n")
}

func TestClient_Increment_CASMismatch(t *testing.T) {
	mock := testutils.NewConnectionMock("EX\r\n")
	client := newTestClient(t, mock)

	value, err := client.Increment(context.Background(), "key", 5, CounterOptions{CAS: 9})

	require.NoError(t, err)
	assert.Equal(t, CASMismatch.String(), value.Status.String())
	assertRequest(t, mock, "ma key v c D5 C9\r\n")
}

func TestClient_Decrement(t *testing.T) {
	mock := testutils.NewConnectionMock("VA 1\r\n7\r\n")
	client := newTestClient(t, mock)

	value, err := client.Decrement(context.Background(), "key", 3)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 7, Status: Applied}, value)
	assertRequest(t, mock, "ma key v c D3 MD\r\n")
}

func TestClient_Decrement_Miss(t *testing.T) {
	mock := testutils.NewConnectionMock("NF\r\n")
	client := newTestClient(t, mock)

	value, err := client.Decrement(context.Background(), "key", 5)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Status: NotFound}, value)
	assertRequest(t, mock, "ma key v c D5 MD\r\n")
}

func TestClient_Increment_MaxUint64(t *testing.T) {
	mock := testutils.NewConnectionMock("VA 20\r\n18446744073709551615\r\n")
	client := newTestClient(t, mock)

	value, err := client.Increment(context.Background(), "key", math.MaxUint64)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: math.MaxUint64, Status: Applied}, value)
	assertRequest(t, mock, "ma key v c D18446744073709551615\r\n")
}

func TestClient_Increment_Errors(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantErr  string
	}{
		{"missing value", "NS\r\n", "missing value"},
		{"a reply ma with v cannot produce", "HD\r\n", "parse error: unexpected HD reply to ma"},
		{"non-numeric value", "VA 3\r\nabc\r\n", "failed to parse"},
		{"server error", "SERVER_ERROR boom\r\n", "SERVER_ERROR"},
		{"client error", "CLIENT_ERROR cannot increment or decrement non-numeric value\r\n", "CLIENT_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testutils.NewConnectionMock(tt.response)
			client := newTestClient(t, mock)

			_, err := client.Increment(context.Background(), "key", 1)

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// =============================================================================
// Multi-Pool Tests
// =============================================================================

func TestClient_MultiPool_LazyPoolCreation(t *testing.T) {
	// Test that pools are created lazily only when keys are accessed
	servers := StaticServers("server1:11211", "server2:11211", "server3:11211")

	mockConn := testutils.NewConnectionMock("HD\r\n")

	client := NewClient(servers, Config{
		MaxConnsPerServer: 1,
		Dialer:            &mockDialer{mockConn, nil},
	})
	defer client.Close()

	// Initially, no pools should be created
	allPoolMetrics := client.PoolMetrics()
	assert.Empty(t, allPoolMetrics, "No pools should exist before any operations")

	// Perform operations that hash to different servers
	ctx := context.Background()
	_, _ = client.Set(ctx, "key1", []byte("value1"))
	_, _ = client.Set(ctx, "key2", []byte("value2"))
	_, _ = client.Set(ctx, "key3", []byte("value3"))

	// Pools should be created only for servers that received requests
	allPoolMetrics = client.PoolMetrics()
	assert.NotEmpty(t, allPoolMetrics, "At least one pool should be created")
	assert.LessOrEqual(t, len(allPoolMetrics), 3, "At most 3 pools should be created")
}

func TestClient_MultiPool_CommandsUseCorrectServer(t *testing.T) {
	// Test that all command methods properly route to the correct server
	servers := StaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nVA 5\r\nvalue\r\nHD\r\nHD\r\nVA 1\r\n5\r\n")

	client := NewClient(servers, Config{
		MaxConnsPerServer: 5,
		Dialer:            &mockDialer{mockConn, nil},
	})
	defer client.Close()

	ctx := context.Background()

	// Test all command methods
	_, _ = client.Set(ctx, "test1", []byte("value1"))
	_, _ = client.Get(ctx, "test2")
	_, _ = client.Add(ctx, "test3", []byte("value3"))
	_, _ = client.Delete(ctx, "test4")
	_, _ = client.Increment(ctx, "test5", 1)

	// Verify that pools were created
	allPoolMetrics := client.PoolMetrics()
	assert.NotEmpty(t, allPoolMetrics, "At least one pool should be created")
}

func TestClient_MultiPool_PoolMetrics(t *testing.T) {
	// Test that PoolMetrics returns correct stats for multiple pools
	servers := StaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\n")

	client := NewClient(servers, Config{
		MaxConnsPerServer: 2,
		Dialer:            &mockDialer{mockConn, nil},
	})
	defer client.Close()

	ctx := context.Background()

	// Create operations that will likely hit both servers
	for i := range 20 {
		key := strings.Repeat("a", i+1)
		_, _ = client.Set(ctx, key, []byte("value"))
	}

	// Check stats
	allPoolMetrics := client.PoolMetrics()
	assert.NotEmpty(t, allPoolMetrics, "Should have at least one pool")

	for _, pm := range allPoolMetrics {
		assert.NotEmpty(t, pm.Addr, "Server address should be set")
		assert.Positive(t, pm.Conns.AcquireCount, "Should have some acquires")
	}
}

func TestClient_MultiPool_CloseAllPools(t *testing.T) {
	// Test that Close() closes all pools
	servers := StaticServers("server1:11211", "server2:11211", "server3:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\nHD\r\n")

	client := NewClient(servers, Config{
		MaxConnsPerServer: 1,
		Dialer:            &mockDialer{mockConn, nil},
	})

	ctx := context.Background()

	// Create pools by accessing different keys
	_, _ = client.Set(ctx, "key1", []byte("value1"))
	_, _ = client.Set(ctx, "key2", []byte("value2"))
	_, _ = client.Set(ctx, "key3", []byte("value3"))

	poolsBefore := len(client.PoolMetrics())
	assert.Positive(t, poolsBefore, "Should have created some pools")

	// Close client
	client.Close()

	// Verify pools are closed (we can't easily check this without accessing internals,
	// but we can verify Close doesn't panic)
}

// blockingClosePool is a fakePool whose Close blocks until releaseClose is closed.
type blockingClosePool struct {
	fakePool
	closeStarted chan struct{}
	releaseClose chan struct{}
}

func (p *blockingClosePool) Close() {
	close(p.closeStarted)
	<-p.releaseClose
}

func TestClient_CloseDoesNotHoldLockWhilePoolCloseBlocks(t *testing.T) {
	client := NewClient(StaticServers("server1:11211"), Config{})
	pool := &blockingClosePool{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
	client.pools.byAddr["server1:11211"] = &poolEntry{sp: &ServerPool{addr: "server1:11211", pool: pool}}

	closeDone := make(chan struct{})
	go func() {
		client.Close()
		close(closeDone)
	}()

	<-pool.closeStarted
	release := sync.OnceFunc(func() { close(pool.releaseClose) })
	defer release()

	metricsDone := make(chan []PoolMetrics, 1)
	go func() { metricsDone <- client.PoolMetrics() }()
	select {
	case metrics := <-metricsDone:
		require.Len(t, metrics, 1)
	case <-time.After(time.Second):
		t.Fatal("PoolMetrics blocked behind pool.Close")
	}

	poolResult := make(chan error, 1)
	go func() {
		_, err := client.getPoolForServer("server2:11211")
		poolResult <- err
	}()
	select {
	case err := <-poolResult:
		require.ErrorIs(t, err, ErrClientClosed)
	case <-time.After(time.Second):
		t.Fatal("closed-state check blocked behind pool.Close")
	}

	select {
	case <-closeDone:
		t.Fatal("Client.Close returned before pool.Close completed")
	default:
	}

	release()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Client.Close did not return after pool.Close completed")
	}
}

func TestClient_ClosePoolsConcurrently(t *testing.T) {
	client := NewClient(StaticServers("server1:11211", "server2:11211"), Config{})
	pools := make([]*blockingClosePool, 0, 2)
	for _, addr := range []string{"server1:11211", "server2:11211"} {
		pool := &blockingClosePool{
			closeStarted: make(chan struct{}),
			releaseClose: make(chan struct{}),
		}
		client.pools.byAddr[addr] = &poolEntry{sp: &ServerPool{addr: addr, pool: pool}}
		pools = append(pools, pool)
	}

	closeDone := make(chan struct{})
	go func() {
		client.Close()
		close(closeDone)
	}()

	release := sync.OnceFunc(func() {
		for _, pool := range pools {
			close(pool.releaseClose)
		}
	})
	defer release()

	// Both pools must enter Close before either is released: a sequential
	// close would block on the first pool and never start the second.
	for _, pool := range pools {
		select {
		case <-pool.closeStarted:
		case <-time.After(time.Second):
			t.Fatal("pool.Close not started while another pool.Close blocks")
		}
	}

	release()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Client.Close did not return after all pools closed")
	}
}

func TestClient_MultiPool_CustomSelectServer(t *testing.T) {
	// Test that custom server selection function is used
	servers := StaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\n")

	client := NewClient(servers, Config{
		MaxConnsPerServer: 1,
		ServerSelector:    staticSelector(0),

		Dialer: &mockDialer{mockConn, nil},
	})
	defer client.Close()

	ctx := context.Background()

	// All operations should go to the same server
	_, _ = client.Set(ctx, "key1", []byte("value1"))
	_, _ = client.Set(ctx, "key2", []byte("value2"))

	allPoolMetrics := client.PoolMetrics()
	assert.Len(t, allPoolMetrics, 1, "Should have only one pool since all keys go to first server")
	assert.Equal(t, "server1:11211", allPoolMetrics[0].Addr)
}

// addressDialer routes each dial to a per-address connection or error, so a
// multi-server client can be driven deterministically in tests.
type addressDialer struct {
	conns  map[string]net.Conn
	errors map[string]error
}

func (d *addressDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	if err := d.errors[address]; err != nil {
		return nil, err
	}
	return d.conns[address], nil
}

// blockingWriteConn signals when a write starts and blocks until released, to
// observe that per-server operations run concurrently.
type blockingWriteConn struct {
	*testutils.ConnectionMock
	started chan<- struct{}
	release <-chan struct{}
}

func (c *blockingWriteConn) Write(p []byte) (int, error) {
	c.started <- struct{}{}
	<-c.release
	return c.ConnectionMock.Write(p)
}

func TestClient_FlushAll(t *testing.T) {
	newClient := func(t *testing.T, response string) (*Client, *testutils.ConnectionMock) {
		t.Helper()
		mock := testutils.NewConnectionMock(response)
		client := NewClient(StaticServers("a:11211"), Config{Dialer: &mockDialer{conn: mock}})
		t.Cleanup(client.Close)
		return client, mock
	}

	t.Run("success", func(t *testing.T) {
		client, mock := newClient(t, "OK\r\n")

		require.NoError(t, client.FlushAll(context.Background()))
		assert.Equal(t, "flush_all\r\n", mock.GetWrittenRequest())
	})

	t.Run("error reply", func(t *testing.T) {
		client, _ := newClient(t, "ERROR\r\n")

		err := client.FlushAll(context.Background())

		require.EqualError(t, err, "memcache: flush_all on a:11211: ERROR")
		var genErr *meta.GenericError
		assert.ErrorAs(t, err, &genErr)
	})

	t.Run("a reply flush_all cannot produce", func(t *testing.T) {
		client, _ := newClient(t, "HD\r\n")

		err := client.FlushAll(context.Background())

		require.EqualError(t, err, "memcache: flush_all on a:11211: parse error: unexpected HD reply to flush_all")
		var parseErr *meta.ParseError
		assert.ErrorAs(t, err, &parseErr)
	})

	t.Run("incomplete response", func(t *testing.T) {
		client, _ := newClient(t, "OK")

		err := client.FlushAll(context.Background())

		require.ErrorIs(t, err, io.EOF)
		var opErr *OpError
		require.ErrorAs(t, err, &opErr)
		assert.Equal(t, "flush_all on a:11211", opErr.Op+" on "+opErr.Server)
	})
}

func TestClient_FlushAllRunsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	connA := &blockingWriteConn{ConnectionMock: testutils.NewConnectionMock("OK\r\n"), started: started, release: release}
	connB := &blockingWriteConn{ConnectionMock: testutils.NewConnectionMock("OK\r\n"), started: started, release: release}
	client := NewClient(StaticServers("a:11211", "b:11211"), Config{
		Dialer: &addressDialer{conns: map[string]net.Conn{"a:11211": connA, "b:11211": connB}},
	})
	t.Cleanup(client.Close)

	done := make(chan error, 1)
	go func() { done <- client.FlushAll(context.Background()) }()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("flushes did not start concurrently")
		}
	}
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, "flush_all\r\n", connA.GetWrittenRequest())
	assert.Equal(t, "flush_all\r\n", connB.GetWrittenRequest())
}

func TestClient_FlushAllJoinsServerErrors(t *testing.T) {
	errA := errors.New("server a unavailable")
	errB := errors.New("server b unavailable")
	client := NewClient(StaticServers("a:11211", "b:11211"), Config{
		Dialer: &addressDialer{errors: map[string]error{
			"a:11211": errA,
			"b:11211": errB,
		}},
	})
	t.Cleanup(client.Close)

	err := client.FlushAll(context.Background())

	assert.ErrorIs(t, err, errA)
	assert.ErrorIs(t, err, errB)
}

func TestClient_FlushAllWithoutServers(t *testing.T) {
	client := NewClient(StaticServers(), Config{})
	t.Cleanup(client.Close)

	err := client.FlushAll(context.Background())

	assert.ErrorIs(t, err, ErrNoServers)
}
