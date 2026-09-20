package memcache

import (
	"context"

	"github.com/pior/memcache/meta"
)

// OpInfo describes an operation as it begins.
type OpInfo struct {
	// Op is the operation, one of the Op* constants ("get", "set", "touch",
	// ...) and the same vocabulary as OpError.Op. It names what the caller
	// asked for, not the wire command carrying it, so a Touch is not reported
	// as a get and an Add is not reported as a set.
	Op       string
	Address  string // resolved server address ("" if not yet known)
	Key      string // single-op key ("" for batch/stats)
	Requests int    // number of pipelined requests for a batch; 0 otherwise
}

// OpResult describes an operation as it completes.
type OpResult struct {
	// Status is the operation's outcome, the same value the caller receives:
	// StatusApplied, StatusNotFound, StatusExists or StatusCASMismatch. It is
	// the zero Status when there is no single outcome to report: the operation
	// failed, or it is a batch, a stats or a flush_all.
	Status Status

	// Code is the raw status the server replied ("HD", "EN", "NS", ...), empty
	// when no response was decoded. Status is what that code meant for this
	// operation — the same NS is StatusExists after an add and StatusNotFound
	// after a replace — so prefer Status and read Code only to report the wire
	// verbatim.
	Code string

	Err error
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

// observedError picks the error to report for a single operation: the
// execution error if any, otherwise the response's protocol error.
func observedError(respErr, err error) error {
	if err != nil {
		return err
	}
	return respErr
}

func observedBatchError(responses []*meta.Response, err error) error {
	if err != nil {
		return err
	}
	for _, resp := range responses {
		if resp != nil && resp.Error != nil {
			return resp.Error
		}
	}
	return nil
}

// opName names the client operation a request implements. The command code is
// not enough: Get and Touch are both mg, and every store mode is ms, so the
// modifiers have to be read back to tell them apart. A command this package
// does not model keeps its wire code.
func opName(req *meta.Request) string {
	switch req.Command {
	case meta.CmdGet:
		// A Get asks for the value; a Touch is the same mg without it.
		if req.HasFlag(meta.FlagReturnValue) {
			return OpGet
		}
		return OpTouch
	case meta.CmdSet:
		switch mode, _ := req.GetFlagToken(meta.FlagMode); string(mode) {
		case meta.ModeAdd:
			return OpAdd
		case meta.ModeReplace:
			return OpReplace
		case meta.ModeAppend:
			return OpAppend
		case meta.ModePrepend:
			return OpPrepend
		default:
			return OpSet
		}
	case meta.CmdArithmetic:
		switch mode, _ := req.GetFlagToken(meta.FlagMode); string(mode) {
		case meta.ModeDecrement, meta.ModeDecrementAlt:
			return OpDecrement
		default:
			return OpIncrement
		}
	case meta.CmdDelete:
		return OpDelete
	case meta.CmdDebug:
		return OpDebug
	case meta.CmdStats:
		return OpStats
	case meta.CmdFlushAll:
		return OpFlushAll
	default:
		return string(req.Command)
	}
}

// statusOf interprets a completed single operation's wire status the way the
// command layer does, so an observer reports the outcome the caller sees. It
// mirrors readItem, storeRequest.result, deleteStatus and Commands.arithmetic:
// NS is the case that needs the operation, not just the code (see
// storeRequest.nsMeans).
//
// The zero Status means there is no outcome to report: the operation failed,
// no response was decoded, or the reply is one the operation does not model.
func statusOf(op string, code meta.StatusType, err error) Status {
	if err != nil || code == "" {
		return 0
	}
	switch code {
	case meta.StatusHD, meta.StatusVA:
		return StatusApplied
	case meta.StatusEN, meta.StatusNF:
		return StatusNotFound
	case meta.StatusEX:
		return StatusCASMismatch
	case meta.StatusNS:
		switch op {
		case OpAdd:
			return StatusExists
		case OpReplace, OpAppend, OpPrepend:
			return StatusNotFound
		}
	}
	return 0
}
