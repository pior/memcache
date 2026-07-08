package memcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMemcacheAddr = "127.0.0.1:11211"

func createTestClient(t *testing.T) *Client {
	t.Helper()
	client := NewClient(StaticServers(testMemcacheAddr), Config{
		MaxSize:             10,
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 10 * time.Second,
	})
	t.Cleanup(client.Close)
	return client
}

// uniqueKey returns a key unique to this run so tests do not collide with
// residue from earlier runs against a shared server.
func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s:%d", prefix, time.Now().UnixNano())
}

func TestIntegration_SetGet(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:setget")

	res, err := client.Set(ctx, key, []byte("hello"), StoreOptions{Flags: 42})
	require.NoError(t, err)
	require.True(t, res.Stored())
	require.NotZero(t, res.CAS)

	got, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got.Value))
	assert.Equal(t, uint32(42), got.Flags)
	assert.Equal(t, res.CAS, got.CAS)
	assert.True(t, got.Found)

	miss, err := client.Get(ctx, uniqueKey("it:absent"))
	require.NoError(t, err)
	assert.False(t, miss.Found)
}

func TestIntegration_AddReplace(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:addrepl")

	// Replace before the key exists reports NotFound.
	r, err := client.Replace(ctx, key, []byte("v1"))
	require.NoError(t, err)
	assert.Equal(t, NotFound, r.Status)

	// Add creates it.
	r, err = client.Add(ctx, key, []byte("v1"))
	require.NoError(t, err)
	assert.True(t, r.Stored())

	// Add again reports Exists.
	r, err = client.Add(ctx, key, []byte("v2"))
	require.NoError(t, err)
	assert.Equal(t, Exists, r.Status)

	// Replace now succeeds.
	r, err = client.Replace(ctx, key, []byte("v2"))
	require.NoError(t, err)
	assert.True(t, r.Stored())

	got, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "v2", string(got.Value))
}

func TestIntegration_AppendPrepend(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:concat")

	// Append to a missing key reports NotFound.
	r, err := client.Append(ctx, key, []byte("x"))
	require.NoError(t, err)
	assert.Equal(t, NotFound, r.Status)

	// CreateOnMiss seeds it.
	r, err = client.Append(ctx, key, []byte("mid"),
		ConcatOptions{CreateOnMiss: &CreateOnMiss{TTL: ExpiresIn(time.Minute)}})
	require.NoError(t, err)
	require.True(t, r.Stored())

	_, err = client.Append(ctx, key, []byte("-after"))
	require.NoError(t, err)
	_, err = client.Prepend(ctx, key, []byte("before-"))
	require.NoError(t, err)

	got, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "before-mid-after", string(got.Value))
}

func TestIntegration_TouchAndGetAndTouch(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:touch")

	found, err := client.Touch(ctx, key, ExpiresIn(time.Minute))
	require.NoError(t, err)
	assert.False(t, found, "touch on a missing key")

	_, err = client.Set(ctx, key, []byte("v"))
	require.NoError(t, err)

	found, err = client.Touch(ctx, key, ExpiresIn(time.Minute))
	require.NoError(t, err)
	assert.True(t, found)

	// Get with a TTL touches while reading.
	got, err := client.Get(ctx, key, GetOptions{TTL: ExpiresIn(time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, "v", string(got.Value))
}

func TestIntegration_CAS(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:cas")

	set, err := client.Set(ctx, key, []byte("v1"))
	require.NoError(t, err)

	// Update with the live token advances the CAS.
	upd, err := client.Set(ctx, key, []byte("v2"), StoreOptions{CAS: set.CAS})
	require.NoError(t, err)
	require.True(t, upd.Stored())
	assert.NotEqual(t, set.CAS, upd.CAS)

	// Reusing the stale token is a mismatch, not an error.
	stale, err := client.Set(ctx, key, []byte("v3"), StoreOptions{CAS: set.CAS})
	require.NoError(t, err)
	assert.Equal(t, CASMismatch, stale.Status)

	got, err := client.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "v2", string(got.Value))
}

func TestIntegration_Delete(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:del")

	set, err := client.Set(ctx, key, []byte("v"))
	require.NoError(t, err)

	// Wrong CAS is rejected as a mismatch.
	st, err := client.Delete(ctx, key, DeleteOptions{CAS: set.CAS + 1})
	require.NoError(t, err)
	assert.Equal(t, CASMismatch, st)

	st, err = client.Delete(ctx, key, DeleteOptions{CAS: set.CAS})
	require.NoError(t, err)
	assert.Equal(t, Applied, st)

	st, err = client.Delete(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, NotFound, st)
}

func TestIntegration_Counters(t *testing.T) {
	client := createTestClient(t)
	ctx := context.Background()
	key := uniqueKey("it:ctr")

	// Increment a missing key without Initial reports NotFound.
	c, err := client.Increment(ctx, key, 1)
	require.NoError(t, err)
	assert.Equal(t, NotFound, c.Status)

	// With Initial it is created seeded with that value.
	c, err = client.Increment(ctx, key, 1, CounterOptions{Initial: u64(5)})
	require.NoError(t, err)
	require.True(t, c.Found())
	assert.Equal(t, uint64(5), c.Value)

	c, err = client.Increment(ctx, key, 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(15), c.Value)

	c, err = client.Decrement(ctx, key, 3)
	require.NoError(t, err)
	assert.Equal(t, uint64(12), c.Value)
}
