package memcache

import (
	"bufio"
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
	client, err := NewClient("localhost:11211", Config{
		MaxSize: 1,
		constructor: func(ctx context.Context) (*Connection, error) {
			return &Connection{
				Conn:   mockConn,
				Reader: bufio.NewReader(mockConn),
			}, nil
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
