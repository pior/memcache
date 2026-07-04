package memcache

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/pior/memcache/internal"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/xxh3"
)

// staticSelector always selects the same server, regardless of the key.
func staticSelector(index int) ServerSelector {
	return func(key string, servers []Server) Server {
		return servers[index%len(servers)]
	}
}

func makeServers(n int) []Server {
	s := make([]Server, n)
	for i := range s {
		s[i] = Server{Address: fmt.Sprintf("10.0.0.%d:11211", i)}
	}
	return s
}

func TestStableServerSelector(t *testing.T) {
	servers := makeServers(10)

	t.Run("consistency", func(t *testing.T) {
		first := StableServerSelector("test-key-123", servers)
		for range 4 {
			require.Equal(t, first, StableServerSelector("test-key-123", servers))
		}
	})

	t.Run("always returns a server from the set", func(t *testing.T) {
		set := make(map[string]bool, len(servers))
		for _, s := range servers {
			set[s.Address] = true
		}
		for _, key := range []string{"key1", "key2", "key3", "long-key-with-many-characters"} {
			for _, n := range []int{1, 2, 5, 10, 100} {
				got := StableServerSelector(key, makeServers(n))
				require.True(t, got.Address != "", "empty address for key=%s n=%d", key, n)
			}
			got := StableServerSelector(key, servers)
			require.True(t, set[got.Address], "selected %q not in the set", got.Address)
		}
	})

	t.Run("distribution", func(t *testing.T) {
		distribution := make(map[string]int)
		for i := range 1000 {
			key := fmt.Sprintf("key-%d", i)
			distribution[StableServerSelector(key, servers).Address]++
		}
		require.Len(t, distribution, len(servers), "every server should receive keys")
		for addr, count := range distribution {
			// Even split is 100/server; allow generous slack for a 1000-key sample.
			require.True(t, count >= 40 && count <= 180, "unbalanced: %s got %d", addr, count)
		}
	})
}

// TestStableServerSelector_OrderIndependent is the core guarantee that the
// previous positional jump-hash scheme lacked: shuffling the same set of
// servers must not remap any key.
func TestStableServerSelector_OrderIndependent(t *testing.T) {
	servers := makeServers(8)

	shuffled := make([]Server, len(servers))
	copy(shuffled, servers)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	for i := range 5000 {
		key := fmt.Sprintf("key-%d", i)
		require.Equal(t,
			StableServerSelector(key, servers).Address,
			StableServerSelector(key, shuffled).Address,
			"key %q remapped after reordering the server set", key)
	}
}

// TestStableServerSelector_MinimalMovement checks the rendezvous property that
// removing one server (anywhere, including the middle) relocates only the keys
// that lived on that server — roughly 1/N — and never moves the rest. Jump hash
// over a positional list would reshuffle a large fraction here.
func TestStableServerSelector_MinimalMovement(t *testing.T) {
	servers := makeServers(8)
	removeIdx := 3 // a middle node, the worst case for positional schemes

	reduced := make([]Server, 0, len(servers)-1)
	reduced = append(reduced, servers[:removeIdx]...)
	reduced = append(reduced, servers[removeIdx+1:]...)
	removed := servers[removeIdx].Address

	const keys = 10000
	moved := 0
	for i := range keys {
		key := fmt.Sprintf("key-%d", i)
		before := StableServerSelector(key, servers).Address
		after := StableServerSelector(key, reduced).Address

		if before == removed {
			// Keys on the removed node must move somewhere still in the set.
			require.NotEqual(t, removed, after)
			moved++
			continue
		}
		// Keys on surviving nodes must not move at all.
		require.Equal(t, before, after, "key %q moved off a surviving node", key)
	}

	// Only the removed node's share (~1/8) should have moved.
	fraction := float64(moved) / float64(keys)
	require.Truef(t, fraction > 0.08 && fraction < 0.17,
		"expected ~1/8 of keys to move, got %.3f (%d/%d)", fraction, moved, keys)
}

func TestOrderedServerSelector_UsesJumpHash(t *testing.T) {
	servers := makeServers(8)
	for i := range 100 {
		key := fmt.Sprintf("key-%d", i)
		expected := servers[internal.JumpHash(xxh3.HashString(key), len(servers))]
		require.Equal(t, expected, OrderedServerSelector(key, servers))
	}
}

// BenchmarkServerSelectors compares the two built-in selectors for realistic
// cluster sizes. OrderedServerSelector (Jump hash) hashes the key once, while
// StableServerSelector hashes each server address per call. Keys are varied
// so the numbers reflect realistic, non-degenerate hashing.
func BenchmarkServerSelectors(b *testing.B) {
	keys := make([]string, 1024) // power of two, so the index is a cheap mask
	for i := range keys {
		keys[i] = fmt.Sprintf("user:%d:session", i)
	}

	selectors := []struct {
		name string
		fn   ServerSelector
	}{
		{"JumpHash", OrderedServerSelector},
		{"Rendezvous", StableServerSelector},
	}

	for _, n := range []int{3, 10} {
		servers := makeServers(n)
		for _, sel := range selectors {
			b.Run(fmt.Sprintf("%s/servers=%d", sel.name, n), func(b *testing.B) {
				var sink Server
				i := 0
				for b.Loop() {
					sink = sel.fn(keys[i&(len(keys)-1)], servers)
					i++
				}
				_ = sink
			})
		}
	}
}
