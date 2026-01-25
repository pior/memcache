# Meta Protocol Package

Low-level wire protocol implementation for the Memcached Meta Protocol (version 1.6+).

## Overview

This package provides a minimalist, high-performance implementation of the meta protocol wire format. It focuses on:

- **Serialization**: Converting `Request` to bytes
- **Parsing**: Converting bytes to `Response`
- **Error Handling**: Clear semantics for connection management

This is a foundation package - it does NOT provide:
- Connection pooling
- Request batching logic
- Retry strategies
- High-level client abstractions

## Files

- `constants.go` - All protocol constants (commands, statuses, flags, limits)
- `request.go` - Request type and constructor functions
- `response.go` - Response type and helper methods
- `writer.go` - Request serialization (WriteRequest)
- `reader.go` - Response parsing (ReadResponse, ReadResponseBatch, PeekStatus)
- `errors.go` - Error types with connection state semantics
- `doc.go` - Package documentation
- `meta_test.go` - Comprehensive unit tests
- `bench_test.go` - Performance benchmarks
- `integration_test.go` - Integration tests (requires memcached on 127.0.0.1:11211)
- `example_test.go` - Runnable examples

## Quick Start

### Basic Get

```go
// Create request with fluent API
req := meta.NewRequest(meta.CmdGet, "mykey", nil).AddReturnValue()

// Serialize to connection
err := meta.WriteRequest(conn, req)
if err != nil {
    return err
}

// Parse response
r := bufio.NewReader(conn)
var resp meta.Response
err = meta.ReadResponse(r, &resp)
if err != nil {
    return err
}

// Handle response
if resp.HasValue() {
    fmt.Println("Value:", string(resp.Data))
}
```

### Set with TTL

```go
req := meta.NewRequest(meta.CmdSet, "mykey", []byte("hello"))
req.AddTTL(60)

err := meta.WriteRequest(conn, req)
if err != nil {
    return err
}

r := bufio.NewReader(conn)
var resp meta.Response
err = meta.ReadResponse(r, &resp)
if err != nil {
    return err
}

if resp.IsSuccess() {
    fmt.Println("Stored successfully")
}
```

### CAS Operation

```go
// Get current CAS
r := bufio.NewReader(conn)
var resp meta.Response
req := meta.NewRequest(meta.CmdGet, "mykey", nil).AddReturnCAS()
meta.WriteRequest(conn, req)
meta.ReadResponse(r, &resp)

casValue, _ := resp.CAS()

// Update with CAS
req = meta.NewRequest(meta.CmdSet, "mykey", []byte("new value")).AddCAS(casValue)
meta.WriteRequest(conn, req)
meta.ReadResponse(r, &resp)

if resp.IsCASMismatch() {
    fmt.Println("CAS conflict - value was modified")
}
```

### Pipelining with Quiet Mode

```go
// Build pipeline with fluent API
reqs := []*meta.Request{
    meta.NewRequest(meta.CmdGet, "key1", nil).AddReturnValue().AddQuiet(),
    meta.NewRequest(meta.CmdGet, "key2", nil).AddReturnValue().AddQuiet(),
    meta.NewRequest(meta.CmdGet, "key3", nil).AddReturnValue(),
    meta.NewRequest(meta.CmdNoOp, "", nil),
}

// Send all requests
for _, req := range reqs {
    if err := meta.WriteRequest(conn, req); err != nil {
        return err
    }
}

// Read responses (only hits and final MN)
resps, err := meta.ReadResponseBatch(bufio.NewReader(conn), 0, true)
if err != nil {
    return err
}

for _, resp := range resps {
    if resp.Status == meta.StatusVA {
        fmt.Println("Hit:", string(resp.Data))
    }
}
```

### Increment Counter

```go
req := meta.NewRequest(meta.CmdArithmetic, "counter", nil).
    AddReturnValue().
    AddDelta(5)

r := bufio.NewReader(conn)
var resp meta.Response
meta.WriteRequest(conn, req)
meta.ReadResponse(r, &resp)

if resp.HasValue() {
    fmt.Println("New value:", string(resp.Data))
}
```

### Stale-While-Revalidate

```go
// Invalidate (mark stale)
r := bufio.NewReader(conn)
var resp meta.Response
req := meta.NewRequest(meta.CmdDelete, "mykey", nil).AddInvalidate().AddTTL(30)
meta.WriteRequest(conn, req)
meta.ReadResponse(r, &resp)

// Get stale value
req = meta.NewRequest(meta.CmdGet, "mykey", nil).AddReturnValue()
meta.WriteRequest(conn, req)
meta.ReadResponse(r, &resp)

if resp.Win() {
    fmt.Println("Won the race to recache")
    // Fetch fresh data and update cache
} else if resp.AlreadyWon() {
    fmt.Println("Another client is recaching")
    // Use stale data
}

if resp.Stale() {
    fmt.Println("Value is stale:", string(resp.Data))
}
```

## Error Handling

The package provides clear error semantics for connection management:

```go
r := bufio.NewReader(conn)
var resp meta.Response
err := meta.ReadResponse(r, &resp)
if err != nil {
    // I/O or parse error
    if meta.ShouldCloseConnection(err) {
        conn.Close()
    }
    return err
}

// Check for protocol errors in response
if resp.HasError() {
    if meta.ShouldCloseConnection(resp.Error) {
        conn.Close()
    }
    return resp.Error
}
```

### Error Types

- **ClientError**: CLIENT_ERROR response - MUST close connection (protocol state corrupted)
- **ServerError**: SERVER_ERROR response - can retry on same connection
- **GenericError**: ERROR response - MUST close connection (unknown command)
- **ParseError**: Client-side parse failure - MUST close connection
- **ConnectionError**: Network/I/O error - connection already broken

## Design Principles

1. **No Validation**: Assumes requests are well-formed for performance
   - Caller is responsible for key length (1-250 bytes)
   - Caller is responsible for opaque length (≤32 bytes)
   - No flag conflict detection

2. **No Buffering**: Writes directly to io.Writer
   - Caller should wrap connection in bufio.Writer if desired
   - Reader expects bufio.Reader for efficient line reading

3. **Minimal Allocations**: Optimized for performance
   - Flags parsed in-place
   - Data buffers allocated once
   - String operations minimized

4. **No State**: Stateless functions
   - No connection tracking
   - No request/response matching
   - Caller manages correlation (use Opaque flag if needed)

5. **Clear Semantics**: Error handling is explicit
   - Connection state is part of error interface
   - No hidden connection closes
   - Caller decides retry strategy

## Usage in Higher-Level Clients

### Non-Pipelined Client

```go
type SimpleClient struct {
    conn net.Conn
    r    *bufio.Reader
}

func (c *SimpleClient) Get(key string) ([]byte, error) {
    req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()
    if err := meta.WriteRequest(c.conn, req); err != nil {
        return nil, err
    }

    var resp meta.Response
    err := meta.ReadResponse(c.r, &resp)
    if err != nil {
        if meta.ShouldCloseConnection(err) {
            c.conn.Close()
        }
        return nil, err
    }

    if resp.IsMiss() {
        return nil, nil
    }

    return resp.Data, nil
}
```

### Pipelined Client

```go
type PipelinedClient struct {
    conn     net.Conn
    r        *bufio.Reader
    pending  []*meta.Request
}

func (c *PipelinedClient) GetMany(keys []string) (map[string][]byte, error) {
    // Send all requests with quiet mode
    reqs := make([]*meta.Request, 0, len(keys)+1)
    for _, key := range keys {
        reqs = append(reqs, meta.NewRequest(meta.CmdGet, key, nil).
            AddReturnValue().AddReturnKey().AddQuiet())
    }
    reqs = append(reqs, meta.NewRequest(meta.CmdNoOp, "", nil))

    // Send all requests
    for _, req := range reqs {
        if err := meta.WriteRequest(c.conn, req); err != nil {
            return nil, err
        }
    }

    // Read responses (only hits)
    resps, err := meta.ReadResponseBatch(c.r, 0, true)
    if err != nil {
        return nil, err
    }

    // Build result map
    result := make(map[string][]byte)
    for _, resp := range resps {
        if resp.Status == meta.StatusVA {
            key, _ := resp.Key()
            result[string(key)] = resp.Data
        }
    }

    return result, nil
}
```

## Performance Considerations

1. **Use bufio.Reader**: Essential for efficient response parsing
   ```go
   r := bufio.NewReader(conn)
   var resp meta.Response
   err := meta.ReadResponse(r, &resp)
   ```

2. **Reuse Connections**: Connection setup is expensive
   ```go
   // Keep connection open for multiple requests
   var resp meta.Response
   for _, req := range requests {
       meta.WriteRequest(conn, req)
       meta.ReadResponse(r, &resp)
   }
   ```

3. **Pipeline with Quiet**: Reduce roundtrips
   ```go
   // Only receive responses for hits, not misses
   req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue().AddQuiet()
   ```

4. **Batch Operations**: Send multiple requests at once
   ```go
   for _, req := range requests {
       meta.WriteRequest(conn, req)
   }
   meta.ReadResponseBatch(r, len(requests), false)
   ```

## Testing

Run unit tests:
```bash
go test ./meta/
```

Run with coverage:
```bash
go test -cover ./meta/
```

Run integration tests (requires memcached on 127.0.0.1:11211):
```bash
go test -v ./meta/ -run Integration
```

Run benchmarks:
```bash
go test -bench=. -benchmem ./meta/
```

Example benchmark results (Apple M2):
```
BenchmarkWriteRequest_SmallGet-8           52944490    22.41 ns/op     1 B/op   1 allocs/op
BenchmarkWriteRequest_SmallSet-8           24972447    47.76 ns/op     4 B/op   2 allocs/op
BenchmarkWriteRequest_LargeSet-8           23039833    51.36 ns/op     8 B/op   2 allocs/op
BenchmarkReadResponse_SmallValue-8          2230508   556.0 ns/op  4480 B/op   8 allocs/op
BenchmarkReadResponse_LargeValue-8           873417  1337 ns/op   14610 B/op   8 allocs/op
BenchmarkRoundTrip_SmallGet-8               2237194   535.6 ns/op  4376 B/op   9 allocs/op
```

## References

- Specification: `../spec/META_PROTOCOL_SPEC.md`
- Quick Reference: `../spec/QUICK_REFERENCE.md`
- AI Reference: `../spec/META_PROTOCOL_AI.md`
