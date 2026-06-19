package memcache

import (
	"context"

	"github.com/pior/memcache/meta"
)

// Result is the outcome of an operation, for observation purposes. For gets and
// deletes it reports key presence (hit/miss); for sets and arithmetic it reports
// whether the value was stored.
type Result int

const (
	ResultUnknown   Result = iota // no specific outcome (errors, stats, batch)
	ResultHit                     // the key was present
	ResultMiss                    // the key was absent
	ResultStored                  // the value was stored
	ResultNotStored               // the value was not stored (condition unmet)
)

func (r Result) String() string {
	switch r {
	case ResultHit:
		return "hit"
	case ResultMiss:
		return "miss"
	case ResultStored:
		return "stored"
	case ResultNotStored:
		return "not_stored"
	default:
		return "unknown"
	}
}

// OpInfo describes an operation as it begins.
type OpInfo struct {
	// Op is the technical operation identifier, the same vocabulary as
	// OpError.Op: a meta protocol command code ("mg", "ms", ...) for single ops,
	// or one of the Op* constants (OpBatch, OpStats). Mapping these to
	// human-readable names ("get", "set", ...) is a presentation concern left to
	// the Observer implementation.
	Op       string
	Server   string // resolved server address ("" if not yet known)
	Key      string // single-op key ("" for batch/stats)
	Requests int    // number of pipelined requests for a batch; 0 otherwise
}

// OpResult describes an operation as it completes.
type OpResult struct {
	Result Result
	Err    error
}

// Observer is notified around each client operation, enabling tracing and
// metrics without coupling the core to any telemetry backend. Implementations
// must be safe for concurrent use.
//
// StartOp is called when an operation begins. It returns a context to use for
// the operation — carrying, for example, a tracing span so downstream work
// nests under it — and an ActiveOp whose End is called exactly once when the
// operation finishes. This mirrors OpenTelemetry's own tracer.Start → span.End
// shape.
type Observer interface {
	StartOp(ctx context.Context, info OpInfo) (context.Context, ActiveOp)
}

// ActiveOp is an in-flight operation returned by Observer.StartOp. End is called
// exactly once with the operation's outcome, closing the span and/or recording
// metrics. Implementations must be safe for concurrent use.
type ActiveOp interface {
	End(OpResult)
}

// noopObserver is installed by NewClient when no Observer is configured, so the
// client can call StartOp unconditionally without a nil check.
type noopObserver struct{}

func (noopObserver) StartOp(ctx context.Context, _ OpInfo) (context.Context, ActiveOp) {
	return ctx, noopActiveOp{}
}

type noopActiveOp struct{}

func (noopActiveOp) End(OpResult) {}

// resultOf derives the observed Result from a completed single operation.
func resultOf(cmd meta.CmdType, resp *meta.Response, err error) Result {
	if err != nil || resp == nil {
		return ResultUnknown
	}
	switch cmd {
	case meta.CmdGet, meta.CmdDelete:
		switch resp.Status {
		case meta.StatusVA, meta.StatusHD:
			return ResultHit
		case meta.StatusEN, meta.StatusNF:
			return ResultMiss
		}
	case meta.CmdSet, meta.CmdArithmetic:
		switch resp.Status {
		case meta.StatusHD, meta.StatusVA:
			return ResultStored
		case meta.StatusNS, meta.StatusEX, meta.StatusNF:
			return ResultNotStored
		}
	}
	return ResultUnknown
}
