//go:build puddle

package memcache

import (
	"bufio"
	"context"
	"net"
	"testing"
)

// mockConn creates a mock connection for benchmarking
type mockNetConn struct {
	net.Conn
}

func (m *mockNetConn) Close() error {
	return nil
}

func newMockConn() *conn {
	return &conn{
		Conn:   &mockNetConn{},
		reader: bufio.NewReader(nil),
	}
}

// poolFactory represents a pool constructor for benchmarking
type poolFactory struct {
	name string
	fn   func(constructor func(ctx context.Context) (*conn, error), maxSize int32) (Pool, error)
}

var poolFactories = []poolFactory{
	{"channel", NewChannelPool},
	{"puddle", NewPuddlePool},
}

// BenchmarkPool_Acquire_Creation benchmarks acquiring a connection when pool is empty (creation path)
func BenchmarkPool_Acquire_Creation(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			for b.Loop() {
				pool, err := pf.fn(constructor, 1)
				if err != nil {
					b.Fatal(err)
				}

				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}

				res.Destroy()
				pool.Close()
			}
		})
	}
}

// BenchmarkPool_Acquire_FastPath benchmarks acquiring a connection from idle pool (fast path)
func BenchmarkPool_Acquire_FastPath(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			pool, err := pf.fn(constructor, 1)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()

			// Pre-create and release a connection to populate the pool
			res, err := pool.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			res.Release()

			b.ResetTimer()
			for b.Loop() {
				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}
				res.Release()
			}
		})
	}
}

// BenchmarkPool_Acquire_Release_Cycle benchmarks a full acquire-release cycle
func BenchmarkPool_Acquire_Release_Cycle(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			pool, err := pf.fn(constructor, 10)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()

			b.ResetTimer()
			for b.Loop() {
				res, err := pool.Acquire(ctx)
				if err != nil {
					b.Fatal(err)
				}
				res.Release()
			}
		})
	}
}

// BenchmarkPool_Concurrent benchmarks concurrent access to the pool
func BenchmarkPool_Concurrent(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			pool, err := pf.fn(constructor, 10)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()

			b.ResetTimer()
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
}

// BenchmarkPool_AcquireAllIdle benchmarks acquiring all idle connections
func BenchmarkPool_AcquireAllIdle(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			pool, err := pf.fn(constructor, 10)
			if err != nil {
				b.Fatal(err)
			}
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

			b.ResetTimer()
			for b.Loop() {
				idle := pool.AcquireAllIdle()
				// Release them back
				for _, res := range idle {
					res.Release()
				}
			}
		})
	}
}

// BenchmarkPool_HighContention benchmarks pool under high contention with limited pool size
func BenchmarkPool_HighContention(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			// Small pool size (2) with high concurrency to create contention
			pool, err := pf.fn(constructor, 2)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()

			b.ResetTimer()
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
}

// BenchmarkPool_MixedOperations benchmarks a realistic mix of operations
func BenchmarkPool_MixedOperations(b *testing.B) {
	for _, pf := range poolFactories {
		b.Run(pf.name, func(b *testing.B) {
			ctx := context.Background()
			constructor := func(ctx context.Context) (*conn, error) {
				return newMockConn(), nil
			}

			pool, err := pf.fn(constructor, 10)
			if err != nil {
				b.Fatal(err)
			}
			defer pool.Close()

			b.ResetTimer()
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
}
