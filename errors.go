package memcache

import (
	"errors"

	"github.com/pior/memcache/meta"
)

// Sentinel errors returned by the client. Check them with errors.Is; they may
// be wrapped with additional context.
var (
	// ErrClientClosed is returned by operations issued after Client.Close.
	ErrClientClosed = errors.New("memcache: client is closed")

	// ErrNoServers is returned when the client has no server to talk to.
	ErrNoServers = errors.New("memcache: no servers available")

	// ErrPoolClosed is returned by operations that acquire a connection from
	// a closed pool, which can happen when an operation races with Close.
	ErrPoolClosed = errors.New("memcache: pool is closed")

	// ErrBreakerOpen is returned when the server's circuit breaker rejects
	// the operation without attempting it: the breaker is open after too
	// many recent failures, or it is half-open and its probe quota is
	// already in flight. See Config.Breaker.
	ErrBreakerOpen = errors.New("memcache: circuit breaker open")
)

// Protocol error types, aliased from the meta package. An operation's failure
// can therefore be classified — is the server unhealthy, or did the client
// send something invalid? — without importing the low-level package:
//
//	if srvErr, ok := errors.AsType[*memcache.ServerError](err); ok {
//	    // the server refused the operation (out of memory, internal error);
//	    // the connection was fine and the operation may be retried
//	}
//
// They are reached through the [OpError] wrapping, so use errors.AsType (or
// errors.As), not a type assertion.
type (
	// ClientError is a CLIENT_ERROR reply: the server rejected the request as
	// malformed. The client closes the connection, since the protocol state is
	// then undefined.
	ClientError = meta.ClientError

	// ServerError is a SERVER_ERROR reply: the server failed the operation
	// (out of memory, internal error). The connection stays usable.
	ServerError = meta.ServerError

	// GenericError is a bare ERROR reply: an unknown command or a protocol
	// violation. The client closes the connection.
	GenericError = meta.GenericError

	// InvalidRequestError is a request rejected by client-side validation,
	// before any byte reached the wire.
	InvalidRequestError = meta.InvalidRequestError

	// ParseError is a server reply the client could not parse.
	ParseError = meta.ParseError

	// ConnectionError wraps an I/O failure on the connection.
	ConnectionError = meta.ConnectionError
)

// Operation names used in OpError.Op for operations that are not a single
// meta protocol request.
const (
	// OpBatch is the Op of pipelined batch executions.
	OpBatch = "batch"

	// OpStats is the Op of stats retrievals.
	OpStats = "stats"

	// OpFlushAll is the Op of all-server cache invalidation.
	OpFlushAll = "flush_all"
)

// OpError records an operation that failed against a specific server,
// following the net.OpError / fs.PathError pattern.
//
// It carries structured context for logging and metrics: which operation,
// against which server. It is not meant for control flow — branch on the
// underlying cause with errors.Is/errors.AsType, which traverse the wrapping:
//
//	if opErr, ok := errors.AsType[*memcache.OpError](err); ok {
//	    log.Printf("op=%s server=%s: %v", opErr.Op, opErr.Address, err)
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
	// explicitly (via errors.AsType) when the key is wanted.
	Key string

	// Address is the host:port of the server the operation was routed to.
	Address string

	// Err is the underlying cause: a connection or timeout error,
	// ErrBreakerOpen, a meta protocol error, etc.
	Err error
}

func (e *OpError) Error() string {
	s := "memcache: " + e.Op
	if e.Address != "" {
		s += " on " + e.Address
	}
	return s + ": " + e.Err.Error()
}

func (e *OpError) Unwrap() error {
	return e.Err
}
