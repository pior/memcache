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
// BenchmarkPool_Acquire_Creation/channel-8         	 1517066	       824.3 ns/op	    8464 B/op	       7 allocs/op
// BenchmarkPool_Acquire_Creation/puddle-8          	  534495	      2218 ns/op	    8880 B/op	      13 allocs/op
// BenchmarkPool_Acquire_Reuse/channel-8         		22137240	        53.86 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Acquire_Reuse/puddle-8          		12595086	        95.22 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Concurrent/channel-8         	 		 6733398	       161.6 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_Concurrent/puddle-8          	 		 4502383	       267.5 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_AcquireAllIdle/channel-8         	 	 2027647	       571.3 ns/op	     496 B/op	       5 allocs/op
// BenchmarkPool_AcquireAllIdle/puddle-8          	 	 2212290	       542.0 ns/op	     512 B/op	       8 allocs/op
// BenchmarkPool_HighContention/channel-8         		 s3972553	       287.5 ns/op	       0 B/op	       0 allocs/op
// BenchmarkPool_HighContention/puddle-8          		 2627834	       441.4 ns/op	     176 B/op	       2 allocs/op
// BenchmarkPool_MixedOperations/channel-8         	 	 6487830	       181.4 ns/op	      84 B/op	       0 allocs/op
// BenchmarkPool_MixedOperations/puddle-8          	 	 4246132	       282.7 ns/op	      87 B/op	       0 allocs/op

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
			return NewConnection(&mockNetConn{}, 0), nil
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
