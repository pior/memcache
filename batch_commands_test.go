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

// newBatchTestClient wires BatchCommands to a client backed by a mock connection.
func newBatchTestClient(t *testing.T, responses ...string) (*BatchCommands, *testutils.ConnectionMock) {
	mock := testutils.NewConnectionMock(responses...)
	client := newTestClient(t, mock)
	return NewBatchCommands(client), mock
}

func TestBatchCommands_MultiGet(t *testing.T) {
	t.Run("hits and misses in order", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "VA 2\r\nv1\r\n", "EN\r\n", "VA 2\r\nv3\r\n", "MN\r\n")

		items, err := bc.MultiGet(context.Background(), []string{"k1", "k2", "k3"})
		require.NoError(t, err)
		require.Len(t, items, 3)

		assert.Equal(t, "v1", string(items[0].Value))
		assert.True(t, items[0].Found)
		assert.False(t, items[1].Found)
		assert.Equal(t, "k2", items[1].Key)
		assert.Equal(t, "v3", string(items[2].Value))

		assert.Equal(t, "mg k1 v\r\nmg k2 v\r\nmg k3 v\r\nmn\r\n", mock.GetWrittenRequest())
	})

	t.Run("empty keys", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		items, err := bc.MultiGet(context.Background(), nil)
		require.NoError(t, err)
		assert.Nil(t, items)
	})

	t.Run("protocol error response", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "SERVER_ERROR busy\r\n", "EN\r\n", "MN\r\n")

		_, err := bc.MultiGet(context.Background(), []string{"k1", "k2"})
		var serverErr *meta.ServerError
		require.ErrorAs(t, err, &serverErr)
	})
}

func TestBatchCommands_MultiSet(t *testing.T) {
	t.Run("success with TTL", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "HD\r\n", "HD\r\n", "MN\r\n")

		items := []Item{
			{Key: "k1", Value: []byte("v1"), TTL: ExpiresIn(time.Minute)},
			{Key: "k2", Value: []byte("v2")},
		}
		require.NoError(t, bc.MultiSet(context.Background(), items))
		assert.Equal(t, "ms k1 2 T60\r\nv1\r\nms k2 2\r\nv2\r\nmn\r\n", mock.GetWrittenRequest())
	})

	t.Run("not stored fails with key in error", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "HD\r\n", "NS\r\n", "MN\r\n")

		items := []Item{
			{Key: "k1", Value: []byte("v1")},
			{Key: "k2", Value: []byte("v2")},
		}
		err := bc.MultiSet(context.Background(), items)
		require.ErrorContains(t, err, "k2")
		require.ErrorContains(t, err, "NS")
	})

	t.Run("empty items", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		require.NoError(t, bc.MultiSet(context.Background(), nil))
	})
}

func TestBatchCommands_MultiDelete(t *testing.T) {
	t.Run("missing keys are not errors", func(t *testing.T) {
		bc, mock := newBatchTestClient(t, "HD\r\n", "NF\r\n", "MN\r\n")

		require.NoError(t, bc.MultiDelete(context.Background(), []string{"k1", "k2"}))
		assert.Equal(t, "md k1\r\nmd k2\r\nmn\r\n", mock.GetWrittenRequest())
	})

	t.Run("unexpected status fails with key in error", func(t *testing.T) {
		bc, _ := newBatchTestClient(t, "HD\r\n", "EX\r\n", "MN\r\n")

		err := bc.MultiDelete(context.Background(), []string{"k1", "k2"})
		require.ErrorContains(t, err, "k2")
		require.ErrorContains(t, err, "EX")
	})

	t.Run("empty keys", func(t *testing.T) {
		bc, _ := newBatchTestClient(t)
		require.NoError(t, bc.MultiDelete(context.Background(), nil))
	})
}
