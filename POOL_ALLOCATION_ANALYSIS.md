# Pool Fast Path Allocation Analysis

## Question
What is causing the 8-byte, 1 allocation per operation in Puddle's fast path?

## Benchmark Results
```
BenchmarkPool_Acquire_FastPath/channel-8    68.51 ns/op    0 B/op    0 allocs/op
BenchmarkPool_Acquire_FastPath/puddle-8     99.20 ns/op    8 B/op    1 allocs/op
```

## Root Cause

The allocation occurs in our `puddlePool.Acquire()` wrapper method at [pool_puddle.go:49](pool_puddle.go#L49):

```go
func (p *puddlePool) Acquire(ctx context.Context) (Resource, error) {
	res, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &puddleResource{res: res}, nil  // ← 8-byte allocation here
}
```

Every time we acquire a connection from puddle, we must wrap the `*puddle.Resource[*conn]` in our own `puddleResource` struct to satisfy our `Resource` interface.

## Struct Layout

```go
type puddleResource struct {
	res *puddle.Resource[*conn]  // Single pointer field = 8 bytes on 64-bit
}
```

The struct contains a single pointer field, which is 8 bytes on 64-bit architectures.

## Why This Happens

1. **Puddle's API**: `puddle.Pool.Acquire()` returns `*puddle.Resource[T]`
2. **Our API**: We need to return `Resource` (our interface)
3. **Wrapper Required**: We must wrap puddle's resource in `puddleResource` to implement our interface
4. **Heap Allocation**: The wrapper escapes to heap because it's returned as an interface

## Why Channel Pool Has Zero Allocations

The channel pool returns `*channelResource` directly, and the resource already exists in the pool's channel. On the fast path:

1. Pop pre-existing `*channelResource` from channel (no allocation)
2. Return it as `Resource` interface (no allocation - already on heap from when it was created)

The key difference is that channel pool resources are **long-lived** (created once, reused many times), while puddle wrapper resources are **created per-acquire** (ephemeral).

## Performance Impact

- **Channel pool**: 68.51 ns/op, 0 allocs
- **Puddle pool**: 99.20 ns/op, 1 alloc (8 bytes)
- **Difference**: 44% slower, +8 bytes per operation

The 1 allocation adds:
- ~30ns overhead for the allocation itself
- GC pressure (8 bytes of garbage per acquire/release cycle)
- Cache pollution

## Can This Be Optimized?

### Option 1: Resource Pool for Wrappers
We could maintain a `sync.Pool` of `puddleResource` wrappers to reuse them. However:
- Adds complexity
- `sync.Pool` has its own overhead (~20-30ns per Get/Put)
- Unlikely to provide net benefit
- Violates puddle's ownership model (their resource already manages lifecycle)

### Option 2: Embed Wrapper in puddle.Resource
Not possible - we don't control puddle's code.

### Option 3: Change Our Interface
We could change our `Pool` interface to return concrete types instead of interface, but:
- Loses abstraction benefits
- Major API breaking change
- Not worth it for 8 bytes

## Conclusion

The 8-byte allocation in puddle's fast path is **inherent to the adapter pattern** we use to wrap puddle's API. It's the cost of abstraction and cannot be easily eliminated without significant trade-offs.

This is one reason why the channel pool outperforms puddle - it's designed specifically for our use case and doesn't require wrapper allocations.
