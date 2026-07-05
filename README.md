# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

The library provides a high-level `Client` with multi-server support, circuit
breakers, and connection pooling, built on top of low-level building blocks (a
meta protocol codec, connections, and command helpers) that you can compose
into a custom client.

It depends only on [sony/gobreaker](https://github.com/sony/gobreaker) (circuit
breaker) and [jackc/puddle](https://github.com/jackc/puddle) (connection pool).

**Work in Progress**: This is an active development project. The low-level meta protocol implementation is stable, and the high-level client includes production-ready features like multi-server support, circuit breakers, and connection pooling.

## Features

- **Multi-server support** with consistent key distribution
- **Circuit breakers** using [gobreaker](https://github.com/sony/gobreaker) for fault tolerance
- **Connection pooling** with health checks and lifecycle management, backed by [jackc/puddle](https://github.com/jackc/puddle)
- **Pool statistics** for monitoring connection health and usage
- Context support for timeouts and cancellation
- Type-safe operations
- Low-level building blocks (meta protocol codec, connections, command helpers) for custom clients

## Installation

```bash
go get github.com/pior/memcache
```

## Quick Start

```go
import (
    "context"
    "time"
    "github.com/pior/memcache"
)

// Create client with static servers
servers := memcache.StaticServers("localhost:11211", "localhost:11212")
client := memcache.NewClient(servers, memcache.Config{
    MaxSize:             10,
    Timeout:             500 * time.Millisecond,
    MaxConnLifetime:     5 * time.Minute,
    MaxConnIdleTime:     1 * time.Minute,
    HealthCheckInterval: 30 * time.Second,
})
defer client.Close()

ctx := context.Background()

// Set with TTL (memcache.ExpiresAt for an absolute expiration time,
// memcache.NoTTL to never expire)
_ = client.Set(ctx, memcache.Item{
    Key:   "mykey",
    Value: []byte("hello world"),
    TTL:   memcache.ExpiresIn(1 * time.Hour),
})

// Get
item, _ := client.Get(ctx, "mykey")
if item.Found {
    fmt.Printf("Value: %s\n", item.Value)
}

// Increment counter
count, _ := client.Increment(ctx, "counter", 1, memcache.NoTTL)
fmt.Printf("Count: %d\n", count)

// Delete
_ = client.Delete(ctx, "mykey")
```

## Multi-Server Support

The client supports multiple memcache servers with consistent key distribution:

```go
servers := memcache.StaticServers(
    "cache1.example.com:11211",
    "cache2.example.com:11211",
    "cache3.example.com:11211",
)

client := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
})
```

Keys are distributed with `memcache.StableServerSelector` (Rendezvous Hash) by default.
Selection depends only on the *set* of server addresses, so reordering the list never
remaps keys and adding or removing a single server moves only ~1/N of keys. For a static,
ordered server list, `memcache.OrderedServerSelector` provides the lower fixed cost of
Jump Hash. You can
also supply a custom selector of the form
`func(key string, servers []memcache.Server) memcache.Server` via
`Config.ServerSelector` (for example, a weighted or zone-aware policy).

## Circuit Breakers

Each server gets its own circuit breaker: when a server's recent failure ratio
trips it, operations to that server fail fast with `memcache.ErrBreakerOpen`
instead of tying up connections. The defaults trip at 60% failures over the
last 10 seconds (with at least 10 operations observed) and retest the server
after 5 seconds:

```go
client := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    Breaker: memcache.BreakerConfig{Enabled: true},
})
```

Every knob can be tuned; see `BreakerConfig` for the full documentation:

```go
Breaker: memcache.BreakerConfig{
    Enabled:          true,
    TripMinRequests:  10,               // don't trip below this volume
    TripFailureRatio: 0.6,              // trip when 60% of recent operations failed
    TripWindow:       10 * time.Second, // "recent" means the last 10s
    OpenDuration:     5 * time.Second,  // fail fast for 5s, then probe the server
    OnStateChange: func(server, from, to string) {
        log.Printf("breaker %s: %s -> %s", server, from, to)
    },
},
```

Only transport-level errors count as failures (dial errors, socket I/O errors,
operation timeouts); cache misses and caller-caused errors (canceled contexts,
invalid keys) do not. Detect rejected operations with
`errors.Is(err, memcache.ErrBreakerOpen)`, and monitor the breakers through
`client.PoolMetrics()`:

```go
for _, m := range client.PoolMetrics() {
    fmt.Printf("Server: %s, Circuit: %s\n", m.Addr, m.Breaker.State)
    fmt.Printf("  Requests: %d, Failures: %d\n",
        m.Breaker.Requests,
        m.Breaker.TotalFailures)
}
```

## Connection Pooling

The client pools connections per server (backed by jackc/puddle), up to
`MaxSize` connections per pool. Connection lifecycle is controlled by
`MaxConnLifetime`, `MaxConnIdleTime`, and `HealthCheckInterval`.

### Pool Statistics

Monitor connection pool health and usage:

```go
for _, m := range client.PoolMetrics() {
    fmt.Printf("Server: %s\n", m.Addr)
    fmt.Printf("  Total Connections: %d\n", m.Conns.TotalConns)
    fmt.Printf("  Idle Connections: %d\n", m.Conns.IdleConns)
    fmt.Printf("  Active Connections: %d\n", m.Conns.ActiveConns)
    fmt.Printf("  Connections Created: %d\n", m.Conns.CreatedConns)
    fmt.Printf("  Acquire Errors: %d\n", m.Conns.AcquireErrors)

    // Circuit breaker state
    fmt.Printf("  Circuit State: %s\n", m.Breaker.State)
}
```

## Timeouts

An operation goes through up to three phases, each bounded differently:

- **Pool checkout** — waiting for a connection from a saturated pool is bounded
  only by the caller's context. There is deliberately no separate pool timeout
  knob: pass a context with a deadline (with `context.Background()` and a fully
  busy pool, an operation can wait indefinitely).
- **Dial** — bounded by `ConnectTimeout` (defaults to `Timeout`).
- **I/O** — bounded by the earlier of the context deadline and `now + Timeout`,
  so even a caller with a far-future deadline cannot be stalled by a
  hung-but-connected server. The cap cannot be disabled — a non-positive
  `Timeout` selects the default; set a large value when a long budget is
  genuinely needed.

The intent is that a cache client fails fast: a timeout is a fast failure the
caller is expected to tolerate. For reads that means falling back to the origin,
like a miss. A timed-out write is ambiguous (the server may or may not have
applied it), so callers that need certainty must verify or accept the ambiguity.

Checkout waits show up in the pool metrics (`AcquireWaitCount`,
`AcquireWaitTimeNs`), and errors during checkout are prefixed with `acquire:`.

## Observability

**`Config.Observer`** is invoked around every operation, enabling tracing and
metrics without coupling the core to any telemetry backend. It is off by default
and has no impact when unset. `StartOp` returns a context (so a span propagates
to nested work) and an `ActiveOp`; the client calls `ActiveOp.End` once with the
cache result (hit/miss/stored) and any error — the same `tracer.Start` →
`span.End` shape OpenTelemetry uses. Keys are never passed to telemetry by the
shipped adapter — only counts — matching the client's policy of keeping keys out
of errors.

A ready-made OpenTelemetry tracing adapter ships as a separate module, so the
OTel dependency tree never leaks into consumers of the core package:

```go
import "github.com/pior/memcache/otelmemcache"

client := memcache.NewClient(servers, memcache.Config{
    Observer: otelmemcache.New(tracerProvider), // one client span per operation
})
```

Keys are excluded from spans by default. If your keys are safe to export to your
tracing backend, opt in with `otelmemcache.New(tracerProvider, otelmemcache.WithKeys())`
to record them as the `db.operation.parameter.key` attribute.

To wire metrics, implement `memcache.Observer` against your metrics backend (the
same hook carries everything needed for per-op latency, error, and hit/miss
counters).

## Low-Level Building Blocks

The high-level client is assembled from smaller pieces you can use on their own
to build a custom client:

- **`meta` package** — request serialization and response parsing for the
  memcached meta protocol.
- **`Connection`** — a single pooled connection that implements `Executor`.
- **`Commands` / `BatchCommands`** — the command logic (Get, Set, Delete,
  Increment, …) on top of any `Executor`.

See the [package documentation](https://pkg.go.dev/github.com/pior/memcache) for
runnable examples.

## Requirements

- Go 1.25+
- Memcached 1.6+ (with meta protocol support)

## License

MIT License - See LICENSE file for details.

## Status

This project is under active development. The meta protocol implementation and core client features (multi-server, circuit breakers, pooling) are production-ready. The API is stabilizing but breaking changes may occur before v1.0.

Contributions and feedback are welcome!
