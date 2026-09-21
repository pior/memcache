# memcache

[![Go Reference](https://pkg.go.dev/badge/github.com/pior/memcache.svg)](https://pkg.go.dev/github.com/pior/memcache)

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
| Key distribution | rendezvous hashing by default: reordering the server list never remaps keys, adding or removing one server moves ~1/N of keys — plus a cheaper jump-hash selector, or your own | CRC32 modulo: any change to the server list remaps most keys |
| Failure isolation | per-server circuit breakers with per-server error attribution | errors surface to the caller |
| Batching | `MultiGet`/`MultiSet`/`MultiDelete` for the common cases, plus pipelined batches of arbitrary mixed commands (get + set + increment in one round trip) | `GetMulti` (reads only) |
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
`MultiGet` of 10 keys delivers **1.10M items/s** versus 151k items/s
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

// Every Config field is optional: the zero value selects a documented default.
// The one worth sizing on day one is MaxConnsPerServer, the per-server
// connection limit — it caps how many operations can be in flight to a server
// at once, and callers queue (bounded only by their context) once it is full.
servers := memcache.StaticServers("localhost:11211", "localhost:11212")
client := memcache.NewClient(servers, memcache.Config{
    MaxConnsPerServer: 20,
})
defer client.Close()

ctx := context.Background()

// Set with TTL (memcache.ExpiresAt for an absolute expiration time,
// omit StoreOptions to never expire). Set reports the outcome and the new
// CAS token; the error is reserved for transport failures.
_, _ = client.Set(ctx, "mykey", []byte("hello world"),
    memcache.StoreOptions{TTL: memcache.ExpiresIn(1 * time.Hour)})

// Get. Every result — Item, StoreResult, Counter, and the Status returned by
// Delete and Touch — reports the outcome as a Status; Status.OK() is the one
// way to ask whether the operation took effect.
item, _ := client.Get(ctx, "mykey")
if item.Status.OK() {
    fmt.Printf("Value: %s\n", item.Value)
}

// Increment a counter. By default a missing key is reported as not found;
// set CounterOptions.Create to create it on first use, seeded with Initial.
count, _ := client.Increment(ctx, "counter", 1, memcache.CounterOptions{Create: true, Initial: 1})
fmt.Printf("Count: %d\n", count.Value)

// Counter deltas and values use memcached's native uint64 representation.
count, _ = client.Decrement(ctx, "counter", 1)
if count.Status == memcache.StatusNotFound {
    fmt.Println("Counter does not exist")
}

// Delete
_, _ = client.Delete(ctx, "mykey")

// Batches pipeline the requests to each server concurrently and return one
// result per key, in order. A failure on any server fails the whole batch.
_, _ = client.MultiSet(ctx, []memcache.SetItem{
    {Key: "a", Value: []byte("1")},
    {Key: "b", Value: []byte("2"), Options: memcache.StoreOptions{TTL: memcache.ExpiresIn(time.Minute)}},
})
items, _ := client.MultiGet(ctx, []string{"a", "b", "c"}) // items[2].Status == memcache.StatusNotFound
```

See the [package documentation](https://pkg.go.dev/github.com/pior/memcache#pkg-examples)
for runnable examples of the usual patterns: cache-aside, read-through with a
CAS-guarded write back, counters, and mixed-command batches.

## Multi-Server Support

The client supports multiple memcache servers with consistent key distribution:

```go
servers := memcache.StaticServers(
    "cache1.example.com:11211",
    "cache2.example.com:11211",
    "cache3.example.com:11211",
)

client := memcache.NewClient(servers, memcache.Config{
    MaxConnsPerServer: 10,
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

## Batching

Batching is two things.

**Convenience operations** — `MultiGet`, `MultiSet` and `MultiDelete` cover the
common case. Keys are grouped by server, each group is pipelined as one
round trip, groups run concurrently, and results come back in the order the
keys were passed:

```go
items, err := client.MultiGet(ctx, []string{"a", "b", "c"})
```

**Batches of arbitrary operations** — `ExecuteBatch` pipelines any mix of meta
commands, which is what the legacy protocol's `GetMulti` cannot express. Build
`meta.Request` values and read the `meta.Response` values back by position:

```go
reqs := []*meta.Request{
    meta.NewRequest(meta.CmdGet, "profile:42", nil).AddReturnValue().AddReturnCAS(),
    meta.NewRequest(meta.CmdSet, "session:42", []byte("live")).AddTTL(300),
    meta.NewRequest(meta.CmdArithmetic, "hits:42", nil).
        AddModeIncrement().AddDelta(1).AddInitialValue(0).AddVivify(3600).AddReturnValue(),
}
resps, err := client.ExecuteBatch(ctx, reqs) // resps[i] answers reqs[i]
```

`ExecuteBatch` responses are owned by the caller, so their values can be kept
without copying. Quiet requests are rejected: they suppress responses, which
would break the by-position matching.

`OperationTimeout` bounds each response read, not the whole batch, so pass a
context with a deadline to cap the total.

## Circuit Breakers

Each server gets its own circuit breaker: when a server's recent failure ratio
trips it, operations to that server fail fast with `memcache.ErrBreakerOpen`
instead of tying up connections. The defaults trip at 60% failures over the
last 10 seconds (with at least 10 operations observed) and retest the server
after 5 seconds:

```go
client := memcache.NewClient(servers, memcache.Config{
    MaxConnsPerServer: 10,
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
invalid keys) do not.

> **Give callers a budget looser than `OperationTimeout`.** A timeout counts
> against the server only when `OperationTimeout` is the binding deadline. If
> every caller passes a context deadline at or below `OperationTimeout`, a hung
> server's timeouts are attributed to the caller and excluded, so the breaker
> never opens and every operation keeps paying the full timeout. A caller
> context with no deadline is capped at `OperationTimeout` and works too.

Detect rejected operations with
`errors.Is(err, memcache.ErrBreakerOpen)`, and monitor the breakers through
`client.PoolMetrics()`:

```go
for _, m := range client.PoolMetrics() {
    fmt.Printf("Server: %s, Circuit: %s\n", m.Address, m.Breaker.State)
    fmt.Printf("  Requests: %d, Failures: %d\n",
        m.Breaker.Requests,
        m.Breaker.TotalFailures)
}
```

## Connection Pooling

The client pools connections per server (backed by jackc/puddle), up to
`MaxConnsPerServer` connections per pool. Connection lifecycle is controlled by
`MaxConnLifetime`, `MaxConnIdleTime`, and `MaintenanceInterval`.

### Pool Statistics

Monitor connection pool health and usage:

```go
for _, m := range client.PoolMetrics() {
    fmt.Printf("Server: %s\n", m.Address)
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
- **Dial** — bounded by `DialTimeout` (defaults to `OperationTimeout`).
- **I/O** — bounded by the earlier of the context deadline and
  `now + OperationTimeout`, so even a caller with a far-future deadline cannot
  be stalled by a hung-but-connected server. The cap cannot be disabled — a
  non-positive `OperationTimeout` selects the default; set a large value when a
  long budget is genuinely needed.

The intent is that a cache client fails fast: a timeout is a fast failure the
caller is expected to tolerate. For reads that means falling back to the origin,
like a miss. A timed-out write is ambiguous (the server may or may not have
applied it), so callers that need certainty must verify or accept the ambiguity.

Checkout waits show up in the pool metrics (`AcquireWaitCount`,
`AcquireWaitDuration`), and errors during checkout are prefixed with `acquire:`.

## Observability

**`Config.Observer`** is invoked around every operation, enabling tracing and
metrics without coupling the core to any telemetry backend. It is off by default
and has no impact when unset. `StartOp` returns a context (so a span propagates
to nested work) and an `ActiveOp`; the client calls `ActiveOp.End` once with the
outcome and any error — the same `tracer.Start` → `span.End` shape OpenTelemetry
uses. Keys are never passed to telemetry by the shipped adapter — only counts —
matching the client's policy of keeping keys out of errors.

The operation is named, not coded: `OpInfo.Op` is `"get"`, `"touch"`, `"add"`,
… (the `Op*` constants), never the `mg`/`ms` command that carries it, and the
same names appear in `OpError.Op`. `OpResult.Status` is the interpreted
outcome the caller sees, so an add that found the key (`StatusExists`) and a
replace that did not (`StatusNotFound`) are distinct although the server
answered `NS` to both; `OpResult.Code` carries that raw code.

A ready-made OpenTelemetry tracing adapter ships as a separate module, so the
OTel dependency tree never leaks into consumers of the core package:

```go
import "github.com/pior/memcache/otelmemcache"

client := memcache.NewClient(servers, memcache.Config{
    Observer: otelmemcache.New(tracerProvider), // one client span per operation
})
```

Keys are excluded from spans by default. If your keys are safe to export to your
tracing backend, opt in with `otelmemcache.New(tracerProvider, otelmemcache.Options{RecordKeys: true})`
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
- **`Commands`** — the command logic (Get, Set, Delete, Increment, MultiGet, …)
  on top of any `Executor`.

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

- Go 1.26+
- Memcached 1.6+ (with meta protocol support)

## License

MIT License - See LICENSE file for details.

## Status

The meta protocol implementation and core client features are production-ready
— see [How It's Tested](#how-its-tested) for the validation record — but the API
is pre-v1.0 and not yet frozen. A breaking change bumps the minor version
(v0.1.0 → v0.2.0) and is listed in [CHANGELOG.md](CHANGELOG.md); pin a version
and read the changelog before upgrading. [RELEASING.md](RELEASING.md) describes
how releases are cut.

Contributions and feedback are welcome!
