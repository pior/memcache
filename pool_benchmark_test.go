package memcache

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// goos: darwin
// goarch: arm64
// pkg: github.com/pior/memcache
// cpu: Apple M2
// BenchmarkPool_Acquire_Creation/channel-8         	 2362138	       496.7 ns/op	    4296 B/op	       5 allocs/op
// BenchmarkPool_Acquire_Creation/puddle-8          	  728088	      1762 ns/op	    4711 B/op	      11 allocs/op
// BenchmarkPool_Acquire_Reuse/channel-8            	15513956	        76.91 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Acquire_Reuse/puddle-8             	12472682	        95.55 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Concurrent/channel-8               	 7403169	       159.4 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Concurrent/puddle-8                	 4413739	       267.8 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_AcquireAllIdle/channel-8           	 1444070	       826.0 ns/op	     496 B/op	       5 allocs/op
// BenchmarkPool_AcquireAllIdle/puddle-8            	 2201946	       542.9 ns/op	     512 B/op	       8 allocs/op
// BenchmarkPool_HighContention/channel-8           	 3398442	       355.9 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_HighContention/puddle-8            	 2543636	       447.8 ns/op	     175 B/op	       2 allocs/op
// BenchmarkPool_MixedOperations/channel-8          	 7259227	       165.2 ns/op	      42 B/op	       0 allocs/op
// BenchmarkPool_MixedOperations/puddle-8           	 4384964	       276.5 ns/op	      45 B/op	       0 allocs/op

func BenchmarkPool_Acquire_Creation(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		pool := fn(1)
		defer pool.Close()

		var runs atomic.Uint64

		for b.Loop() {
			res, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			res.Destroy()
			runs.Add(1)
		}

		want := runs.Load()
		require.EqualValues(b, want, created.Load())
	})
}

func BenchmarkPool_Acquire_Reuse(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		pool := fn(1)
		defer pool.Close()

		// Pre-create and release a connection to populate the pool
		res, err := pool.Acquire(ctx)
		require.NoError(b, err)
		if err != nil {
			b.Fatal(err)
		}
		res.Release()

		for b.Loop() {
			res, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			res.Release()
		}

		require.EqualValues(b, 1, created.Load())
	})
}

func BenchmarkPool_Concurrent(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		// RunParallel will respect GOMAXPROCS, so we use that to determine pool size
		maxSize := int32(runtime.NumCPU())

		pool := fn(maxSize)
		defer pool.Close()

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}
				res.Release()
			}
		})
	})
}

func BenchmarkPool_AcquireAllIdle(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		pool := fn(10)
		defer pool.Close()

		// Pre-populate the pool with 10 idle connections
		resources := make([]Resource, 10)
		for i := range 10 {
			res, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			resources[i] = res
		}
		for _, res := range resources {
			res.Release()
		}

		for b.Loop() {
			resources := pool.AcquireAllIdle()

			if len(resources) != 10 {
				b.Fatalf("expected 10 idle connections, got %d", len(resources))
			}

			for _, res := range resources {
				res.Release()
			}
		}
	})
}

func BenchmarkPool_HighContention(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		// Small pool size (2) with high concurrency to create contention
		pool := fn(2)
		defer pool.Close()

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}
				res.Release()
			}
		})
	})
}

func BenchmarkPool_MixedOperations(b *testing.B) {
	forEachPoolBenchmark(b, func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32) {
		ctx := context.Background()

		pool := fn(10)
		defer pool.Close()

		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}

				// Occasionally destroy connections to simulate errors
				if i%100 == 0 {
					res.Destroy()
				} else {
					res.Release()
				}
				i++
			}
		})
	})
}

func wrapPool(cp func(constructor func(ctx context.Context) (*Connection, error), maxSize int32) (Pool, error), created *atomic.Uint32) func(maxSize int32) Pool {
	return func(maxSize int32) Pool {
		constructor := func(ctx context.Context) (*Connection, error) {
			created.Add(1)
			return newMockConn(), nil
		}
		pool, err := cp(constructor, maxSize)
		if err != nil {
			panic(err)
		}
		return pool
	}
}

func forEachPoolBenchmark(b *testing.B, benchmarks func(b *testing.B, fn func(maxSize int32) Pool, created *atomic.Uint32)) {
	b.Run("channel", func(b *testing.B) {
		var created atomic.Uint32
		benchmarks(b, wrapPool(NewChannelPool, &created), &created)
	})
	b.Run("puddle", func(b *testing.B) {
		var created atomic.Uint32
		benchmarks(b, wrapPool(NewPuddlePool, &created), &created)
	})
}
