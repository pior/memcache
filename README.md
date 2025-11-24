# memcache

A modern memcache client for Go implementing the [meta protocol](https://github.com/memcached/memcached/wiki/MetaCommands).

**Work in Progress**: This is an active development project. The low-level meta protocol implementation is stable, but the high-level client API is evolving based on real-world use cases.

## Features

### Low-Level Meta Protocol (`meta` package)
- Complete meta protocol implementation (get, set, delete, arithmetic, debug)
- Zero-copy response parsing
- Pipelined request batching
- Comprehensive error handling with connection state management
- Extensively tested and benchmarked

### High-Level Client (In Development)
- Connection pooling with health checks and lifecycle management
- Custom channel-based pool (fastest) and optional puddle-based pool
- Context support for timeouts and cancellation
- Type-safe operations

## Installation

```bash
go get github.com/pior/memcache
```

## Quick Start

### Using the Meta Protocol Directly

```go
import (
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

### Using the High-Level Client

```go
import (
    "context"
    "github.com/pior/memcache"
)

// Create client
client, _ := memcache.NewClient("localhost:11211", memcache.Config{
    MaxSize:             10,
    MaxConnLifetime:     5 * time.Minute,
    MaxConnIdleTime:     1 * time.Minute,
    HealthCheckInterval: 30 * time.Second,
})
defer client.Close()

ctx := context.Background()

// Set
_ = client.Set(ctx, "mykey", []byte("hello world"), 3600)

// Get
value, _ := client.Get(ctx, "mykey")
fmt.Printf("Value: %s\n", value)

// Delete
_ = client.Delete(ctx, "mykey")
```

## Connection Pooling

### Default Channel Pool (Recommended)

The default pool uses Go channels for fast, lock-free connection management:

```go
client, _ := memcache.NewClient("localhost:11211", memcache.Config{
    MaxSize: 10,  // Uses channel pool by default
})
```

### Optional Puddle Pool

For compatibility with puddle-based systems, a puddle pool is available:

```go
// Build with: go build -tags=puddle
client, _ := memcache.NewClient("localhost:11211", memcache.Config{
    MaxSize: 10,
    Pool:    memcache.NewPuddlePool,  // Requires -tags=puddle
})
```

**Performance**: Channel pool is ~40% faster than puddle on the fast path (69ns vs 94ns per acquire/release cycle).

## Planned High-Level Features

The client API will expand to support:

- **Distributed locking**: Using CAS operations
- **Thundering herd protection**: Using the `W` (win) flag for cache stampede prevention
- **CAS operations**: For atomic updates
- **Multi-server support**: Consistent hashing and server discovery
- **Pool statistics**: Monitoring connection health and usage
- **Pluggable server discovery**: Custom strategies for finding memcache servers
- **Batch operations**: Efficient multi-key get/set/delete

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
├── client.go          # High-level client
├── pool_channel.go    # Channel-based connection pool
├── pool_puddle.go     # Puddle-based pool (requires -tags=puddle)
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

The main module has **zero external dependencies** - only the Go standard library.

Optional dependencies (with build tags):
- `github.com/jackc/puddle/v2` (with `-tags=puddle`)

Command-line tools (in `cmd/`) have their own go.mod files with separate dependencies.

## Requirements

- Go 1.25+
- Memcached 1.6+ (with meta protocol support)

## License

MIT License - See LICENSE file for details.

## Status

This project is under active development. The meta protocol implementation is production-ready, but the high-level client API is evolving. Breaking changes may occur before v1.0.

Contributions and feedback are welcome!
