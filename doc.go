// Package memcache is a modern memcache client for Go implementing the
// memcached meta protocol (version 1.6+).
//
// The high-level [Client] is the recommended entry point. It provides
// multi-server support with consistent key distribution, per-server circuit
// breakers, and connection pooling with health checks:
//
//	servers := memcache.StaticServers("localhost:11211", "localhost:11212")
//	client := memcache.NewClient(servers, memcache.Config{
//		MaxSize: 10,
//		Timeout: 500 * time.Millisecond,
//	})
//	defer client.Close()
//
//	_ = client.Set(ctx, memcache.Item{Key: "mykey", Value: []byte("hello")})
//	item, _ := client.Get(ctx, "mykey")
//
// # Building Blocks
//
// The client is assembled from smaller pieces that can be used on their own to
// build a custom client:
//
//   - The meta package serializes requests and parses responses for the
//     memcached meta protocol.
//   - [Connection] wraps a single net.Conn and implements [Executor].
//   - [Commands] and [BatchCommands] hold the command logic (Get, Set, Delete,
//     Increment, …) on top of any [Executor].
//
// Connection pooling is built into [Client] (backed by jackc/puddle) and is not
// a standalone building block.
//
// # Timeouts
//
// An operation goes through up to three phases, each bounded differently:
//
//   - Pool checkout: waiting for a connection from a saturated pool is bounded
//     only by the caller's context. There is deliberately no separate pool
//     timeout knob; pass a context with a deadline. With context.Background()
//     and a fully busy pool, an operation can wait indefinitely.
//   - Dial: establishing a new connection is bounded by [Config.ConnectTimeout].
//     It inherits a positive [Config.Timeout], otherwise it defaults to five
//     seconds.
//   - I/O: socket reads and writes are bounded by the earlier of the context
//     deadline and now+[Config.Timeout], so even a caller with a far-future
//     deadline cannot be stalled by a hung-but-connected server. When Timeout
//     is disabled and no deadline exists, context cancellation interrupts I/O.
//
// The intent is that a cache client fails fast: a timeout is a fast failure
// the caller is expected to tolerate. For reads that means falling back to the
// origin, like a miss. A timed-out write is different — it is ambiguous (the
// server may or may not have applied it), so callers that need certainty must
// verify or accept the ambiguity.
//
// Checkout waits are visible in [ConnPoolMetrics] (AcquireWaitCount,
// AcquireWaitTimeNs), and an error during checkout is prefixed with "acquire:"
// to distinguish it from an I/O failure on the wire.
package memcache
