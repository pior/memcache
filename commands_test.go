package memcache

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executorOnly struct{}

func (executorOnly) Execute(context.Context, *meta.Request, func(*meta.Response) error) error {
	return nil
}

func TestTTL_Expiration(t *testing.T) {
	// ref is only a base for constructing absolute-time TTLs; their encoding
	// depends on the embedded time, not the current clock.
	ref := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ttl  TTL
		want string
	}{
		{name: "NoTTL means no expiration", ttl: NoTTL, want: "0"},
		{name: "zero duration means no expiration", ttl: ExpiresIn(0), want: "0"},
		{name: "negative duration means no expiration", ttl: ExpiresIn(-time.Hour), want: "0"},
		{name: "sub-second rounds up to 1s", ttl: ExpiresIn(500 * time.Millisecond), want: "1"},
		{name: "1.5s rounds up to 2s", ttl: ExpiresIn(1500 * time.Millisecond), want: "2"},
		{name: "exact seconds unchanged", ttl: ExpiresIn(time.Hour), want: "3600"},
		{name: "30 days is still relative", ttl: ExpiresIn(30 * 24 * time.Hour), want: strconv.Itoa(30 * 24 * 3600)},
		{name: "absolute time becomes a unix timestamp", ttl: ExpiresAt(ref.Add(time.Hour)), want: strconv.FormatInt(ref.Add(time.Hour).Unix(), 10)},
		{name: "absolute time in the past stays absolute (expired)", ttl: ExpiresAt(ref.Add(-time.Hour)), want: strconv.FormatInt(ref.Add(-time.Hour).Unix(), 10)},
		{name: "absolute time near the epoch is clamped to the absolute range", ttl: ExpiresAt(time.Unix(60, 0)), want: strconv.FormatInt(minAbsoluteExptime, 10)},
		{name: "zero time means no expiration", ttl: ExpiresAt(time.Time{}), want: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strconv.Itoa(tt.ttl.Expiration())
			assert.Equal(t, tt.want, got)
		})
	}

	// Relative durations beyond 30 days read the clock. synctest freezes it so
	// the two time.Now calls agree, no clock injection needed.
	t.Run("beyond 30 days becomes an absolute unix timestamp", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			want := strconv.FormatInt(time.Now().Unix()+31*24*3600, 10)
			got := strconv.Itoa(ExpiresIn(31 * 24 * time.Hour).Expiration())
			assert.Equal(t, want, got)
		})
	})
}

// Get must hand the caller an owned value: the connection reuses its response
// buffers, so a second operation on the same connection would otherwise
// overwrite the value returned by the first.
func TestGet_ValueOwnedAfterConnectionReuse(t *testing.T) {
	// The second value is smaller, so it lands in the same backing array.
	mock := testutils.NewConnectionMock("VA 5\r\nhello\r\n", "VA 2\r\nhi\r\n")
	client := newTestClient(t, mock)

	first, err := client.Get(context.Background(), "k1")
	require.NoError(t, err)
	require.Equal(t, "hello", string(first.Value))

	second, err := client.Get(context.Background(), "k2")
	require.NoError(t, err)
	require.Equal(t, "hi", string(second.Value))

	assert.Equal(t, "hello", string(first.Value),
		"connection buffer reuse must not mutate a previously returned Item.Value")
}

func TestGetAndTouch_ValueOwnedAfterConnectionReuse(t *testing.T) {
	mock := testutils.NewConnectionMock("VA 5\r\nhello\r\n", "VA 2\r\nhi\r\n")
	client := newTestClient(t, mock)

	first, err := client.GetAndTouch(context.Background(), "k1", ExpiresIn(time.Minute))
	require.NoError(t, err)

	second, err := client.GetAndTouch(context.Background(), "k2", ExpiresIn(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "hi", string(second.Value))
	assert.Equal(t, "hello", string(first.Value))
}

func TestCommands_FlushAll(t *testing.T) {
	t.Run("standalone connection", func(t *testing.T) {
		mock := testutils.NewConnectionMock("OK\r\n")
		commands := NewCommands(NewConnection(mock, time.Second))

		err := commands.FlushAll(context.Background())

		require.NoError(t, err)
		assert.Equal(t, "flush_all\r\n", mock.GetWrittenRequest())
	})

	t.Run("unsupported executor", func(t *testing.T) {
		commands := NewCommands(executorOnly{})

		err := commands.FlushAll(context.Background())

		require.ErrorContains(t, err, "does not support flush_all")
	})
}

func TestClient_ExecuteBatch_RejectsQuietFlag(t *testing.T) {
	mockConn := testutils.NewConnectionMock()
	client := newTestClient(t, mockConn)

	reqs := []*meta.Request{
		meta.NewRequest(meta.CmdGet, "key1", nil).AddReturnValue().AddQuiet(),
	}
	resps, err := client.ExecuteBatch(context.Background(), reqs)

	require.ErrorContains(t, err, "quiet flag is not supported")
	assert.Nil(t, resps)
	assert.Empty(t, mockConn.GetWrittenRequest(), "nothing must be written for a rejected batch")
}

func TestClient_OperationsAfterClose(t *testing.T) {
	t.Run("before any pool is created", func(t *testing.T) {
		mockConn := testutils.NewConnectionMock()
		client := newTestClient(t, mockConn)

		client.Close()
		client.Close() // must not panic

		_, err := client.Get(context.Background(), "key")
		require.ErrorIs(t, err, ErrClientClosed)
	})

	// Closed pools stay in the pool map after Close: an operation routed to
	// an address that already has a pool must still fail with ErrClientClosed,
	// not the ErrPoolClosed of the underlying closed pool.
	t.Run("against a previously created pool", func(t *testing.T) {
		mockConn := testutils.NewConnectionMock("VA 5\r\nhello\r\n")
		client := newTestClient(t, mockConn)

		_, err := client.Get(context.Background(), "key")
		require.NoError(t, err)

		client.Close()

		_, err = client.Get(context.Background(), "key")
		require.ErrorIs(t, err, ErrClientClosed)
	})
}
