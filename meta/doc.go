// Package meta provides a low-level wire protocol implementation for the
// Memcached Meta Protocol (version 1.6+).
//
// This package serves as a foundation for building higher-level memcache clients
// with different properties (pipelining, connection pooling, batching, etc.).
// It focuses on correctness and performance for serialization and parsing,
// without imposing architectural decisions on clients.
//
// # Core Types
//
// Request and Response are pure data containers without embedded logic:
//
//   - Request: Represents a meta protocol command (mg, ms, md, ma, me, mn)
//   - Response: Represents a parsed server response
//   - Flag: Represents a protocol flag with optional token
//
// # Serialization and Parsing
//
// WriteRequest serializes requests to wire format:
//
//	req := meta.NewMetaGetRequest("mykey", meta.Flag{Type: 'v'})
//	n, err := meta.WriteRequest(conn, req)
//
// ReadResponse parses responses from wire format:
//
//	resp, err := meta.ReadResponse(bufio.NewReader(conn))
//	if err != nil {
//	    if meta.ShouldCloseConnection(err) {
//	        conn.Close()
//	    }
//	    return err
//	}
//
// # Batch Operations
//
// For pipelining multiple requests:
//
//	reqs := []*meta.Request{
//	    meta.NewMetaGetRequest("key1", meta.Flag{Type: 'v'}, meta.Flag{Type: 'q'}),
//	    meta.NewMetaGetRequest("key2", meta.Flag{Type: 'v'}, meta.Flag{Type: 'q'}),
//	    meta.NewMetaGetRequest("key3", meta.Flag{Type: 'v'}),
//	    meta.NewMetaNoOpRequest(),
//	}
//	for _, req := range reqs {
//	    meta.WriteRequest(conn, req)
//	}
//
//	// Read responses until NoOp marker
//	r := bufio.NewReader(conn)
//	var resps []*meta.Response
//	for {
//	    resp, err := meta.ReadResponse(r)
//	    if err != nil || resp.Status == meta.StatusMN {
//	        break
//	    }
//	    resps = append(resps, resp)
//	}
//
// # Error Handling
//
// The package defines error types that indicate connection state:
//
//   - ClientError: Protocol state corrupted, CLOSE connection
//   - ServerError: Server-side error, connection can be REUSED
//   - GenericError: Unknown command or protocol issue, CLOSE connection
//   - ParseError: Client-side parsing failure, CLOSE connection
//   - ConnectionError: Network/I/O error, connection already broken
//
// Use ShouldCloseConnection to determine error handling strategy:
//
//	if err != nil {
//	    if meta.ShouldCloseConnection(err) {
//	        conn.Close()
//	    }
//	    return err
//	}
//
// # Constants
//
// All protocol constants are defined:
//
//   - Commands: CmdMetaGet, CmdMetaSet, CmdMetaDelete, CmdMetaArithmetic, etc.
//   - Response codes: StatusHD, StatusVA, StatusEN, StatusNF, StatusNS, StatusEX, etc.
//   - Flags: FlagReturnValue, FlagReturnCAS, FlagTTL, FlagQuiet, etc.
//   - Modes: ModeSet, ModeAdd, ModeReplace, ModeAppend, ModePrepend, etc.
//   - Limits: MaxKeyLength, MaxOpaqueLength, MaxValueSize
//
// # Design Principles
//
// 1. Zero business logic - just serialization and parsing
// 2. No connection management - caller controls connections
// 3. No validation - assumes well-formed requests (for performance)
// 4. Minimal allocations - efficient memory usage
// 5. Clear error semantics - connection state is explicit
//
// # Examples
//
// Basic get:
//
//	req := meta.NewMetaGetRequest("mykey", meta.Flag{Type: 'v'})
//	meta.WriteRequest(conn, req)
//	resp, _ := meta.ReadResponse(bufio.NewReader(conn))
//	if resp.HasValue() {
//	    value := resp.Data
//	}
//
// Set with TTL:
//
//	req := meta.NewMetaSetRequest("mykey", []byte("hello"),
//	    meta.Flag{Type: 'T', Token: "60"})
//	meta.WriteRequest(conn, req)
//	resp, _ := meta.ReadResponse(bufio.NewReader(conn))
//
// CAS operation:
//
//	req := meta.NewMetaSetRequest("mykey", []byte("new"),
//	    meta.Flag{Type: 'C', Token: "12345"})
//	meta.WriteRequest(conn, req)
//	resp, _ := meta.ReadResponse(bufio.NewReader(conn))
//	if resp.IsCASMismatch() {
//	    // Handle CAS conflict
//	}
//
// Increment counter:
//
//	req := meta.NewMetaArithmeticRequest("counter",
//	    meta.Flag{Type: 'v'},
//	    meta.Flag{Type: 'D', Token: "5"})
//	meta.WriteRequest(conn, req)
//	resp, _ := meta.ReadResponse(bufio.NewReader(conn))
//
// Stale-while-revalidate pattern:
//
//	// Invalidate item
//	req := meta.NewMetaDeleteRequest("mykey",
//	    meta.Flag{Type: 'I'},
//	    meta.Flag{Type: 'T', Token: "30"})
//	meta.WriteRequest(conn, req)
//	resp, _ := meta.ReadResponse(bufio.NewReader(conn))
//
//	// Get stale value with win flag
//	req = meta.NewMetaGetRequest("mykey", meta.Flag{Type: 'v'})
//	meta.WriteRequest(conn, req)
//	resp, _ = meta.ReadResponse(bufio.NewReader(conn))
//	if resp.HasWinFlag() {
//	    // Client won the race to recache
//	    // Fetch fresh data and update cache
//	}
//
// # Performance Considerations
//
// - Use bufio.Reader for efficient response reading
// - Reuse buffers where possible to reduce allocations
// - WriteRequest writes directly to io.Writer without intermediate buffers
// - ReadResponse allocates only for data blocks
// - Flag parsing is optimized for minimal allocations
//
// # Thread Safety
//
// This package is thread-safe for reads (constants, helper functions).
// Request and Response types are not thread-safe - callers must synchronize
// access if sharing across goroutines.
//
// Serialization (WriteRequest) and parsing (ReadResponse) are thread-safe
// as long as different io.Writer/Reader instances are used per goroutine.
package meta
