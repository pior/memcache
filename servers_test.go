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
// DefaultSelectServer Tests
// =============================================================================

func TestDefaultSelectServer_NoServers(t *testing.T) {
	servers := []string{}

	addr, err := DefaultSelectServer("key1", servers)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no servers available")
	assert.Empty(t, addr)
}

func TestDefaultSelectServer_SingleServer(t *testing.T) {
	servers := []string{"server1:11211"}

	addr, err := DefaultSelectServer("key1", servers)

	require.NoError(t, err)
	assert.Equal(t, "server1:11211", addr)

	// Different key should return same server
	addr2, err := DefaultSelectServer("different-key", servers)

	require.NoError(t, err)
	assert.Equal(t, "server1:11211", addr2)
}

func TestDefaultSelectServer_MultipleServers_ConsistentHashing(t *testing.T) {
	servers := []string{"server1:11211", "server2:11211", "server3:11211"}

	// Same key should always return same server
	key := "test-key"
	addr1, err := DefaultSelectServer(key, servers)
	require.NoError(t, err)

	for range 100 {
		addr, err := DefaultSelectServer(key, servers)
		require.NoError(t, err)
		assert.Equal(t, addr1, addr, "Same key should always map to same server")
	}
}

func TestDefaultSelectServer_MultipleServers_Distribution(t *testing.T) {
	servers := []string{"server1:11211", "server2:11211", "server3:11211"}

	// Test that different keys distribute across servers
	distribution := make(map[string]int)

	for i := range 1000 {
		key := string(rune('a' + i))
		addr, err := DefaultSelectServer(key, servers)
		require.NoError(t, err)
		distribution[addr]++
	}

	// All servers should have been selected at least once
	assert.Len(t, distribution, 3, "All servers should be used")

	// Each server should have roughly 1/3 of keys (allow some variance)
	for server, count := range distribution {
		// Allow 20% variance from expected 333 keys per server
		assert.Greater(t, count, 200, "Server %s should have reasonable number of keys", server)
		assert.Less(t, count, 500, "Server %s should have reasonable number of keys", server)
	}
}

func TestDefaultSelectServer_DifferentKeys_DifferentServers(t *testing.T) {
	servers := []string{"server1:11211", "server2:11211", "server3:11211"}

	// Generate enough keys to ensure we hit different servers
	seenServers := make(map[string]bool)

	for i := range 100 {
		key := string(rune('a' + i))
		addr, err := DefaultSelectServer(key, servers)
		require.NoError(t, err)
		seenServers[addr] = true

		if len(seenServers) == 3 {
			// We've seen all servers
			break
		}
	}

	assert.Len(t, seenServers, 3, "Should distribute across all servers")
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

func TestDefaultSelectServer_ConcurrentAccess(t *testing.T) {
	servers := []string{"server1:11211", "server2:11211", "server3:11211"}

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			key := string(rune('a' + index))
			addr, err := DefaultSelectServer(key, servers)
			require.NoError(t, err)
			assert.NotEmpty(t, addr)
		}(i)
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

	// Custom selector that always picks the first server
	customSelector := func(key string, servers []string) (string, error) {
		if len(servers) == 0 {
			return "", assert.AnError
		}
		return servers[0], nil
	}

	client, err := NewClient(servers, Config{
		MaxSize:      1,
		SelectServer: customSelector,
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
