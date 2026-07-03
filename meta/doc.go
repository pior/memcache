package meta

// Memcached Meta Protocol (version 1.6+).
//
// Package meta provides low-level request serialization and response parsing for
// the memcached meta protocol.
//
// It is intended as a foundation for higher-level clients (connection pooling,
// batching, pipelining, consistent hashing, etc.).
//
// # Core Types
//
// Request and Response are low-level data containers:
//   - Request: represents a meta protocol command (mg, ms, md, ma, me, mn, stats)
//   - Response: represents a parsed server response
//   - Flags: serialized meta protocol flags (wire-ready bytes)
//
// # Serialization
//
// WriteRequest serializes a Request to wire format:
//
//	req := meta.NewRequest(meta.CmdGet, "mykey", nil).AddReturnValue()
//	_ = meta.WriteRequest(conn, req)
//
// All Add* methods support fluent chaining:
//
//	req := meta.NewRequest(meta.CmdGet, "mykey", nil).
//		AddReturnValue().
//		AddReturnCAS().
//		AddReturnTTL()
//
// # Parsing
//
// ReadResponse parses responses from wire format:
//
//	r := bufio.NewReader(conn)
//	var resp meta.Response
//	err := meta.ReadResponse(r, &resp)
//	if err != nil {
//		if meta.ShouldCloseConnection(err) {
//			_ = conn.Close()
//		}
//		return err
//	}
//	if resp.HasError() {
//		return resp.Error
//	}
//	if resp.HasValue() {
//		value := resp.Data
//		_ = value
//	}
//
// # Response Reuse and Ownership
//
// A Response owns its Data and Flags backing arrays and can be reused for every
// response on a connection:
//
//	var resp meta.Response
//	for {
//		if err := meta.ReadResponse(r, &resp); err != nil {
//			return err
//		}
//		// Consume resp before the next ReadResponse call using &resp.
//	}
//
// ReadResponse reuses reasonably sized slice capacity and releases buffers
// larger than 1 MiB on the next call. Fields remain valid until the same
// Response is reused or modified. Use a separate Response, or clone Data and
// Flags, when a parsed response must remain independent and longer-lived.
//
// # Error Handling
//
// The package defines error types that indicate connection state.
// Use ShouldCloseConnection to determine whether the connection can be reused.
//
// # Performance
//
// Flags are stored in serialized form to minimize allocations and make request
// writing fast (single append/write). ReadResponse parses flags into the same
// serialized representation and reuses caller-owned response buffers.
