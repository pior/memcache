package memcache

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
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

// The connection owns one Response for single-request Execute calls, so the
// Data buffer must be reused across operations: that is the allocation win the
// consume window exists for.
func TestConnection_Execute_ReusesResponseBuffers(t *testing.T) {
	conn, _ := newMockConnection("VA 5\r\nhello\r\n", "VA 5\r\nworld\r\n")

	var firstAddr *byte
	err := conn.Execute(context.Background(), getReq("k1"), func(resp *meta.Response) error {
		require.Equal(t, "hello", string(resp.Data))
		firstAddr = &resp.Data[0]
		return nil
	})
	require.NoError(t, err)

	err = conn.Execute(context.Background(), getReq("k2"), func(resp *meta.Response) error {
		require.Equal(t, "world", string(resp.Data))
		assert.Same(t, firstAddr, &resp.Data[0], "the second response must reuse the connection-owned buffer")
		return nil
	})
	require.NoError(t, err)
}

// An error returned by consume is a command-level outcome: Execute must return
// it unchanged and the connection must remain usable (the response was fully
// read off the wire).
func TestConnection_Execute_ConsumeErrorPropagates(t *testing.T) {
	conn, _ := newMockConnection("EN\r\n", "VA 2\r\nok\r\n")

	consumeErr := errors.New("not what I wanted")
	err := conn.Execute(context.Background(), getReq("k1"), func(*meta.Response) error {
		return consumeErr
	})
	assert.Same(t, consumeErr, err, "consume's error must be returned unchanged")

	err = conn.Execute(context.Background(), getReq("k2"), func(resp *meta.Response) error {
		assert.Equal(t, "ok", string(resp.Data))
		return nil
	})
	require.NoError(t, err, "the connection must stay usable after a consume error")
}

// Batch responses are retained by callers (e.g. MultiGet stores Data in
// Item.Value), so each response must have independent storage: hoisting the
// per-iteration Response out of the read loop would silently corrupt every
// value in the batch.
func TestConnection_ExecuteBatch_ResponsesAreIndependent(t *testing.T) {
	conn, _ := newMockConnection("VA 2\r\nv1\r\n", "VA 2\r\nv2\r\n", "MN\r\n")

	resps, err := conn.ExecuteBatch(context.Background(), []*meta.Request{getReq("k1"), getReq("k2")})
	require.NoError(t, err)
	require.Len(t, resps, 2)

	assert.Equal(t, "v1", string(resps[0].Data))
	assert.Equal(t, "v2", string(resps[1].Data))
	assert.NotSame(t, &resps[0].Data[0], &resps[1].Data[0],
		"batch responses must not share a backing array")
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

// An invalid request anywhere in the batch must be rejected before any write.
func TestConnection_ExecuteBatch_InvalidRequestBuffersNothing(t *testing.T) {
	tests := []struct {
		name       string
		invalidReq *meta.Request
	}{
		{"invalid key", getReq("bad key")},
		{"oversized opaque", getReq("valid2").AddOpaque(strings.Repeat("x", meta.MaxOpaqueLength+1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newMockConnection()
			reqs := []*meta.Request{getReq("valid"), tt.invalidReq}

			_, err := conn.ExecuteBatch(context.Background(), reqs)

			var invalidRequest *meta.InvalidRequestError
			require.ErrorAs(t, err, &invalidRequest)
			assert.Zero(t, conn.Writer.Buffered())
			assert.Empty(t, mock.GetWrittenRequest(), "no bytes must reach the connection")
		})
	}
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

func TestConnection_AttributesIOTimeoutToCallerDeadline(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *Connection) error
	}{
		{
			name: "execute",
			run: func(ctx context.Context, conn *Connection) error {
				return conn.Execute(ctx, getReq("key"), discardResponse)
			},
		},
		{
			name: "execute batch",
			run: func(ctx context.Context, conn *Connection) error {
				_, err := conn.ExecuteBatch(ctx, []*meta.Request{getReq("key")})
				return err
			},
		},
		{
			name: "execute stats",
			run: func(ctx context.Context, conn *Connection) error {
				_, err := conn.ExecuteStats(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() {
				_ = client.Close()
				_ = server.Close()
			})

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			err := tt.run(ctx, NewConnection(client, time.Second))

			require.Error(t, err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
		})
	}
}

func TestConnection_OperatorTimeoutIsNotAttributedToContext(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	err := NewConnection(client, 20*time.Millisecond).Execute(
		context.Background(), getReq("key"), discardResponse,
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
}
