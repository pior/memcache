# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

The library provides a high-level `Client` with multi-server support, circuit
breakers, and connection pooling, built on top of low-level building blocks (a
meta protocol codec, connections, and command helpers) that you can compose
into a custom client.

Its runtime dependencies are [sony/gobreaker](https://github.com/sony/gobreaker)
for circuit breakers, [jackc/puddle](https://github.com/jackc/puddle) for
connection pooling, and [zeebo/xxh3](https://github.com/zeebo/xxh3) for key
hashing.

## Why This Client?

[bradfitz/gomemcache](https://github.com/bradfitz/gomemcache) is the de-facto
Go memcache client and is battle-tested; if a plain client speaking the legacy
text protocol is all you need, it remains a fine choice. This client exists for
services that need more from their cache path:

| | pior/memcache | bradfitz/gomemcache |
|---|---|---|
| Protocol | meta (memcached 1.6+) | legacy text |
| Timeouts | per-call `context.Context`, plus a per-operation I/O bound that cannot be disabled | client-wide `Timeout` |
| Connection pooling | bounded pool with connection lifetime/idle limits, health checks, and pool metrics | free-list of idle connections, unbounded when busy |
| Key distribution | rendezvous hashing: reordering the server list never remaps keys, adding or removing one server moves ~1/N of keys | CRC32 modulo: any change to the server list remaps most keys |
| Failure isolation | per-server circuit breakers with per-server error attribution | errors surface to the caller |
| Batching | pipelined batches of mixed commands (get, set, delete, …) | `GetMulti` (reads only) |
| Observability | `Observer` hook with a ready-made OpenTelemetry adapter | — |

### Performance

Single-key operations are bounded by the server round-trip in both clients —
switching does not cost you throughput
([`cmd/bench`](cmd/bench), 8 workers against memcached 1.6 on loopback,
AMD Ryzen 7 8845HS, 200k ops per operation, trimmed mean of 5 runs):

| operation | pior/memcache | bradfitz/gomemcache |
|---|---|---|
| get (hit) | 151k ops/s | 151k ops/s |
| get (miss) | 155k ops/s | 141k ops/s |
| set | 151k ops/s | 150k ops/s |
| get 10 KB | 151k ops/s | 150k ops/s |
| set 10 KB | 106k ops/s | 105k ops/s |
| delete | 149k ops/s | 151k ops/s |
| increment | 149k ops/s | 151k ops/s |

Pipelining is where the meta protocol pays off: on the same setup, a
`BatchCommands` batch of 10 gets delivers **1.10M items/s** versus 151k items/s
issuing them one at a time — and batches extend to writes and mixed commands,
which the legacy protocol's `GetMulti` cannot express.

Reproduce with `./bench -count 200000 -concurrency 8 -runs 5` (add `-bradfitz`
for the gomemcache side); every pull request also runs
[a paired benchmark](.github/workflows/bench.yml) against `main` to catch
regressions.

## Features

- **Multi-server support** with consistent key distribution
- **Circuit breakers** using [gobreaker](https://github.com/sony/gobreaker) for fault tolerance
- **Connection pooling** with health checks and lifecycle management, backed by [jackc/puddle](https://github.com/jackc/puddle)
- **Pool statistics** for monitoring connection health and usage
- Context deadlines honored throughout; cancellation during I/O is bounded by the per-operation timeout
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
    "fmt"
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
// omit StoreOptions to never expire). Set reports the outcome and the new
// CAS token; the error is reserved for transport failures.
_, _ = client.Set(ctx, "mykey", []byte("hello world"),
    memcache.StoreOptions{TTL: memcache.ExpiresIn(1 * time.Hour)})

// Get
item, _ := client.Get(ctx, "mykey")
if item.Found {
    fmt.Printf("Value: %s\n", item.Value)
}

// Increment a counter. By default a missing key is reported as not found;
// pass CounterOptions.Initial to create it on first use.
initial := uint64(1)
count, _ := client.Increment(ctx, "counter", 1, memcache.CounterOptions{Initial: &initial})
fmt.Printf("Count: %d\n", count.Value)

// Counter deltas and values use memcached's native uint64 representation.
count, _ = client.Decrement(ctx, "counter", 1)
if !count.Found() {
    fmt.Println("Counter does not exist")
}

// Delete
_, _ = client.Delete(ctx, "mykey")
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

## How It's Tested

A cache client's worst failure is returning the wrong value, so every stress
and chaos harness in this repo enforces the same invariant: stored values embed
their key, and any response carrying data for a different key counts as a
protocol desynchronization. Under failure injection, errors are expected —
wrong data never is.

Beyond the unit and integration tests (run in CI against a real memcached, with
the race detector, plus a paired benchmark that flags performance regressions
on every PR), the client is validated with:

- **In-process failure injection** ([`stress/`](stress/)) — connections killed
  mid-stream, injected latency and jitter, per-request server errors, connection
  churn against a saturated pool, latency spikes past the timeout, and full
  server outages.
- **Endurance soak** ([`loadtest/stress/`](loadtest/stress/)) — a 56-hour
  saturation soak against three servers, ~35 billion operations, including
  chaos phases (server kills, a hung server): zero desyncs.
- **Chaos replay** — container-level faults (freeze, kill one node, kill a
  majority) against a live 3-node fleet at ~230k ops/s: errors attributed only
  to the faulted node, breakers open and re-close per node, a frozen node
  degrades throughput instead of stalling the client — zero desyncs across
  188M ops. [Run record](docs/runs/misaki-chaos-2026-07-06/SUMMARY.md).
- **Fleet churn at scale** — 48 real memcached servers under rolling server
  churn, breaker flapping, a mass-freeze of half the fleet, and a total outage:
  182M ops with pool, goroutine, and heap usage flat throughout; a full run
  built with the race detector reported zero data races; a service-discovery
  probe (142 server addresses rolled through a 24-node live set) caught a
  departed-pool reaping leak, fixed in
  [#129](https://github.com/pior/memcache/pull/129).
  [Run record](docs/runs/misaki-churnstress-2026-07-06/SUMMARY.md).
- **Cloud chaos on GCP** ([`loadtest/`](loadtest/)) — orchestrated VM fleets
  with a fault timeline (SIGSTOP freeze, iptables blackhole, netem
  latency+loss, SIGKILL+restart), run same-zone (~475M ops across two clients)
  and cross-region through a ~180ms-RTT shard: zero desyncs in every phase,
  per-shard error attribution, breakers open during each fault and re-close on
  heal, full throughput recovery.
  [Run record](docs/runs/gcp-chaos-2026-07-06/SUMMARY.md).

## Requirements

- Go 1.25+
- Memcached 1.6+ (with meta protocol support)

## License

MIT License - See LICENSE file for details.

## Status

This project is under active development. The meta protocol implementation and
core client features are production-ready — see [How It's Tested](#how-its-tested)
for the validation record — but the API is still pre-v1.0 and may change before
the first stable release.

Contributions and feedback are welcome!
