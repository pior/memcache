# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

The library provides a high-level `Client` with multi-server support, circuit
breakers, and connection pooling, built on top of low-level building blocks (a
meta protocol codec, connections, command helpers, and pluggable pools) that you
can compose into a custom client.

It depends only on [sony/gobreaker](https://github.com/sony/gobreaker) (circuit
breaker) and [jackc/puddle](https://github.com/jackc/puddle) (default pool).

**Work in Progress**: This is an active development project. The low-level meta protocol implementation is stable, and the high-level client includes production-ready features like multi-server support, circuit breakers, and connection pooling.

## Features

- **Multi-server support** with consistent key distribution
- **Circuit breakers** using [gobreaker](https://github.com/sony/gobreaker) for fault tolerance
- **Connection pooling** with health checks and lifecycle management
- **jackc/puddle pool** (default) and optional channel-based pool
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

Keys are consistently distributed across servers with minimal key movement when servers are added or removed. You can provide a custom `ServerSelector` function if needed.

## Circuit Breakers

Protect your application from cascading failures with built-in circuit breakers:

```go
client := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    CircuitBreakerSettings: &gobreaker.Settings{
        MaxRequests: 3,                // maxRequests in half-open state
        Interval:    time.Minute,      // interval to reset failure counts
        Timeout:     10 * time.Second, // timeout before transitioning to half-open
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
            return counts.Requests >= 10 && failureRatio >= 0.6
        },
    },
})

// Monitor circuit breaker state
stats := client.AllPoolStats()
for _, serverStats := range stats {
    fmt.Printf("Server: %s, Circuit: %s\n",
        serverStats.Addr,
        serverStats.CircuitBreakerState)

    // Access circuit breaker metrics
    counts := serverStats.CircuitBreakerCounts
    fmt.Printf("  Requests: %d, Failures: %d\n",
        counts.Requests,
        counts.TotalFailures)
}
```

## Connection Pooling

The client pools connections per server using jackc/puddle by default. A
channel-based pool is available as an alternative:

```go
client := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    NewPool: memcache.NewChannelPool,
})
```

### Pool Statistics

Monitor connection pool health and usage:

```go
stats := client.AllPoolStats()
for _, serverStats := range stats {
    poolStats := serverStats.PoolStats

    fmt.Printf("Server: %s\n", serverStats.Addr)
    fmt.Printf("  Total Connections: %d\n", poolStats.TotalConns)
    fmt.Printf("  Idle Connections: %d\n", poolStats.IdleConns)
    fmt.Printf("  Active Connections: %d\n", poolStats.ActiveConns)
    fmt.Printf("  Connections Created: %d\n", poolStats.CreatedConns)
    fmt.Printf("  Acquire Errors: %d\n", poolStats.AcquireErrors)

    // Circuit breaker state
    fmt.Printf("  Circuit State: %s\n", serverStats.CircuitBreakerState)
}
```

## Low-Level Building Blocks

The high-level client is assembled from smaller pieces you can use on their own
to build a custom client:

- **`meta` package** — request serialization and response parsing for the
  memcached meta protocol.
- **`Connection`** — a single pooled connection that implements `Executor`.
- **`Commands` / `BatchCommands`** — the command logic (Get, Set, Delete,
  Increment, …) on top of any `Executor`.
- **`Pool`** — a pluggable connection pool interface (puddle and channel-based
  implementations included).

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
