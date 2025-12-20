package memcache

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// StaticServers Tests
// =============================================================================

func TestStaticServers_List(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	list := servers.List()

	assert.Len(t, list, 3)
	assert.Equal(t, "server1:11211", list[0])
	assert.Equal(t, "server2:11211", list[1])
	assert.Equal(t, "server3:11211", list[2])
}

func TestStaticServers_EmptyList(t *testing.T) {
	servers := NewStaticServers()

	list := servers.List()

	assert.Len(t, list, 0)
}

func TestStaticServers_SingleServer(t *testing.T) {
	servers := NewStaticServers("localhost:11211")

	list := servers.List()

	assert.Len(t, list, 1)
	assert.Equal(t, "localhost:11211", list[0])
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestStaticServers_ConcurrentAccess(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list := servers.List()
			assert.Len(t, list, 3)
		}()
	}

	wg.Wait()
}

// =============================================================================
// Client Integration Tests
// =============================================================================

func TestClient_SelectServerForKey_SingleServer(t *testing.T) {
	servers := NewStaticServers("localhost:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	addr, err := client.selectServerForKey("test-key")
	require.NoError(t, err)
	assert.Equal(t, "localhost:11211", addr)
}

func TestClient_SelectServerForKey_MultipleServers(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	// Same key should always return same server
	key := "consistent-key"
	addr1, err := client.selectServerForKey(key)
	require.NoError(t, err)

	for range 10 {
		addr, err := client.selectServerForKey(key)
		require.NoError(t, err)
		assert.Equal(t, addr1, addr)
	}
}

func TestClient_SelectServerForKey_CustomSelector(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	client, err := NewClient(servers, Config{
		MaxSize:        1,
		ServerSelector: staticSelector(0),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	addr, err := client.selectServerForKey("any-key")
	require.NoError(t, err)
	assert.Equal(t, "server1:11211", addr)

	addr2, err := client.selectServerForKey("different-key")
	require.NoError(t, err)
	assert.Equal(t, "server1:11211", addr2)
}

func TestClient_SingleServer(t *testing.T) {
	servers := NewStaticServers("localhost:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	addr, err := client.selectServerForKey("test-key")
	require.NoError(t, err)
	assert.Equal(t, "localhost:11211", addr)
}

func TestClient_SelectServerForKey_Concurrent(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211", "server3:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 10,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			key := string(rune('a' + index))
			addr, err := client.selectServerForKey(key)
			require.NoError(t, err)
			assert.NotEmpty(t, addr)
		}(i)
	}

	wg.Wait()
}
