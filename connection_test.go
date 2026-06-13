package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockConnection(responses ...string) (*Connection, *testutils.ConnectionMock) {
	mock := testutils.NewConnectionMock(responses...)
	return NewConnection(mock, time.Second), mock
}

func getReq(key string) *meta.Request {
	return meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()
}

func TestConnection_ExecuteBatch_AllResponses(t *testing.T) {
	conn, mock := newMockConnection("VA 2\r\nv1\r\n", "EN\r\n", "MN\r\n")

	resps, err := conn.ExecuteBatch(context.Background(), []*meta.Request{getReq("k1"), getReq("k2")})
	require.NoError(t, err)
	require.Len(t, resps, 2)
	assert.Equal(t, "v1", string(resps[0].Data))
	assert.Equal(t, string(meta.StatusEN), string(resps[1].Status))
	assert.Equal(t, "mg k1 v\r\nmg k2 v\r\nmn\r\n", mock.GetWrittenRequest())
}

// A protocol error response must not stop the batch: the remaining responses
// have to be drained so the stream stays synchronized.
func TestConnection_ExecuteBatch_DrainsAfterErrorResponse(t *testing.T) {
	conn, _ := newMockConnection("CLIENT_ERROR boom\r\n", "EN\r\n", "MN\r\n")

	resps, err := conn.ExecuteBatch(context.Background(), []*meta.Request{getReq("k1"), getReq("k2")})
	require.NoError(t, err)
	require.Len(t, resps, 2)

	var clientErr *meta.ClientError
	require.ErrorAs(t, resps[0].Error, &clientErr)
	assert.Equal(t, string(meta.StatusEN), string(resps[1].Status))
}

// Fewer responses than requests without quiet mode means the connection is
// desynchronized: ExecuteBatch must report it instead of returning short.
func TestConnection_ExecuteBatch_ResponseCountMismatch(t *testing.T) {
	conn, _ := newMockConnection("EN\r\n", "MN\r\n") // one response for two requests

	resps, err := conn.ExecuteBatch(context.Background(), []*meta.Request{getReq("k1"), getReq("k2")})

	var parseErr *meta.ParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Len(t, resps, 1)
}

// With quiet requests, suppressed responses are legal: no count check.
func TestConnection_ExecuteBatch_QuietSuppressedResponses(t *testing.T) {
	conn, _ := newMockConnection("VA 2\r\nv1\r\n", "MN\r\n") // miss response suppressed

	reqs := []*meta.Request{
		getReq("k1").AddQuiet(),
		getReq("k2").AddQuiet(),
	}
	resps, err := conn.ExecuteBatch(context.Background(), reqs)
	require.NoError(t, err)
	require.Len(t, resps, 1)
	assert.Equal(t, "v1", string(resps[0].Data))
}

// An invalid key anywhere in the batch must be rejected before any write.
func TestConnection_ExecuteBatch_InvalidKeyWritesNothing(t *testing.T) {
	conn, mock := newMockConnection()

	reqs := []*meta.Request{getReq("valid"), getReq("bad key")}
	_, err := conn.ExecuteBatch(context.Background(), reqs)

	var invalidKey *meta.InvalidKeyError
	require.ErrorAs(t, err, &invalidKey)
	assert.Empty(t, mock.GetWrittenRequest(), "no bytes must reach the connection")
}

func TestConnection_ExecuteBatch_Empty(t *testing.T) {
	conn, mock := newMockConnection()

	resps, err := conn.ExecuteBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, resps)
	assert.Empty(t, mock.GetWrittenRequest())
}

func TestConnection_ExecuteStats(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		conn, mock := newMockConnection("STAT pid 1\r\nSTAT uptime 2\r\nEND\r\n")

		stats, err := conn.ExecuteStats(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "1", stats["pid"])
		assert.Equal(t, "2", stats["uptime"])
		assert.Equal(t, "stats\r\n", mock.GetWrittenRequest())
	})

	t.Run("with argument", func(t *testing.T) {
		conn, mock := newMockConnection("END\r\n")

		_, err := conn.ExecuteStats(context.Background(), "items")
		require.NoError(t, err)
		assert.Equal(t, "stats items\r\n", mock.GetWrittenRequest())
	})

	t.Run("server error", func(t *testing.T) {
		conn, _ := newMockConnection("SERVER_ERROR busy\r\n")

		_, err := conn.ExecuteStats(context.Background())
		var serverErr *meta.ServerError
		require.ErrorAs(t, err, &serverErr)
	})
}

func TestConnection_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		conn, mock := newMockConnection("MN\r\n")
		require.NoError(t, conn.Ping(context.Background()))
		assert.Equal(t, "mn\r\n", mock.GetWrittenRequest())
	})

	t.Run("unexpected response", func(t *testing.T) {
		conn, _ := newMockConnection("HD\r\n")
		require.ErrorContains(t, conn.Ping(context.Background()), "health check failed")
	})

	t.Run("connection closed", func(t *testing.T) {
		conn, _ := newMockConnection() // empty read buffer -> EOF
		require.Error(t, conn.Ping(context.Background()))
	})
}
