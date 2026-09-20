package memcache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/pior/memcache"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const exampleServerAddr = "127.0.0.1:11211"

// The documentation examples are compiled but never executed (they have no
// Output comment, since they need a server). These tests run the behavior each
// one claims, against a real memcached, so the docs cannot drift from reality.

func exampleClient(t *testing.T) *memcache.Client {
	t.Helper()
	client := memcache.NewClient(
		memcache.StaticServers(exampleServerAddr),
		memcache.Config{MaxConnsPerServer: 4},
	)
	t.Cleanup(client.Close)
	return client
}

// ExampleClient_ExecuteBatch: a get, a set and a vivifying increment pipelined
// in one round trip, answered by position.
func TestIntegration_ExampleExecuteBatch(t *testing.T) {
	client := exampleClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	require.NoError(t, storeError(client.Set(ctx, "profile:42", []byte("ada"))))
	_, err := client.Delete(ctx, "hits:42")
	require.NoError(t, err)

	newBatch := func() []*meta.Request {
		return []*meta.Request{
			meta.NewRequest(meta.CmdGet, "profile:42", nil).
				AddReturnValue().
				AddReturnCAS(),
			meta.NewRequest(meta.CmdSet, "session:42", []byte("live")).
				AddTTL(300),
			meta.NewRequest(meta.CmdArithmetic, "hits:42", nil).
				AddModeIncrement().
				AddDelta(1).
				AddInitialValue(0).
				AddVivify(3600).
				AddReturnValue(),
		}
	}

	resps, err := client.ExecuteBatch(ctx, newBatch())
	require.NoError(t, err)
	require.Len(t, resps, 3)
	for i, resp := range resps {
		require.NoError(t, resp.Error, "resps[%d]", i)
	}

	assert.True(t, resps[0].HasValue(), "the get returns a value")
	assert.Equal(t, "ada", string(resps[0].Data))
	_, hasCAS := resps[0].CAS()
	assert.True(t, hasCAS, "the get returns a CAS token")

	assert.True(t, resps[1].IsSuccess(), "the set is stored")

	require.True(t, resps[2].HasValue(), "the increment returns a value")
	assert.Equal(t, "0", string(resps[2].Data),
		"a vivified counter starts at the initial value, the delta is not applied")

	resps, err = client.ExecuteBatch(ctx, newBatch())
	require.NoError(t, err)
	hits, err := strconv.ParseUint(string(resps[2].Data), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), hits, "the second pass increments")
}

// ExampleClient_Increment: a counter created on first use and incremented
// atomically afterwards.
func TestIntegration_ExampleIncrement(t *testing.T) {
	client := exampleClient(t)
	ctx := t.Context()
	const key = "ratelimit:user:42"

	_, err := client.Delete(ctx, key)
	require.NoError(t, err)

	opts := memcache.CounterOptions{
		Create:  true,
		Initial: 1,
		TTL:     memcache.ExpiresIn(time.Minute),
	}

	count, err := client.Increment(ctx, key, 1, opts)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), count.Value, "created at Initial")

	count, err = client.Increment(ctx, key, 1, opts)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), count.Value, "incremented once it exists")
}

// ExampleClient_Set_compareAndSwap: a stale CAS token is rejected, and Add
// reports Exists when another writer claimed the key first.
func TestIntegration_ExampleCompareAndSwap(t *testing.T) {
	client := exampleClient(t)
	ctx := t.Context()

	t.Run("a stale CAS is rejected", func(t *testing.T) {
		require.NoError(t, storeError(client.Set(ctx, "cart:42", []byte("socks"))))

		item, err := client.Get(ctx, "cart:42")
		require.NoError(t, err)
		require.True(t, item.Found)

		// A racing writer bumps the CAS behind our back.
		require.NoError(t, storeError(client.Set(ctx, "cart:42", []byte("hat"))))

		res, err := client.Set(ctx, "cart:42", []byte("socks,shoes"),
			memcache.StoreOptions{CAS: item.CAS})
		require.NoError(t, err)
		assert.Equal(t, memcache.CASMismatch.String(), res.Status.String())
	})

	t.Run("Add claims a free key once", func(t *testing.T) {
		_, err := client.Delete(ctx, "cart:99")
		require.NoError(t, err)

		res, err := client.Add(ctx, "cart:99", []byte("socks"),
			memcache.StoreOptions{TTL: memcache.ExpiresIn(time.Hour)})
		require.NoError(t, err)
		assert.True(t, res.Stored(), "the key was free")

		res, err = client.Add(ctx, "cart:99", []byte("hat"))
		require.NoError(t, err)
		assert.Equal(t, memcache.Exists.String(), res.Status.String(),
			"the key is taken, so the second writer re-reads instead")
	})
}

// ExampleClient_MultiGet: results come back in the order the keys were passed,
// with Found=false for the missing ones.
func TestIntegration_ExampleMultiGet(t *testing.T) {
	client := exampleClient(t)
	ctx := t.Context()

	require.NoError(t, storeError(client.Set(ctx, "user:1", []byte("ada"))))
	require.NoError(t, storeError(client.Set(ctx, "user:3", []byte("grace"))))
	_, err := client.Delete(ctx, "user:2")
	require.NoError(t, err)

	keys := []string{"user:1", "user:2", "user:3"}
	items, err := client.MultiGet(ctx, keys)
	require.NoError(t, err)
	require.Len(t, items, len(keys))

	assert.Equal(t, "user:1=ada", itemString(items[0]))
	assert.Equal(t, "user:2=<miss>", itemString(items[1]))
	assert.Equal(t, "user:3=grace", itemString(items[2]))
}

func itemString(item memcache.Item) string {
	if !item.Found {
		return item.Key + "=<miss>"
	}
	return item.Key + "=" + string(item.Value)
}

func storeError(_ memcache.StoreResult, err error) error { return err }
