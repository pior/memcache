// Package metrics collects always-on load-test telemetry: latency histograms
// per operation and atomic outcome counters, snapshotted as JSON for periodic
// monitoring and merged across VMs for the final report.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache/loadtest/internal/workload"
)

// Outcome classifies the result of an operation.
type Outcome uint8

const (
	OutcomeOK      Outcome = iota // non-read success (set/delete/incr/...)
	OutcomeHit                    // read found a valid value
	OutcomeMiss                   // read found nothing
	OutcomeError                  // transport/protocol error
	OutcomeTimeout                // deadline exceeded
	OutcomeDesync                 // key-embedding invariant violated — must stay zero
)

// Metrics accumulates counters and per-op latency. Safe for concurrent use.
type Metrics struct {
	ops      atomic.Int64
	hits     atomic.Int64
	misses   atomic.Int64
	errors   atomic.Int64
	timeouts atomic.Int64
	desyncs  atomic.Int64

	perOp  [workload.NumOps]atomic.Int64
	hist   [workload.NumOps]Histogram
	allLat Histogram // combined latency across all ops

	// serverErrors counts errors per server address (from OpError.Server), so
	// a chaos run can verify that failures attribute to the faulted server.
	serverErrors sync.Map // string -> *atomic.Int64
}

// RecordServerError attributes one failed operation to a server address.
// Empty addresses (errors that carry no server context) are ignored.
func (m *Metrics) RecordServerError(addr string) {
	if addr == "" {
		return
	}
	counter, ok := m.serverErrors.Load(addr)
	if !ok {
		counter, _ = m.serverErrors.LoadOrStore(addr, new(atomic.Int64))
	}
	counter.(*atomic.Int64).Add(1)
}

// New returns an empty Metrics.
func New() *Metrics { return &Metrics{} }

// Record registers one completed operation: its kind, latency, and outcome.
func (m *Metrics) Record(op workload.Op, d time.Duration, outcome Outcome) {
	m.ops.Add(1)
	m.perOp[op].Add(1)
	m.hist[op].Record(d)
	m.allLat.Record(d)

	switch outcome {
	case OutcomeOK:
		// non-read success; counted in ops only
	case OutcomeHit:
		m.hits.Add(1)
	case OutcomeMiss:
		m.misses.Add(1)
	case OutcomeError:
		m.errors.Add(1)
	case OutcomeTimeout:
		m.timeouts.Add(1)
		m.errors.Add(1)
	case OutcomeDesync:
		m.desyncs.Add(1)
	}
}

// Desyncs returns the desync count (the invariant that must stay zero).
func (m *Metrics) Desyncs() int64 { return m.desyncs.Load() }

// Ops returns the total operation count.
func (m *Metrics) Ops() int64 { return m.ops.Load() }

// Snapshot captures the current state.
func (m *Metrics) Snapshot() Snapshot {
	s := Snapshot{
		Ops:      m.ops.Load(),
		Hits:     m.hits.Load(),
		Misses:   m.misses.Load(),
		Errors:   m.errors.Load(),
		Timeouts: m.timeouts.Load(),
		Desyncs:  m.desyncs.Load(),
		PerOp:    make(map[string]OpSnapshot, workload.NumOps),
		Latency:  m.allLat.Data(),
	}
	for op := range workload.NumOps {
		count := m.perOp[op].Load()
		if count == 0 {
			continue
		}
		s.PerOp[workload.Op(op).String()] = OpSnapshot{
			Count:   count,
			Latency: m.hist[op].Data(),
		}
	}
	m.serverErrors.Range(func(addr, counter any) bool {
		if s.ErrorsByServer == nil {
			s.ErrorsByServer = make(map[string]int64)
		}
		s.ErrorsByServer[addr.(string)] = counter.(*atomic.Int64).Load()
		return true
	})
	return s
}
