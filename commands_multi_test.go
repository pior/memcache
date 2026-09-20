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

// newBatchTestClient returns a client backed by a mock connection, seen
// through Querier: the batch methods are part of the client's command surface.
func newBatchTestClient(t *testing.T, responses ...string) (Querier, *testutils.ConnectionMock) {
	t.Helper()
	mock := testutils.NewConnectionMock(responses...)
	return newTestClient(t, mock), mock
}

func TestCommands_MultiGet(t *testing.T) {
	ctx := context.Background()

	t.Run("hits and misses in order, with cas and flags", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "VA 2 c11 f7\r\nv1\r\n", "EN\r\n", "VA 2 c13\r\nv3\r\n", "MN\r\n")

		items, err := bc.MultiGet(ctx, []string{"k1", "k2", "k3"})
		require.NoError(t, err)
		assert.Equal(t, "mg k1 v c f\r\nmg k2 v c f\r\nmg k3 v c f\r\nmn\r\n", mock.GetWrittenRequest())

		want := []Item{
			{Key: "k1", Value: []byte("v1"), CAS: 11, Flags: 7, Found: true},
			{Key: "k2"},
			{Key: "k3", Value: []byte("v3"), CAS: 13, Found: true},
		}
		assert.Equal(t, want, items)
	})

	t.Run("ttl option touches every key", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "VA 2\r\nv1\r\n", "EN\r\n", "MN\r\n")

		_, err := bc.MultiGet(ctx, []string{"k1", "k2"}, GetOptions{TTL: ExpiresIn(60 * time.Second)})
		require.NoError(t, err)
		assert.Equal(t, "mg k1 v c f T60\r\nmg k2 v c f T60\r\nmn\r\n", mock.GetWrittenRequest())
	})

	t.Run("empty keys", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		items, err := bc.MultiGet(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, items)
	})

	t.Run("protocol error names the key", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "EN\r\n", "SERVER_ERROR busy\r\n", "MN\r\n")

		_, err := bc.MultiGet(ctx, []string{"k1", "k2"})
		var serverErr *meta.ServerError
		require.ErrorAs(t, err, &serverErr)
		assert.ErrorContains(t, err, "k2")
	})

	t.Run("a reply mg cannot produce fails the batch", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "EN\r\n", "HD\r\n", "MN\r\n")

		_, err := bc.MultiGet(ctx, []string{"k1", "k2"})
		var parseErr *meta.ParseError
		require.ErrorAs(t, err, &parseErr)
		assert.EqualError(t, err, "memcache: batch on localhost:11211: parse error: unexpected HD reply to mg")
	})
}

func TestCommands_MultiSet(t *testing.T) {
	ctx := context.Background()

	t.Run("per-item options and results", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "HD c21\r\n", "EX\r\n", "NF\r\n", "MN\r\n")

		items := []SetItem{
			{Key: "k1", Value: []byte("v1")},
			{Key: "k2", Value: []byte("v2"), Options: StoreOptions{TTL: ExpiresIn(60 * time.Second), Flags: 7, CAS: 5}},
			{Key: "k3", Value: []byte("v3"), Options: StoreOptions{CAS: 9}},
		}
		results, err := bc.MultiSet(ctx, items)
		require.NoError(t, err)
		assert.Equal(t, "ms k1 2 c\r\nv1\r\nms k2 2 c T60 F7 C5\r\nv2\r\nms k3 2 c C9\r\nv3\r\nmn\r\n", mock.GetWrittenRequest())

		want := []StoreResult{
			{Status: Applied, CAS: 21},
			{Status: CASMismatch},
			{Status: NotFound},
		}
		assert.Equal(t, want, results)
	})

	t.Run("not stored is unexpected for a plain set", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "HD\r\n", "NS\r\n", "MN\r\n")

		_, err := bc.MultiSet(ctx, []SetItem{{Key: "k1", Value: []byte("v1")}, {Key: "k2", Value: []byte("v2")}})
		require.ErrorContains(t, err, "k2")
		assert.ErrorContains(t, err, "NS")
	})

	t.Run("protocol error names the key", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "HD\r\n", "CLIENT_ERROR object too large\r\n", "MN\r\n")

		_, err := bc.MultiSet(ctx, []SetItem{{Key: "k1", Value: []byte("v1")}, {Key: "k2", Value: []byte("v2")}})
		var clientErr *meta.ClientError
		require.ErrorAs(t, err, &clientErr)
		assert.ErrorContains(t, err, "k2")
	})

	t.Run("empty items", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		results, err := bc.MultiSet(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, results)
	})
}

func TestCommands_MultiDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("statuses in order", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "HD\r\n", "NF\r\n", "MN\r\n")

		statuses, err := bc.MultiDelete(ctx, []string{"k1", "k2"})
		require.NoError(t, err)
		assert.Equal(t, "md k1\r\nmd k2\r\nmn\r\n", mock.GetWrittenRequest())
		assert.Equal(t, []Status{Applied, NotFound}, statuses)
	})

	t.Run("unexpected status names the key", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "HD\r\n", "NS\r\n", "MN\r\n")

		_, err := bc.MultiDelete(ctx, []string{"k1", "k2"})
		require.ErrorContains(t, err, "k2")
		assert.ErrorContains(t, err, "NS")
	})

	t.Run("empty keys", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		statuses, err := bc.MultiDelete(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, statuses)
	})
}

// TestCommands_MultiGet_ValuesAreOwned pins the ExecuteBatch ownership
// contract: MultiGet returns resp.Data without cloning, which is only safe
// while the batch path allocates fresh buffers per response. Values from one
// batch must not alias each other, and must survive later operations on the
// same connection (a single Get reuses the connection's response buffers).
func TestCommands_MultiGet_ValuesAreOwned(t *testing.T) {
	mock := testutils.NewConnectionMock(
		"VA 2\r\nv1\r\n", "VA 2\r\nv2\r\n", "MN\r\n", // first MultiGet
		"VA 2\r\nvg\r\n",                             // Get on the same connection
		"VA 2\r\nv3\r\n", "VA 2\r\nv4\r\n", "MN\r\n", // second MultiGet
	)
	client := newTestClient(t, mock)
	bc := NewCommands(client)
	ctx := context.Background()
	keys := []string{"k1", "k2"}

	first, err := bc.MultiGet(ctx, keys)
	require.NoError(t, err)

	got, err := client.Get(ctx, "k1")
	require.NoError(t, err)
	assert.Equal(t, "vg", string(got.Value))

	second, err := bc.MultiGet(ctx, keys)
	require.NoError(t, err)

	assert.Equal(t, "v1", string(first[0].Value))
	assert.Equal(t, "v2", string(first[1].Value))
	assert.Equal(t, "v3", string(second[0].Value))
	assert.Equal(t, "v4", string(second[1].Value))
}
