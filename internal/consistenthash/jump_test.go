package consistenthash

import (
	"math/rand/v2"
	"testing"
)

func TestJump(t *testing.T) {
	// Vectors generated from the reference C++ code, via
	// https://github.com/dgryski/go-jump. Bucket i holds Jump(key, i+1).
	t.Run("matches the reference implementation", func(t *testing.T) {
		tests := []struct {
			key     uint64
			buckets []int
		}{
			{0, []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
			{1, []int{0, 0, 0, 0, 0, 0, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 17, 17}},
			{0xdeadbeef, []int{0, 1, 2, 3, 3, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 16, 16, 16}},
			{0x0ddc0ffeebadf00d, []int{0, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 15, 15, 15, 15}},
		}
		for _, tt := range tests {
			for i, want := range tt.buckets {
				if got := Jump(tt.key, i+1); got != want {
					t.Errorf("Jump(%#x, %d) = %d, want %d", tt.key, i+1, got, want)
				}
			}
		}
	})

	t.Run("stays in range", func(t *testing.T) {
		for range 10000 {
			key := rand.Uint64()
			b := Jump(key, 10)
			if b < 0 || b >= 10 {
				t.Fatalf("Jump(%d, 10) = %d, out of range", key, b)
			}
		}
	})

	t.Run("non-positive bucket count", func(t *testing.T) {
		if got := Jump(123, 0); got != 0 {
			t.Errorf("Jump(_, 0) = %d, want 0", got)
		}
		if got := Jump(123, -5); got != 0 {
			t.Errorf("Jump(_, -5) = %d, want 0", got)
		}
	})

	t.Run("single bucket", func(t *testing.T) {
		for range 100 {
			if got := Jump(rand.Uint64(), 1); got != 0 {
				t.Fatalf("Jump(_, 1) = %d, want 0", got)
			}
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		for range 100 {
			key := rand.Uint64()
			first := Jump(key, 7)
			second := Jump(key, 7)
			if first != second {
				t.Fatalf("Jump(%d, 7) = %d then %d, not deterministic", key, first, second)
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
			before := Jump(key, 10)
			after := Jump(key, 11)
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
			counts[Jump(rand.Uint64(), buckets)]++
		}
		want := keys / buckets
		for b, c := range counts {
			if c < want*8/10 || c > want*12/10 {
				t.Errorf("bucket %d holds %d keys, want %d +/- 20%%", b, c, want)
			}
		}
	})
}
