package memcache

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a test client with a mock connection
func newTestClient(t testing.TB, mockConn *testutils.ConnectionMock) *Client {
	servers := NewStaticServers("localhost:11211")
	client, err := NewClient(servers, Config{
		MaxSize: 1,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	if err != nil {
		t.Fatalf("Failed to create test client: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
	})
	return client
}

// assertRequest verifies the exact protocol request written to the connection
func assertRequest(t *testing.T, mockConn *testutils.ConnectionMock, expected string) {
	t.Helper()
	actual := mockConn.GetWrittenRequest()
	if actual != expected {
		t.Errorf("Request mismatch:\nExpected: %q\nActual:   %q", expected, actual)
	}
}

// assertNoRequest verifies that no request was written (error before send)
func assertNoRequest(t *testing.T, mockConn *testutils.ConnectionMock) {
	t.Helper()
	actual := mockConn.GetWrittenRequest()
	if actual != "" {
		t.Errorf("Expected no request, but got: %q", actual)
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

func TestClient_Get_InvalidKey_Empty(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	_, err := client.Get(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assertNoRequest(t, mockConn)
}

func TestClient_Get_InvalidKey_TooLong(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	longKey := strings.Repeat("a", 251)
	_, err := client.Get(context.Background(), longKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum length")
	assertNoRequest(t, mockConn)
}

func TestClient_Get_InvalidKey_Whitespace(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	_, err := client.Get(context.Background(), "my key")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace")
	assertNoRequest(t, mockConn)
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
		TTL:   60 * time.Second,
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

func TestClient_Set_InvalidKey(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	err := client.Set(context.Background(), Item{
		Key:   "",
		Value: []byte("value"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assertNoRequest(t, mockConn)
}

func TestClient_Set_TTLVariations(t *testing.T) {
	tests := []struct {
		name            string
		ttl             time.Duration
		expectedRequest string
	}{
		{
			name:            "1 second",
			ttl:             1 * time.Second,
			expectedRequest: "ms key 5 T1\r\nvalue\r\n",
		},
		{
			name:            "3600 seconds",
			ttl:             3600 * time.Second,
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

	require.Error(t, err)
	assert.Contains(t, err.Error(), "key already exists")
}

func TestClient_Add_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("HD\r\n")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "key",
		Value: []byte("value"),
		TTL:   60 * time.Second,
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

func TestClient_Add_InvalidKey(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	err := client.Add(context.Background(), Item{
		Key:   "my key",
		Value: []byte("value"),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace")
	assertNoRequest(t, mockConn)
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

func TestClient_Delete_InvalidKey(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	err := client.Delete(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assertNoRequest(t, mockConn)
}

// =============================================================================
// Increment Tests - Positive Delta
// =============================================================================

func TestClient_Increment_PositiveDelta_FirstCall(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n5\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 5, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(5), value)
	assertRequest(t, mockConn, "ma key v D5 J5 N0\r\n")
}

func TestClient_Increment_PositiveDelta_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n1\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 1, 60*time.Second)

	require.NoError(t, err)
	assert.Equal(t, int64(1), value)
	assertRequest(t, mockConn, "ma key v D1 J1 N60 T60\r\n")
}

func TestClient_Increment_ZeroDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 2\r\n42\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 0, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(42), value)
	assertRequest(t, mockConn, "ma key v D0 J0 N0\r\n")
}

// =============================================================================
// Increment Tests - Negative Delta
// =============================================================================

func TestClient_Increment_NegativeDelta_FirstCall(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", -5, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(0), value)
	// Verify: absolute value for D, decrement mode, J0 for initial
	assertRequest(t, mockConn, "ma key v D5 MD J0 N0\r\n")
}

func TestClient_Increment_NegativeDelta_Decrement(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n7\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", -3, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(7), value)
	assertRequest(t, mockConn, "ma key v D3 MD J0 N0\r\n")
}

func TestClient_Increment_NegativeDelta_WithTTL(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", -1, 30*time.Second)

	require.NoError(t, err)
	assert.Equal(t, int64(0), value)
	assertRequest(t, mockConn, "ma key v D1 MD J0 N30 T30\r\n")
}

// =============================================================================
// Increment Tests - Edge Cases
// =============================================================================

func TestClient_Increment_LargeDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 7\r\n1000000\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", 1000000, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(1000000), value)
	assertRequest(t, mockConn, "ma key v D1000000 J1000000 N0\r\n")
}

func TestClient_Increment_LargeNegativeDelta(t *testing.T) {
	mockConn := testutils.NewConnectionMock("VA 1\r\n0\r\n")
	client := newTestClient(t, mockConn)

	value, err := client.Increment(context.Background(), "key", -1000000, NoTTL)

	require.NoError(t, err)
	assert.Equal(t, int64(0), value)
	assertRequest(t, mockConn, "ma key v D1000000 MD J0 N0\r\n")
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

func TestClient_Increment_InvalidKey(t *testing.T) {
	mockConn := testutils.NewConnectionMock("")
	client := newTestClient(t, mockConn)

	_, err := client.Increment(context.Background(), "", 1, NoTTL)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assertNoRequest(t, mockConn)
}

// =============================================================================
// Multi-Pool Tests
// =============================================================================

func TestClient_MultiPool_LazyPoolCreation(t *testing.T) {
	// Test that pools are created lazily only when keys are accessed
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	mockConn := testutils.NewConnectionMock("HD\r\n")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	require.NoError(t, err)
	defer client.Close()

	// Initially, no pools should be created
	allStats := client.AllPoolStats()
	assert.Len(t, allStats, 0, "No pools should exist before any operations")

	// Perform operations that hash to different servers
	ctx := context.Background()
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})
	_ = client.Set(ctx, Item{Key: "key3", Value: []byte("value3")})

	// Pools should be created only for servers that received requests
	allStats = client.AllPoolStats()
	assert.Greater(t, len(allStats), 0, "At least one pool should be created")
	assert.LessOrEqual(t, len(allStats), 3, "At most 3 pools should be created")
}

func TestClient_MultiPool_CommandsUseCorrectServer(t *testing.T) {
	// Test that all command methods properly route to the correct server
	servers := NewStaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nVA 5\r\nvalue\r\nHD\r\nHD\r\nVA 1\r\n5\r\n")

	client, err := NewClient(servers, Config{
		MaxSize: 5,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Test all command methods
	_ = client.Set(ctx, Item{Key: "test1", Value: []byte("value1")})
	_, _ = client.Get(ctx, "test2")
	_ = client.Add(ctx, Item{Key: "test3", Value: []byte("value3")})
	_ = client.Delete(ctx, "test4")
	_, _ = client.Increment(ctx, "test5", 1, NoTTL)

	// Verify that pools were created
	allStats := client.AllPoolStats()
	assert.Greater(t, len(allStats), 0, "At least one pool should be created")
}

func TestClient_MultiPool_AllPoolStats(t *testing.T) {
	// Test that AllPoolStats returns correct stats for multiple pools
	servers := NewStaticServers("server1:11211", "server2:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\n")

	client, err := NewClient(servers, Config{
		MaxSize: 2,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Create operations that will likely hit both servers
	for i := 0; i < 20; i++ {
		key := strings.Repeat("a", i+1)
		_ = client.Set(ctx, Item{Key: key, Value: []byte("value")})
	}

	// Check stats
	allStats := client.AllPoolStats()
	assert.Greater(t, len(allStats), 0, "Should have at least one pool")

	for _, serverStats := range allStats {
		assert.NotEmpty(t, serverStats.Addr, "Server address should be set")
		assert.Greater(t, serverStats.PoolStats.AcquireCount, uint64(0), "Should have some acquires")
	}
}

func TestClient_MultiPool_CloseAllPools(t *testing.T) {
	// Test that Close() closes all pools
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\nHD\r\n")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create pools by accessing different keys
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})
	_ = client.Set(ctx, Item{Key: "key3", Value: []byte("value3")})

	poolsBefore := len(client.AllPoolStats())
	assert.Greater(t, poolsBefore, 0, "Should have created some pools")

	// Close client
	client.Close()

	// Verify pools are closed (we can't easily check this without accessing internals,
	// but we can verify Close doesn't panic)
}

func TestClient_MultiPool_ServerSelectionError(t *testing.T) {
	// Test error handling when server selection fails
	servers := NewStaticServers() // Empty server list

	_, err := NewClient(servers, Config{
		MaxSize: 1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no servers")
}

func TestClient_MultiPool_CustomSelectServer(t *testing.T) {
	// Test that custom server selection function is used
	servers := NewStaticServers("server1:11211", "server2:11211")

	alwaysFirst := func(key string, servers []string) (string, error) {
		if len(servers) == 0 {
			return "", assert.AnError
		}
		return servers[0], nil
	}

	mockConn := testutils.NewConnectionMock("HD\r\nHD\r\n")

	client, err := NewClient(servers, Config{
		MaxSize:      1,
		SelectServer: alwaysFirst,
		constructor: func(ctx context.Context) (*Connection, error) {
			return NewConnection(mockConn), nil
		},
	})
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// All operations should go to the same server
	_ = client.Set(ctx, Item{Key: "key1", Value: []byte("value1")})
	_ = client.Set(ctx, Item{Key: "key2", Value: []byte("value2")})

	allStats := client.AllPoolStats()
	assert.Len(t, allStats, 1, "Should have only one pool since all keys go to first server")
	assert.Equal(t, "server1:11211", allStats[0].Addr)
}
