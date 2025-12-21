package memcache

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultServerSelector(t *testing.T) {
	t.Run("consistency", func(t *testing.T) {
		first := DefaultServerSelector("test-key-123", 10)
		require.Equal(t, first, DefaultServerSelector("test-key-123", 10))
		require.Equal(t, first, DefaultServerSelector("test-key-123", 10))
		require.Equal(t, first, DefaultServerSelector("test-key-123", 10))
		require.Equal(t, first, DefaultServerSelector("test-key-123", 10))
	})

	t.Run("bounds", func(t *testing.T) {
		// Returned index should always be within valid range
		keys := []string{"key1", "key2", "key3", "long-key-with-many-characters"}
		serverCounts := []int{1, 2, 5, 10, 100}

		for _, key := range keys {
			for _, count := range serverCounts {
				result := DefaultServerSelector(key, count)
				require.True(t, result >= 0 && result < count, "out of bounds: key=%s, serverCount=%d, result=%d", key, count, result)
			}
		}
	})

	t.Run("distribution", func(t *testing.T) {
		// Keys should be distributed across servers (not all going to one server)
		serverCount := 10
		distribution := make(map[int]int)

		for i := range 100 {
			key := fmt.Sprintf("key-%d", i)
			server := DefaultServerSelector(key, serverCount)
			distribution[server]++
		}

		// At least 5 servers should have keys (reasonable distribution)
		require.True(t, len(distribution) >= 5, "poor distribution: only %d servers used out of %d", len(distribution), serverCount)

		// No server should have more than 30% of keys (reasonable balance)
		for server, count := range distribution {
			require.True(t, count <= 30, "unbalanced distribution: server %d has %d%% of keys", server, count)
		}
	})
}

func BenchmarkDefaultServerSelector(b *testing.B) {
	key := "benchmark-key-123"
	serverCount := 10

	for b.Loop() {
		DefaultServerSelector(key, serverCount)
	}
}
