package memcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPuddlePool_AcquireAfterClose(t *testing.T) {
	pool, err := newPuddlePool(func(context.Context) (*Connection, error) {
		panic("not used")
	}, 1)
	require.NoError(t, err)

	pool.Close()

	_, err = pool.Acquire(context.Background())
	require.ErrorIs(t, err, ErrPoolClosed)
}
