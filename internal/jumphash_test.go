package internal

import (
	"math/rand/v2"
	"testing"
)

func TestJumpHash(t *testing.T) {
	t.Run("stays in range", func(t *testing.T) {
		for range 10000 {
			key := rand.Uint64()
			b := JumpHash(key, 10)
			if b < 0 || b >= 10 {
				t.Fatalf("JumpHash(%d, 10) = %d, out of range", key, b)
			}
		}
	})

	t.Run("non-positive bucket count", func(t *testing.T) {
		if got := JumpHash(123, 0); got != 0 {
			t.Errorf("JumpHash(_, 0) = %d, want 0", got)
		}
		if got := JumpHash(123, -5); got != 0 {
			t.Errorf("JumpHash(_, -5) = %d, want 0", got)
		}
	})

	t.Run("single bucket", func(t *testing.T) {
		for range 100 {
			if got := JumpHash(rand.Uint64(), 1); got != 0 {
				t.Fatalf("JumpHash(_, 1) = %d, want 0", got)
			}
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		for range 100 {
			key := rand.Uint64()
			first := JumpHash(key, 7)
			second := JumpHash(key, 7)
			if first != second {
				t.Fatalf("JumpHash(%d, 7) = %d then %d, not deterministic", key, first, second)
			}
		}
	})

	// The defining property of jump hash: adding a bucket only moves keys
	// into the new bucket, never between existing buckets.
	t.Run("monotonic consistency when adding a bucket", func(t *testing.T) {
		const keys = 10000
		moved := 0
		for range keys {
			key := rand.Uint64()
			before := JumpHash(key, 10)
			after := JumpHash(key, 11)
			if before != after {
				if after != 10 {
					t.Fatalf("key %d moved from bucket %d to existing bucket %d", key, before, after)
				}
				moved++
			}
		}
		// Expect ~1/11 of keys to move to the new bucket.
		if moved < keys/22 || moved > keys/6 {
			t.Errorf("moved %d/%d keys, expected around %d", moved, keys, keys/11)
		}
	})

	t.Run("reasonable distribution", func(t *testing.T) {
		const keys = 100000
		const buckets = 10
		var counts [buckets]int
		for range keys {
			counts[JumpHash(rand.Uint64(), buckets)]++
		}
		want := keys / buckets
		for b, c := range counts {
			if c < want*8/10 || c > want*12/10 {
				t.Errorf("bucket %d holds %d keys, want %d +/- 20%%", b, c, want)
			}
		}
	})
}
