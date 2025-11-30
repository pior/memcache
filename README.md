# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

**Work in Progress**: This is an active development project. The low-level meta protocol implementation is stable, and the high-level client includes production-ready features like multi-server support, circuit breakers, and connection pooling.

## Features

### Low-Level Meta Protocol (`meta` package)
- Complete meta protocol implementation (get, set, delete, arithmetic, debug)
- Zero-copy response parsing
- Pipelined request batching
- Comprehensive error handling with connection state management
- Extensively tested and benchmarked

### High-Level Client
- **Multi-server support** with CRC32-based consistent hashing
- **Circuit breakers** using [gobreaker](https://github.com/sony/gobreaker) for fault tolerance
- **Connection pooling** with health checks and lifecycle management
- **Custom channel-based pool** (fastest) and optional puddle-based pool
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
    SelectServer: memcache.DefaultSelectServer,
})
```

The client uses CRC32-based consistent hashing by default for key distribution across servers.

## Circuit Breakers

Protect your application from cascading failures with built-in circuit breakers:

```go
client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    NewCircuitBreaker: memcache.NewCircuitBreakerConfig(
        3,              // maxRequests in half-open state
        time.Minute,    // interval to reset failure counts
        10*time.Second, // timeout before transitioning to half-open
    ),
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

### Custom Circuit Breaker

```go
import "github.com/sony/gobreaker/v2"

client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    NewCircuitBreaker: func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response] {
        settings := gobreaker.Settings{
            Name:        serverAddr,
            MaxRequests: 5,
            Interval:    30 * time.Second,
            Timeout:     5 * time.Second,
            ReadyToTrip: func(counts gobreaker.Counts) bool {
                failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
                return counts.Requests >= 5 && failureRatio >= 0.5
            },
            OnStateChange: func(name string, from, to gobreaker.State) {
                fmt.Printf("Circuit %s: %s -> %s\n", name, from, to)
            },
        }
        return gobreaker.NewCircuitBreaker[*meta.Response](settings)
    },
})
```

## Connection Pooling

### Default Channel Pool (Recommended)

The default pool uses Go channels for fast, lock-free connection management:

```go
client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,  // Uses channel pool by default
})
```

### Optional Puddle Pool

For compatibility with puddle-based systems, a puddle pool is available:

```go
// Build with: go build -tags=puddle
client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize: 10,
    Pool:    memcache.NewPuddlePool,  // Requires -tags=puddle
})
```

**Performance**: Channel pool is ~40% faster than puddle on the fast path (69ns vs 94ns per acquire/release cycle).

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

    // Direct execution without pooling
    return connection.Send(req)
}

// Create Commands with custom executor
commands := memcache.NewCommands(executeFunc)

// Use commands
item, _ := commands.Get(ctx, "mykey")
_ = commands.Set(ctx, memcache.Item{Key: "key", Value: []byte("value")})
```

This allows you to build custom clients with different execution strategies while reusing the command logic.

## Project Structure

```
.
├── meta/              # Low-level meta protocol implementation
│   ├── constants.go   # Protocol constants and status codes
│   ├── request.go     # Request building
│   ├── writer.go      # Request serialization
│   ├── reader.go      # Response parsing
│   ├── response.go    # Response utilities
│   └── errors.go      # Protocol error types
├── client.go          # High-level multi-server client
├── commands.go        # Reusable command operations
├── servers.go         # Server discovery and selection
├── circuit_breaker.go # Circuit breaker configuration
├── pool_channel.go    # Channel-based connection pool
├── pool_puddle.go     # Puddle-based pool (requires -tags=puddle)
├── stats.go           # Pool statistics
└── cmd/
    ├── speed/         # Benchmarking tool (separate module)
    └── tester/        # Protocol testing tool (separate module)
```

## Testing

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...

# Run with puddle pool
go test -tags=puddle -bench=BenchmarkPool ./...
```

## Dependencies

Core dependencies:
- `github.com/sony/gobreaker/v2` - Circuit breaker implementation

Optional dependencies (with build tags):
- `github.com/jackc/puddle/v2` (with `-tags=puddle`)

Command-line tools (in `cmd/`) have their own go.mod files with separate dependencies.

## Requirements

- Go 1.24+
- Memcached 1.6+ (with meta protocol support)

## Roadmap

Future enhancements:

- **Distributed locking**: Using CAS operations
- **Thundering herd protection**: Using the `W` (win) flag for cache stampede prevention
- **Batch operations**: Efficient multi-key get/set/delete
- **Custom server discovery**: Dynamic server list updates
- **Metrics integration**: Prometheus/OpenTelemetry exporters

## License

MIT License - See LICENSE file for details.

## Status

This project is under active development. The meta protocol implementation and core client features (multi-server, circuit breakers, pooling) are production-ready. The API is stabilizing but breaking changes may occur before v1.0.

Contributions and feedback are welcome!
