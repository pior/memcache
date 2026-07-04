package memcache

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// newBenchPool builds a pool whose constructor counts the connections it
// creates, so benchmarks can assert creation/reuse behavior.
func newBenchPool(b *testing.B, maxSize int32, created *atomic.Uint32) connPool {
	b.Helper()
	pool, err := newPuddlePool(func(ctx context.Context) (*Connection, error) {
		created.Add(1)
		return NewConnection(&mockNetConn{}, 0), nil
	}, maxSize)
	require.NoError(b, err)
	return pool
}

func BenchmarkPool_Acquire_Creation(b *testing.B) {
	ctx := context.Background()

	var created atomic.Uint32
	pool := newBenchPool(b, 1, &created)
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
}

func BenchmarkPool_Acquire_Reuse(b *testing.B) {
	ctx := context.Background()

	var created atomic.Uint32
	pool := newBenchPool(b, 1, &created)
	defer pool.Close()

	// Pre-create and release a connection to populate the pool
	res, err := pool.Acquire(ctx)
	require.NoError(b, err)
	res.Release()

	for b.Loop() {
		res, err := pool.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		res.Release()
	}

	require.EqualValues(b, 1, created.Load())
}

func BenchmarkPool_Concurrent(b *testing.B) {
	ctx := context.Background()

	// RunParallel will respect GOMAXPROCS, so we use that to determine pool size
	maxSize := int32(runtime.NumCPU())

	var created atomic.Uint32
	pool := newBenchPool(b, maxSize, &created)
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
}

func BenchmarkPool_AcquireAllIdle(b *testing.B) {
	ctx := context.Background()

	var created atomic.Uint32
	pool := newBenchPool(b, 10, &created)
	defer pool.Close()

	// Pre-populate the pool with 10 idle connections
	resources := make([]poolResource, 10)
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
}

func BenchmarkPool_HighContention(b *testing.B) {
	ctx := context.Background()

	// Small pool size (2) with high concurrency to create contention
	var created atomic.Uint32
	pool := newBenchPool(b, 2, &created)
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
}

func BenchmarkPool_MixedOperations(b *testing.B) {
	ctx := context.Background()

	var created atomic.Uint32
	pool := newBenchPool(b, 10, &created)
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
}
