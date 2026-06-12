package memcache

import "errors"

// Sentinel errors returned by the client. Check them with errors.Is; they may
// be wrapped with additional context.
var (
	// ErrNotStored is returned when a conditional store is not applied:
	// Add on an existing key, or replace/append/prepend on a missing key.
	ErrNotStored = errors.New("memcache: item not stored")

	// ErrClientClosed is returned by operations issued after Client.Close.
	ErrClientClosed = errors.New("memcache: client is closed")

	// ErrNoServers is returned when the client has no server to talk to.
	ErrNoServers = errors.New("memcache: no servers available")

	// ErrPoolClosed is returned by Pool.Acquire after the pool has been closed.
	ErrPoolClosed = errors.New("memcache: pool is closed")
)

// Operation names used in OpError.Op for operations that are not a single
// meta protocol request.
const (
	// OpBatch is the Op of pipelined batch executions.
	OpBatch = "batch"

	// OpStats is the Op of stats retrievals.
	OpStats = "stats"
)

// OpError records an operation that failed against a specific server,
// following the net.OpError / fs.PathError pattern.
//
// It carries structured context for logging and metrics: which operation,
// against which server. It is not meant for control flow — branch on the
// underlying cause with errors.Is/errors.As, which traverse the wrapping:
//
//	var opErr *memcache.OpError
//	if errors.As(err, &opErr) {
//	    log.Printf("op=%s server=%s: %v", opErr.Op, opErr.Server, err)
//	}
//	if errors.Is(err, context.DeadlineExceeded) { ... }
type OpError struct {
	// Op is the operation that failed: a meta protocol command code
	// ("mg", "ms", ...) or one of the Op* constants (OpBatch, OpStats).
	Op string

	// Key is the cache key, when the operation targets a single key.
	//
	// The key is deliberately NOT part of the Error() message: keys often
	// carry user identifiers (PII) that don't belong in logs, and embedding
	// them would give error messages unbounded cardinality. Read this field
	// explicitly (via errors.As) when the key is wanted.
	Key string

	// Server is the address of the server the operation was routed to.
	Server string

	// Err is the underlying cause: a connection or timeout error, a
	// gobreaker state error, a meta protocol error, etc.
	Err error
}

func (e *OpError) Error() string {
	s := "memcache: " + e.Op
	if e.Server != "" {
		s += " on " + e.Server
	}
	return s + ": " + e.Err.Error()
}

func (e *OpError) Unwrap() error {
	return e.Err
}
