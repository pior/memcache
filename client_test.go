package memcache

import (
	"bytes"
	"context"
	"errors"
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
// Get Tests
// =============================================================================

func TestClient_Get_Success(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 5\r\nhello\r\n")
	client := newTestClient(t, mockConn)

	item, err := client.Get(context.Background(), "testkey")

	require.NoError(t, err)
	assert.Equal(t, "testkey", item.Key)
	assert.Equal(t, []byte("hello"), item.Value)
	assert.True(t, item.Found)
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

func TestClient_Get_Miss(t *testing.T) {
	mockConn := testutils.NewConnectionMock("EN\r\n")
	client := newTestClient(t, mockConn)

	item, err := client.Get(context.Background(), "testkey")

	require.NoError(t, err)
	assert.Equal(t, "testkey", item.Key)
	assert.False(t, item.Found)
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

func TestClient_Get_EmptyValue(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 0\r\n\r\n")
	client := newTestClient(t, mockConn)

	item, err := client.Get(context.Background(), "testkey")

	require.NoError(t, err)
	assert.Equal(t, []byte{}, item.Value)
	assert.True(t, item.Found)
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

func TestClient_Get_ServerError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Get(context.Background(), "testkey")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_ERROR")
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

func TestClient_Get_ClientError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("CLIENT_ERROR bad format\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Get(context.Background(), "testkey")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLIENT_ERROR")
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

func TestClient_Get_UnexpectedStatus(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NS\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Get(context.Background(), "testkey")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected response status")
	assertRequest(t, mockConn, "mg testkey v\r\n")
}

// =============================================================================
// Set Tests
// =============================================================================

func TestClient_Set_Success_NoTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
		TTL:   NoTTL,
	})

	require.NoError(t, err)
	assertRequest(t, mockConn, "ms key 5\r\nvalue\r\n")
}

func TestClient_Set_Success_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
		TTL:   ExpiresIn(60 * time.Second),
	})

	require.NoError(t, err)
	assertRequest(t, mockConn, "ms key 5 T60\r\nvalue\r\n")
}

func TestClient_Set_EmptyValue(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: []byte{},
		TTL:   NoTTL,
	})

	require.NoError(t, err)
	assertRequest(t, mockConn, "ms key 0\r\n\r\n")
}

func TestClient_Set_BinaryValue(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	binaryData := []byte{0x00, 0x01, 0xFF, 0xFE}
	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: binaryData,
		TTL:   NoTTL,
	})

	require.NoError(t, err)
	written := mockConn.GetWrittenRequest()
	assert.True(t, strings.HasPrefix(written, "ms key 4\r\n"))
	assert.True(t, bytes.Contains([]byte(written), binaryData))
}

func TestClient_Set_LargeValue(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	largeValue := make([]byte, 10240)
	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: largeValue,
		TTL:   NoTTL,
	})

	require.NoError(t, err)
	written := mockConn.GetWrittenRequest()
	assert.True(t, strings.HasPrefix(written, "ms key 10240\r\n"))
}

func TestClient_Set_NotStored(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NS\r\n")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "set failed with status: NS")
}

func TestClient_Set_ServerError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_ERROR")
}

func TestClient_Set_TTLVariations(t *testing.T) {
	tests := []struct {
		name            string
		ttl             TTL
		expectedRequest string
	}{
		{
			name:            "1 second",
			ttl:             ExpiresIn(1 * time.Second),
			expectedRequest: "ms key 5 T1\r\nvalue\r\n",
		},
		{
			name:            "3600 seconds",
			ttl:             ExpiresIn(3600 * time.Second),
			expectedRequest: "ms key 5 T3600\r\nvalue\r\n",
		},
		{
			name:            "zero TTL",
			ttl:             NoTTL,
			expectedRequest: "ms key 5\r\nvalue\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockConn := testutils.NewConnectionMock("HD\r\n")
			client := newTestClient(t, mockConn)

			err := client.Set(context.Background(), Item{
				Key:   "key",
				Value: []byte("value"),
				TTL:   tt.ttl,
			})

			require.NoError(t, err)
			assertRequest(t, mockConn, tt.expectedRequest)
		})
	}
}

// =============================================================================
// Add Tests
// =============================================================================

func TestClient_Add_Success(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
	})

	require.NoError(t, err)
	assertRequest(t, mockConn, "ms key 5 ME\r\nvalue\r\n")
}

func TestClient_Add_AlreadyExists(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NS\r\n")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
	})

	require.ErrorIs(t, err, ErrNotStored)
	assert.Contains(t, err.Error(), "key already exists")
}

func TestClient_Add_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
		TTL:   ExpiresIn(60 * time.Second),
	})

	require.NoError(t, err)
	assertRequest(t, mockConn, "ms key 5 ME T60\r\nvalue\r\n")
}

func TestClient_Add_ServerError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_ERROR")
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestClient_Delete_Success_Found(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Delete(context.Background(), "key")

	require.NoError(t, err)
	assertRequest(t, mockConn, "md key\r\n")
}

func TestClient_Delete_Success_NotFound(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NF\r\n")
	client := newTestClient(t, mockConn)

	err := client.Delete(context.Background(), "key")

	require.NoError(t, err)
	assertRequest(t, mockConn, "md key\r\n")
}

func TestClient_Delete_UnexpectedStatus(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NS\r\n")
	client := newTestClient(t, mockConn)

	err := client.Delete(context.Background(), "key")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed with status: NS")
}

func TestClient_Delete_ServerError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mockConn)

	err := client.Delete(context.Background(), "key")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_ERROR")
}

// =============================================================================
// Increment Tests - Positive Delta
// =============================================================================

func TestClient_Increment_PositiveDelta_FirstCall(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 5, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 5, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D5 J5 N0\r\n")
}

func TestClient_Increment_PositiveDelta_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n1\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 1, ExpiresIn(60*time.Second))

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 1, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D1 J1 N60 T60\r\n")
}

func TestClient_Increment_ZeroDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 2\r\n42\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 0, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 42, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D0 J0 N0\r\n")
}

// =============================================================================
// Decrement Tests
// =============================================================================

func TestClient_Decrement_Miss(t *testing.T) {
	mockConn := testutils.NewConnectionMock("NF\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Decrement(context.Background(), "key", 5, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key"}, value)
	assertRequest(t, mockConn, "ma key v D5 MD\r\n")
}

func TestClient_Decrement(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n7\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Decrement(context.Background(), "key", 3, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 7, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D3 MD\r\n")
}

func TestClient_Decrement_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Decrement(context.Background(), "key", 1, ExpiresIn(30*time.Second))

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 0, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D1 MD T30\r\n")
}

// =============================================================================
// Increment Tests - Edge Cases
// =============================================================================

func TestClient_Increment_LargeDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 7\r\n1000000\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 1000000, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 1000000, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D1000000 J1000000 N0\r\n")
}

func TestClient_Decrement_LargeDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Decrement(context.Background(), "key", 1000000, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: 0, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D1000000 MD\r\n")
}

func TestClient_Increment_MaxUint64(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 20\r\n18446744073709551615\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", math.MaxUint64, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, Counter{Key: "key", Value: math.MaxUint64, Found: true}, value)
	assertRequest(t, mockConn, "ma key v D18446744073709551615 J18446744073709551615 N0\r\n")
}

// =============================================================================
// Increment Tests - Error Cases
// =============================================================================

func TestClient_Increment_NoValue(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Increment(context.Background(), "key", 1, NoTTL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "increment response missing value")
}

func TestClient_Increment_InvalidValueFormat(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 3\r\nabc\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Increment(context.Background(), "key", 1, NoTTL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse increment result")
}

func TestClient_Increment_ServerError(t *testing.T) {
	mockConn := testutils.NewConnectionMock("SERVER_ERROR out of memory\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Increment(context.Background(), "key", 1, NoTTL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER_ERROR")
}

func TestClient_Increment_ClientError_NonNumeric(t *testing.T) {
	mockConn := testutils.NewConnectionMock("CLIENT_ERROR cannot increment or decrement non-numeric value\r\n")
	client := newTestClient(t, mockConn)

	_, err := client.Increment(context.Background(), "key", 1, NoTTL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLIENT_ERROR")
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
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})
	_ = client.Set(ctx, Item{Key: "key3", Value: []byte("value3")})

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
	_ = client.Set(ctx, Item{Key: "test1", Value: []byte("value1")})
	_, _ = client.Get(ctx, "test2")
	_ = client.Add(ctx, Item{Key: "test3", Value: []byte("value3")})
	_ = client.Delete(ctx, "test4")
	_, _ = client.Increment(ctx, "test5", 1, NoTTL)

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
		_ = client.Set(ctx, Item{Key: key, Value: []byte("value")})
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
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})
	_ = client.Set(ctx, Item{Key: "key3", Value: []byte("value3")})

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
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})

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

func TestClient_FlushAllRunsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	connA := &blockingWriteConn{ConnectionMock: testutils.NewConnectionMock("OK\r\n"), started: started, release: release}
	connB := &blockingWriteConn{ConnectionMock: testutils.NewConnectionMock("OK\r\n"), started: started, release: release}
	client := NewClient(StaticServers("a:11211", "b:11211"), Config{
		Dialer:              &addressDialer{conns: map[string]net.Conn{"a:11211": connA, "b:11211": connB}},
		HealthCheckInterval: -1,
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
		HealthCheckInterval: -1,
	})
	t.Cleanup(client.Close)

	err := client.FlushAll(context.Background())

	assert.ErrorIs(t, err, errA)
	assert.ErrorIs(t, err, errB)
}

func TestClient_FlushAllWithoutServers(t *testing.T) {
	client := NewClient(StaticServers(), Config{HealthCheckInterval: -1})
	t.Cleanup(client.Close)

	err := client.FlushAll(context.Background())

	assert.ErrorIs(t, err, ErrNoServers)
}
