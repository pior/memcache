package coarsetime

import (
	"testing"
	"time"
)

// BenchmarkTimeNow/time-8         	35926340	         32.82 ns/op	       0 B/op	       0 allocs/op
// BenchmarkTimeNow/coarsetime-8   	609668066	         1.950 ns/op	       0 B/op	       0 allocs/op
func BenchmarkTimeNow(b *testing.B) {
	var t time.Time

	b.Run("time", func(b *testing.B) {
		for b.Loop() {
			t = time.Now()
		}
	})

	b.Run("coarsetime", func(b *testing.B) {
		for b.Loop() {
			t = Now()
		}
	})

	_ = t
}
