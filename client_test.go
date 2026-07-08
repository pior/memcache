package memcache

import (
	"context"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a test client with a mock connection
func newTestClient(t testing.TB, mockConn *testutils.ConnectionMock) *Client {
	servers := StaticServers("localhost:11211")
	client := NewClient(servers, Config{
		Dialer: &mockDialer{conn: mockConn},
	})
	t.Cleanup(func() {
		client.Close()
	})
	return client
}

// NewClient must never leave operations unbounded: the per-operation cap
// cannot be disabled, so any non-positive Timeout selects the conservative
// default.
func TestNewClient_TimeoutDefault(t *testing.T) {
	newClient := func(t *testing.T, config Config) *Client {
		client := NewClient(StaticServers("localhost:11211"), config)
		t.Cleanup(client.Close)
		return client
	}

	t.Run("zero selects the default", func(t *testing.T) {
		client := newClient(t, Config{})
		assert.Equal(t, defaultOperationTimeout, client.config.Timeout)
		assert.Equal(t, defaultOperationTimeout, client.config.ConnectTimeout,
			"ConnectTimeout must inherit the defaulted Timeout")
	})

	t.Run("negative selects the default", func(t *testing.T) {
		client := newClient(t, Config{Timeout: -time.Second})
		assert.Equal(t, defaultOperationTimeout, client.config.Timeout)
	})

	t.Run("explicit value is preserved", func(t *testing.T) {
		client := newClient(t, Config{Timeout: 250 * time.Millisecond})
		assert.Equal(t, 250*time.Millisecond, client.config.Timeout)
	})
}

func TestNewClient_ConnectTimeoutDefault(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   time.Duration
	}{
		{
			name:   "zero inherits the resolved Timeout",
			config: Config{Timeout: 500 * time.Millisecond},
			want:   500 * time.Millisecond,
		},
		{
			name:   "negative inherits the resolved Timeout",
			config: Config{Timeout: 500 * time.Millisecond, ConnectTimeout: -time.Second},
			want:   500 * time.Millisecond,
		},
		{
			name:   "negative Timeout resolves before being inherited",
			config: Config{Timeout: -time.Second},
			want:   defaultOperationTimeout,
		},
		{
			name:   "explicit positive dial timeout is preserved",
			config: Config{Timeout: time.Second, ConnectTimeout: 2 * time.Second},
			want:   2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(StaticServers("localhost:11211"), tt.config)
			t.Cleanup(client.Close)

			assert.Equal(t, tt.want, client.config.ConnectTimeout)
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

func u64(v uint64) *uint64 { return &v }

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

	t.Run("unexpected status", func(t *testing.T) {
		mock := testutils.NewConnectionMock("NS\r\n")
		client := newTestClient(t, mock)

		_, err := client.Get(context.Background(), "k")

		require.ErrorContains(t, err, "unexpected response status")
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
				return q.Append(context.Background(), "k", []byte("val"), ConcatOptions{CreateOnMiss: &CreateOnMiss{TTL: ExpiresIn(60 * time.Second)}})
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
		CounterOptions{Initial: u64(1), TTL: ExpiresIn(60 * time.Second)})

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
		{"missing value", "HD\r\n", "missing value"},
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
		MaxSize: 1,
		Dialer:  &mockDialer{mockConn, nil},
	})
	defer client.Close()

	// Initially, no pools should be created
	allPoolMetrics := client.PoolMetrics()
	assert.Len(t, allPoolMetrics, 0, "No pools should exist before any operations")

	// Perform operations that hash to different servers
	ctx := context.Background()
	_, _ = client.Set(ctx, "key1", []byte("value1"))
	_, _ = client.Set(ctx, "key2", []byte("value2"))
	_, _ = client.Set(ctx, "key3", []byte("value3"))

	// Pools should be created only for servers that received requests
	allPoolMetrics = client.PoolMetrics()
	assert.Greater(t, len(allPoolMetrics), 0, "At least one pool should be created")
	assert.LessOrEqual(t, len(allPoolMetrics), 3, "At most 3 pools should be created")
}

func TestClient_MultiPool_CommandsUseCorrectServer(t *testing.T) {
	// Test that all command methods properly route to the correct server
	servers := StaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nVA 5\r\nvalue\r\nHD\r\nHD\r\nVA 1\r\n5\r\n")

	client := NewClient(servers, Config{
		MaxSize: 5,
		Dialer:  &mockDialer{mockConn, nil},
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
	assert.Greater(t, len(allPoolMetrics), 0, "At least one pool should be created")
}

func TestClient_MultiPool_PoolMetrics(t *testing.T) {
	// Test that PoolMetrics returns correct stats for multiple pools
	servers := StaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\n")

	client := NewClient(servers, Config{
		MaxSize: 2,
		Dialer:  &mockDialer{mockConn, nil},
	})
	defer client.Close()

	ctx := context.Background()

	// Create operations that will likely hit both servers
	for i := 0; i < 20; i++ {
		key := strings.Repeat("a", i+1)
		_, _ = client.Set(ctx, key, []byte("value"))
	}

	// Check stats
	allPoolMetrics := client.PoolMetrics()
	assert.Greater(t, len(allPoolMetrics), 0, "Should have at least one pool")

	for _, pm := range allPoolMetrics {
		assert.NotEmpty(t, pm.Addr, "Server address should be set")
		assert.Greater(t, pm.Conns.AcquireCount, uint64(0), "Should have some acquires")
	}
}

func TestClient_MultiPool_CloseAllPools(t *testing.T) {
	// Test that Close() closes all pools
	servers := StaticServers("server1:11211", "server2:11211", "server3:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\nHD\r\n")

	client := NewClient(servers, Config{
		MaxSize: 1,
		Dialer:  &mockDialer{mockConn, nil},
	})

	ctx := context.Background()

	// Create pools by accessing different keys
	_, _ = client.Set(ctx, "key1", []byte("value1"))
	_, _ = client.Set(ctx, "key2", []byte("value2"))
	_, _ = client.Set(ctx, "key3", []byte("value3"))

	poolsBefore := len(client.PoolMetrics())
	assert.Greater(t, poolsBefore, 0, "Should have created some pools")

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
		MaxSize:        1,
		ServerSelector: staticSelector(0),

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
