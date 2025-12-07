# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

**Work in Progress**: This is an active development project. The low-level meta protocol implementation is stable, and the high-level client includes production-ready features like multi-server support, circuit breakers, and connection pooling.

## Features

### Low-Level Meta Protocol (`meta` package)
- Meta protocol implementation (get, set, delete, arithmetic, debug)
- Pipelined request batching
- Error handling with connection state management

### High-Level Client
- **Multi-server support** with CRC32-based consistent hashing
- **Circuit breakers** using [gobreaker](https://github.com/sony/gobreaker) for fault tolerance
- **Connection pooling** with health checks and lifecycle management
- **jackc/puddle pool** (default) and optional channel-based pool
- **Pool statistics** for monitoring connection health and usage
- **Reusable Commands** struct for building custom clients
- Context support for timeouts and cancellation
- Type-safe operations

## Installation

```bash
go get github.com/pior/memcache
```

## Quick Start

### Using the High-Level Client

```go
import (
    "context"
    "time"
    "github.com/pior/memcache"
)

// Create client with static servers
servers := memcache.NewStaticServers("localhost:11211", "localhost:11212")
client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize:             10,
    MaxConnLifetime:     5 * time.Minute,
    MaxConnIdleTime:     1 * time.Minute,
    HealthCheckInterval: 30 * time.Second,
})
defer client.Close()

ctx := context.Background()

// Set with TTL
_ = client.Set(ctx, memcache.Item{
    Key:   "mykey",
    Value: []byte("hello world"),
    TTL:   1 * time.Hour,
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

### Using the Meta Protocol Directly

```go
import (
    "bufio"
    "net"
    "github.com/pior/memcache/meta"
)

// Create connection
conn, _ := net.Dial("tcp", "localhost:11211")
defer conn.Close()

// Write request
req := meta.NewRequest(meta.CmdSet, "mykey", []byte("hello world"), []meta.Flag{
    {Type: meta.FlagTTL, Token: "3600"},
})
meta.WriteRequest(conn, req)

// Read response
r := bufio.NewReader(conn)
resp, _ := meta.ReadResponse(r)
if resp.Status == meta.StatusHD {
    fmt.Println("Stored!")
}
```

## Multi-Server Support

The client supports multiple memcache servers with automatic server selection:

```go
// Static server list
servers := memcache.NewStaticServers(
    "cache1.example.com:11211",
    "cache2.example.com:11211",
    "cache3.example.com:11211",
)

client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    // Optional: Custom server selection (default is CRC32-based)
    // Alternative: memcache.JumpSelectServer for Jump Hash
    SelectServer: memcache.DefaultSelectServer,
})
```

The client uses CRC32-based consistent hashing by default for key distribution across servers.
Alternatively, JumpSelectServer provides Jump Hash algorithm for better distribution properties.

## Circuit Breakers

Protect your application from cascading failures with built-in circuit breakers:

```go
client, _ := memcache.NewClient(servers, memcache.Config{
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

### Optional Channel based Pool

```go
client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    Pool:    memcache.NewChannelPool,
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

## Reusable Commands

The `Commands` struct provides a reusable, composable way to execute memcache operations:

```go
// Create a custom execute function
executeFunc := func(ctx context.Context, key string, req *meta.Request) (*meta.Response, error) {
    conn, _ := net.Dial("tcp", "localhost:11211")
    defer conn.Close()

    connection := memcache.NewConnection(conn)
    // Direct execution without pooling
    return connection.Execute(ctx, req)
}

// Create Commands with custom executor
commands := memcache.NewCommands(executeFunc)

// Use commands
item, _ := commands.Get(ctx, "mykey")
_ = commands.Set(ctx, memcache.Item{Key: "key", Value: []byte("value")})
```

This allows you to build custom clients with different execution strategies while reusing the command logic.

## Testing

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...

# Run with channel pool
go test -bench=BenchmarkPool ./...
```

## Dependencies

Core dependencies:
- `github.com/sony/gobreaker/v2` - Circuit breaker implementation
- `github.com/jackc/puddle/v2` - Default pool implementation

Command-line tools (in `cmd/`) have their own go.mod files with separate dependencies.

## Requirements

- Go 1.24+
- Memcached 1.6+ (with meta protocol support)

## License

MIT License - See LICENSE file for details.

## Status

This project is under active development. The meta protocol implementation and core client features (multi-server, circuit breakers, pooling) are production-ready. The API is stabilizing but breaking changes may occur before v1.0.

Contributions and feedback are welcome!
