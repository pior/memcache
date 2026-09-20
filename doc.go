// Package memcache is a modern memcache client for Go implementing the
// memcached meta protocol (version 1.6+).
//
// The high-level [Client] is the recommended entry point. It provides
// multi-server support with consistent key distribution, per-server circuit
// breakers, and connection pooling with health checks:
//
//	servers := memcache.StaticServers("localhost:11211", "localhost:11212")
//	client := memcache.NewClient(servers, memcache.Config{
//		MaxConnsPerServer: 20,
//	})
//	defer client.Close()
//
//	_, _ = client.Set(ctx, "mykey", []byte("hello"))
//	item, _ := client.Get(ctx, "mykey")
//
// Every [Config] field is optional: the zero value selects a documented
// default. The one worth sizing on day one is [Config.MaxConnsPerServer],
// which caps how many operations can be in flight to a single server.
//
// The runnable examples below cover the usual patterns: cache-aside,
// read-modify-write guarded by a CAS, counters, and mixed-command batches.
//
// # Key Distribution
//
// Keys are mapped to servers by a [ServerSelector]. The default,
// [StableServerSelector], uses Rendezvous (Highest-Random-Weight) hashing: the
// result depends only on the set of addresses, not their order, so reordering
// the server list never remaps a key and adding or removing one server moves
// only ~1/N of keys. It hashes once per server per call.
//
// [OrderedServerSelector] is the simpler, cheaper consistent-hash strategy —
// Jump Hash, one hash per call regardless of fleet size — but it is
// position-based: inserting or removing a server anywhere but the end remaps a
// large fraction of keys. Use it only for a static, stably ordered list.
//
// Any function of the form
// func(key string, servers []Server) Server satisfies [ServerSelector], so a
// weighted or zone-aware policy can be supplied through
// [Config.ServerSelector].
//
// # Batching
//
// Batching comes in two forms.
//
// The convenience operations — [BatchCommands.MultiGet],
// [BatchCommands.MultiSet] and [BatchCommands.MultiDelete] — cover the common
// case. Keys are grouped by server, each group is pipelined as a single round
// trip, groups run concurrently, and results are returned in the order the
// keys were passed.
//
// [Client.ExecuteBatch] pipelines an arbitrary mix of meta commands — a get, a
// set and an increment in one round trip — by taking [meta.Request] values and
// returning the [meta.Response] values by position. Unlike the responses
// handed to a [ResponseFunc], batch responses are owned by the caller, so
// their values can be kept without copying. Requests using the quiet flag are
// rejected: they suppress responses, which would break the by-position
// matching.
//
// # Circuit Breakers
//
// A circuit breaker can guard each server, so a failing server fails fast with
// [ErrBreakerOpen] instead of tying up connections. It is off unless
// [BreakerConfig.Enabled] is set; see [BreakerConfig] for the policy and its
// defaults.
//
// Only transport-level errors count as failures — dial errors, socket I/O
// errors, and operations cut off by [Config.OperationTimeout]. A cache miss is
// a normal outcome. Errors the caller caused (a canceled context, an expired
// caller deadline, a request rejected by client-side validation) say nothing
// about server health and are not counted in either direction, so an impatient
// caller cannot trip a healthy server's breaker.
//
// That attribution rule has a consequence worth designing for: a timeout
// counts against the server only when Config.OperationTimeout is the binding
// deadline. If every caller passes a context deadline at or below
// OperationTimeout, a hung server's timeouts are attributed to the caller and
// excluded — the breaker never opens, never sheds, and every operation keeps
// paying the full timeout. For a breaker to shed a hung server, callers need a
// budget looser than OperationTimeout (a context with no deadline works too:
// it is capped at OperationTimeout).
//
// # Timeouts
//
// An operation goes through up to three phases, each bounded differently:
//
//   - Pool checkout: waiting for a connection from a saturated pool is bounded
//     only by the caller's context. There is deliberately no separate pool
//     timeout knob; pass a context with a deadline. With context.Background()
//     and a fully busy pool, an operation can wait indefinitely.
//   - Dial: establishing a new connection is bounded by [Config.DialTimeout]
//     (which defaults to [Config.OperationTimeout]).
//   - I/O: socket reads and writes are bounded by the earlier of the context
//     deadline and now plus [Config.OperationTimeout], so even a caller with a
//     far-future deadline cannot be stalled by a hung-but-connected server.
//     The cap cannot be disabled — a non-positive OperationTimeout selects the
//     default; set a large value when a long budget is genuinely needed.
//
// Batch operations (MultiGet, MultiSet, MultiDelete, ExecuteBatch) apply the
// I/O bound per read, not per batch: the deadline is extended before each one
// so that a large batch is not cut short. A batch of n requests takes one
// window to write and n+1 to read (the responses plus the NoOp marker), so a
// slow server can hold it for far longer than a single
// [Config.OperationTimeout]. Only the context deadline caps the total, so
// always pass one on batch calls.
//
// The intent is that a cache client fails fast: a timeout is a fast failure
// the caller is expected to tolerate. For reads that means falling back to the
// origin, like a miss. A timed-out write is different — it is ambiguous (the
// server may or may not have applied it), so callers that need certainty must
// verify or accept the ambiguity.
//
// Checkout waits are visible in [ConnPoolMetrics] (AcquireWaitCount,
// AcquireWaitDuration), and an error during checkout is prefixed with "acquire:"
// to distinguish it from an I/O failure on the wire.
//
// # Errors
//
// A returned error means the operation did not complete: a transport failure,
// a breaker rejection, or a protocol error. It never means "miss" or "not
// stored" — those are outcomes, reported in [Item].Found and [Status].
//
// Failures are wrapped in an [OpError] carrying the operation, the key and the
// server it was routed to. Branch on the cause, and read the OpError only for
// logging and metrics:
//
//	if errors.Is(err, memcache.ErrBreakerOpen) { /* server is shedding */ }
//	if errors.Is(err, context.DeadlineExceeded) { /* caller budget spent */ }
//
//	if opErr, ok := errors.AsType[*memcache.OpError](err); ok {
//		log.Printf("op=%s server=%s: %v", opErr.Op, opErr.Address, err)
//	}
//
// Keys are deliberately absent from error messages — they often carry user
// identifiers and would give log lines unbounded cardinality — so read
// [OpError.Key] when the key is wanted.
//
// A protocol error from the server is classified through the error types this
// package aliases from meta, so telling a server failure from a malformed
// request needs no import of the low-level package:
//
//	if _, ok := errors.AsType[*memcache.ServerError](err); ok {
//		// the server failed the operation (out of memory, internal error)
//	}
//
// # Building Blocks
//
// The client is assembled from smaller pieces that can be used on their own to
// build a custom client:
//
//   - The meta package serializes requests and parses responses for the
//     memcached meta protocol.
//   - [Connection] wraps a single net.Conn and implements [Executor].
//   - [Commands] and [BatchCommands] hold the command logic (Get, Set, Delete,
//     Increment, …) on top of any [Executor].
//
// Connection pooling is built into [Client] (backed by jackc/puddle) and is not
// a standalone building block.
package memcache
