# Memcache Client for Go

A high-performance, production-ready memcache client for Go that implements the memcache meta protocol. This client is designed for scalability, reliability, and ease of use.

## Features

- **Meta Protocol Only**: Implements the modern memcache meta protocol for better performance and features
- **Connection Pooling**: Efficient connection management with configurable pool sizes
- **Consistent Hashing**: Reliable server selection with virtual nodes for even distribution
- **Concurrent Operations**: Thread-safe operations with batching support
- **Comprehensive Testing**: Extensive unit tests, benchmarks, and fuzz tests
- **CLI Tools**: Interactive CLI and benchmarking tools for testing and debugging

## Installation

```bash
go get github.com/pior/memcache
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/pior/memcache"
)

func main() {
    // Create client with default configuration
    client, err := memcache.NewClient(nil)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    ctx := context.Background()

    // Set a value
    item := memcache.NewItem("hello", []byte("world"))
    if err := client.Set(ctx, item); err != nil {
        log.Fatal(err)
    }

    // Get the value
    item, err = client.Get(ctx, "hello")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Value: %s\n", string(item.Value))
}
```

## Configuration

### Client Configuration

```go
config := &memcache.ClientConfig{
    Servers: []string{"localhost:11211", "localhost:11212"},
    PoolConfig: &memcache.PoolConfig{
        MinConnections: 2,
        MaxConnections: 10,
        ConnTimeout:    5 * time.Second,
        IdleTimeout:    5 * time.Minute,
    },
    HashRing: &memcache.HashRingConfig{
        VirtualNodes: 160,
    },
}

client, err := memcache.NewClient(config)
```

### Pool Configuration

- **MinConnections**: Minimum number of connections to maintain per server
- **MaxConnections**: Maximum number of connections per server
- **ConnTimeout**: Timeout for establishing new connections
- **IdleTimeout**: Time before idle connections are closed

### Hash Ring Configuration

- **VirtualNodes**: Number of virtual nodes per server for consistent hashing (more nodes = better distribution)

## API Reference

### Basic Operations

```go
// Get a single item
item, err := client.Get(ctx, "key")

// Set an item
item := memcache.NewItem("key", []byte("value"))
item.SetTTL(time.Hour)
err := client.Set(ctx, item)

// Delete an item
err := client.Delete(ctx, "key")
```

### Batch Operations

```go
// Get multiple items
results, err := client.GetMulti(ctx, []string{"key1", "key2", "key3"})

// Set multiple items
items := []*memcache.Item{
    memcache.NewItem("key1", []byte("value1")),
    memcache.NewItem("key2", []byte("value2")),
}
err := client.SetMulti(ctx, items)

// Delete multiple items
err := client.DeleteMulti(ctx, []string{"key1", "key2"})
```

### Utility Operations

```go
// Ping all servers
err := client.Ping(ctx)

// Get server statistics
stats := client.Stats()
for _, stat := range stats {
    fmt.Printf("Server: %s, Connections: %d\n", stat.Address, stat.ActiveConnections)
}
```

### Working with Items

```go
item := memcache.NewItem("user:123", []byte(`{"name": "John"}`))

// Set TTL
item.SetTTL(time.Hour)

// Set flags for metadata
item.SetFlag("format", "json")
item.SetFlag("version", "1")

// Get flags
format, exists := item.GetFlag("format")
```

## Error Handling

The client defines several specific error types:

```go
// Check for cache miss
item, err := client.Get(ctx, "key")
if err == memcache.ErrCacheMiss {
    // Handle cache miss
} else if err != nil {
    // Handle other errors
}

// Other errors
var (
    ErrCacheMiss     = errors.New("memcache: cache miss")
    ErrKeyTooLong    = errors.New("memcache: key too long")
    ErrEmptyKey      = errors.New("memcache: empty key")
    ErrServerError   = errors.New("memcache: server error")
    ErrClientClosed  = errors.New("memcache: client closed")
)
```

## CLI Tools

### Interactive CLI

```bash
go build -o memcache-cli ./cmd/memcache-cli
./memcache-cli
```

Commands:
- `get <key>` - Get a value by key
- `set <key> <value> [ttl]` - Set a key-value pair with optional TTL
- `delete <key>` - Delete a key
- `multi-get <key1> <key2>` - Get multiple keys at once
- `stats` - Show server statistics
- `ping` - Ping all servers

### Benchmark Tool

```bash
go build -o memcache-bench ./cmd/memcache-bench
./memcache-bench -requests 10000 -concurrency 50 -operation mixed
```

Options:
- `-requests` - Number of requests to send
- `-concurrency` - Number of concurrent workers
- `-key-size` - Size of keys in bytes
- `-value-size` - Size of values in bytes
- `-operation` - Operation type: get, set, delete, or mixed
- `-ttl` - TTL for set operations in seconds

## Testing

### Run all tests
```bash
go test -v
```

### Run benchmarks
```bash
go test -bench=.
```

### Run fuzz tests
```bash
go test -fuzz=FuzzFormatGetCommand
go test -fuzz=FuzzParseResponse
```

## Architecture

### Components

1. **Protocol Layer** (`protocol.go`): Meta protocol command formatting and response parsing
2. **Connection Layer** (`connection.go`): Individual TCP connection management
3. **Pool Layer** (`pool.go`): Connection pooling with least-requests-in-flight selection
4. **Selector Layer** (`selector.go`): Server selection using consistent hashing
5. **Client Layer** (`client.go`): High-level API combining all components
6. **Types** (`types.go`): Data structures and utility functions

### Design Principles

- **Separation of Concerns**: Each layer has a single responsibility
- **Thread Safety**: All operations are safe for concurrent use
- **Error Transparency**: Errors are propagated with context
- **Resource Management**: Proper cleanup and connection lifecycle management
- **Performance**: Optimized for high-throughput scenarios

## Protocol Support

This client implements the memcache **meta protocol** only. The meta protocol provides:

- Better performance than text protocol
- Request tracking with opaque values
- Extensible flag system
- Binary-safe operations

### Supported Commands

- `mg` (meta get) - Retrieve items
- `ms` (meta set) - Store items
- `md` (meta delete) - Delete items

## Performance

The client is optimized for:

- **High Throughput**: Efficient connection pooling and batching
- **Low Latency**: Connection reuse and minimal allocations
- **Scalability**: Consistent hashing for even server distribution
- **Reliability**: Comprehensive error handling and connection management

Typical performance (with local memcached):
- **Single operations**: ~100μs latency
- **Batch operations**: ~1000 ops/ms throughput
- **Connection overhead**: Minimal with pooling

## Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass
5. Submit a pull request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Compatibility

- **Go Version**: Requires Go 1.24+ (uses latest Go features)
- **Memcached Version**: Compatible with memcached 1.6+ (meta protocol support)
- **Operating Systems**: Linux, macOS, Windows

## Roadmap

- [ ] TLS support for secure connections
- [ ] Compression support for large values
- [ ] Metrics and observability integration
- [ ] Additional meta protocol features
- [ ] Performance optimizations
