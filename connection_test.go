package memcache

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
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

// The per-operation cap cannot be disabled on the public building block
// either: a non-positive timeout selects the default instead of leaving the
// connection unbounded.
func TestNewConnection_TimeoutDefault(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{"zero selects the default", 0, DefaultOperationTimeout},
		{"negative selects the default", -time.Second, DefaultOperationTimeout},
		{"explicit value is preserved", 250 * time.Millisecond, 250 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := NewConnection(testutils.NewConnectionMock(), tt.timeout)
			assert.Equal(t, tt.want, conn.defaultTimeout)
		})
	}
}

func getReq(key string) *meta.Request {
	return meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()
}

// The connection owns one Response for single-request Execute calls, so the
// Data buffer must be reused across operations: that is the allocation win the
// ResponseFunc window exists for.
func TestConnection_Execute_ReusesResponseBuffers(t *testing.T) {
	conn, _ := newMockConnection("VA 5\r\nhello\r\n", "VA 5\r\nworld\r\n")

	var firstAddr *byte
	err := conn.Execute(context.Background(), getReq("k1"), func(resp *meta.Response) {
		require.Equal(t, "hello", string(resp.Data))
		firstAddr = &resp.Data[0]
	})
	require.NoError(t, err)

	err = conn.Execute(context.Background(), getReq("k2"), func(resp *meta.Response) {
		require.Equal(t, "world", string(resp.Data))
		assert.Same(t, firstAddr, &resp.Data[0], "the second response must reuse the connection-owned buffer")
	})
	require.NoError(t, err)
}

// A reply the command cannot produce answers a different request: the stream
// is out of sync, so Execute must fail with an error that closes the
// connection, and must not hand the reply to fn.
func TestConnection_Execute_RejectsReplyTheCommandCannotProduce(t *testing.T) {
	conn, _ := newMockConnection("HD\r\n")

	called := false
	err := conn.Execute(context.Background(), getReq("k"), func(*meta.Response) { called = true })

	var parseErr *meta.ParseError
	require.ErrorAs(t, err, &parseErr)
	assert.True(t, meta.ShouldCloseConnection(err))
	assert.False(t, called, "fn must not see a reply to another request")
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

// More responses than requests means the connection is desynchronized. The
// error must not be accompanied by the surplus response: callers size their
// result slices from the request count, so a longer slice is an index out of
// range waiting to happen.
func TestConnection_ExecuteBatch_MoreResponsesThanRequests(t *testing.T) {
	conn, _ := newMockConnection("EN\r\n", "EN\r\n", "EN\r\n", "MN\r\n") // three for two

	reqs := []*meta.Request{getReq("k1"), getReq("k2")}
	resps, err := conn.ExecuteBatch(context.Background(), reqs)

	var parseErr *meta.ParseError
	require.ErrorAs(t, err, &parseErr)
	assert.True(t, meta.ShouldCloseConnection(err))
	assert.LessOrEqual(t, len(resps), len(reqs),
		"ExecuteBatch must never return more responses than requests")
}

// Without quiet requests, response i answers request i, so each one is checked
// against its request's command.
func TestConnection_ExecuteBatch_RejectsReplyTheCommandCannotProduce(t *testing.T) {
	conn, _ := newMockConnection("VA 2\r\nv1\r\n", "HD\r\n", "EN\r\n", "MN\r\n")

	_, err := conn.ExecuteBatch(context.Background(), []*meta.Request{getReq("k1"), getReq("k2"), getReq("k3")})

	var parseErr *meta.ParseError
	require.ErrorAs(t, err, &parseErr)
	assert.EqualError(t, err, "parse error: unexpected HD reply to mg")
}

// With quiet requests, response i does not necessarily answer request i, so
// statuses are not checked. This is a documented limit: here the HD answers the
// ms, and the quiet mg's miss was suppressed.
func TestConnection_ExecuteBatch_QuietBatchStatusesAreNotChecked(t *testing.T) {
	conn, _ := newMockConnection("HD\r\n", "MN\r\n")

	reqs := []*meta.Request{
		getReq("k1").AddQuiet(),
		meta.NewRequest(meta.CmdSet, "k2", []byte("v")),
	}
	resps, err := conn.ExecuteBatch(context.Background(), reqs)
	require.NoError(t, err)
	require.Len(t, resps, 1)
	assert.Equal(t, string(meta.StatusHD), string(resps[0].Status))
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

	// memcached sub-commands like cachedump take parameters; every token the
	// caller passes must reach the wire, not just the first.
	t.Run("with multiple arguments", func(t *testing.T) {
		conn, mock := newMockConnection("END\r\n")

		_, err := conn.ExecuteStats(context.Background(), "cachedump", "1", "100")
		require.NoError(t, err)
		assert.Equal(t, "stats cachedump 1 100\r\n", mock.GetWrittenRequest())
	})

	t.Run("empty argument is rejected", func(t *testing.T) {
		conn, mock := newMockConnection("END\r\n")

		_, err := conn.ExecuteStats(context.Background(), "cachedump", "", "100")
		require.ErrorAs(t, err, new(*meta.InvalidRequestError))
		assert.Empty(t, mock.GetWrittenRequest(), "nothing may reach the wire")
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

	t.Run("a reply mn cannot produce", func(t *testing.T) {
		conn, _ := newMockConnection("HD\r\n")
		var parseErr *meta.ParseError
		require.ErrorAs(t, conn.Ping(context.Background()), &parseErr)
	})

	t.Run("error reply", func(t *testing.T) {
		conn, _ := newMockConnection("ERROR\r\n")
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

func TestConnection_AlreadyCanceledContextWritesNothing(t *testing.T) {
	conn, mock := newMockConnection("EN\r\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := conn.Execute(ctx, getReq("key"), discardResponse)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, mock.GetWrittenRequest())
}

type cancelOnReadConn struct {
	*testutils.ConnectionMock
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelOnReadConn) Read(b []byte) (int, error) {
	n, err := c.ConnectionMock.Read(b)
	c.once.Do(c.cancel)
	return n, err
}

func TestConnection_ExecuteBatch_DoesNotRearmAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mock := &cancelOnReadConn{
		ConnectionMock: testutils.NewConnectionMock("EN\r\n", "EN\r\n", "MN\r\n"),
		cancel:         cancel,
	}
	conn := NewConnection(mock, time.Second)

	responses, err := conn.ExecuteBatch(ctx, []*meta.Request{getReq("k1"), getReq("k2")})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, responses, 1)
}
