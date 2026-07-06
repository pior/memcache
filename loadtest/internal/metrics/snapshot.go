package metrics

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Snapshot is a serializable, mergeable view of Metrics at a point in time.
type Snapshot struct {
	Ops      int64                 `json:"ops"`
	Hits     int64                 `json:"hits"`
	Misses   int64                 `json:"misses"`
	Errors   int64                 `json:"errors"`
	Timeouts int64                 `json:"timeouts"`
	Desyncs  int64                 `json:"desyncs"`
	PerOp    map[string]OpSnapshot `json:"per_op"`
	Latency  HistogramData         `json:"latency"`

	// ErrorsByServer attributes errors to server addresses (via OpError), the
	// per-shard failure signal for chaos runs. Omitted while error-free.
	ErrorsByServer map[string]int64 `json:"errors_by_server,omitempty"`
}

// OpSnapshot is the per-operation count and latency.
type OpSnapshot struct {
	Count   int64         `json:"count"`
	Latency HistogramData `json:"latency"`
}

// Merge folds o into s (combining per-VM snapshots into a fleet total).
func (s *Snapshot) Merge(o Snapshot) {
	s.Ops += o.Ops
	s.Hits += o.Hits
	s.Misses += o.Misses
	s.Errors += o.Errors
	s.Timeouts += o.Timeouts
	s.Desyncs += o.Desyncs
	s.Latency.Merge(o.Latency)
	if s.PerOp == nil {
		s.PerOp = make(map[string]OpSnapshot)
	}
	for name, op := range o.PerOp {
		cur := s.PerOp[name]
		cur.Count += op.Count
		cur.Latency.Merge(op.Latency)
		s.PerOp[name] = cur
	}
	if len(o.ErrorsByServer) > 0 && s.ErrorsByServer == nil {
		s.ErrorsByServer = make(map[string]int64)
	}
	for addr, n := range o.ErrorsByServer {
		s.ErrorsByServer[addr] += n
	}
}

// Throughput returns ops per second over the elapsed duration.
func (s Snapshot) Throughput(elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(s.Ops) / elapsed.Seconds()
}

// ErrorRate returns the fraction of ops that errored (0..1).
func (s Snapshot) ErrorRate() float64 {
	if s.Ops == 0 {
		return 0
	}
	return float64(s.Errors) / float64(s.Ops)
}

// Text renders a compact human-readable summary.
func (s Snapshot) Text(elapsed time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ops=%d (%.0f/s) hits=%d misses=%d errors=%d (%.2f%%) timeouts=%d desyncs=%d\n",
		s.Ops, s.Throughput(elapsed), s.Hits, s.Misses, s.Errors, s.ErrorRate()*100, s.Timeouts, s.Desyncs)
	fmt.Fprintf(&b, "latency: p50=%s p95=%s p99=%s max=%s mean=%s\n",
		s.Latency.Percentile(50), s.Latency.Percentile(95),
		s.Latency.Percentile(99), s.Latency.Max(), s.Latency.Mean())
	names := make([]string, 0, len(s.PerOp))
	for name := range s.PerOp {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		op := s.PerOp[name]
		fmt.Fprintf(&b, "  %-9s count=%-9d p99=%s\n", name, op.Count, op.Latency.Percentile(99))
	}
	if len(s.ErrorsByServer) > 0 {
		addrs := make([]string, 0, len(s.ErrorsByServer))
		for a := range s.ErrorsByServer {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		b.WriteString("errors by server:\n")
		for _, a := range addrs {
			fmt.Fprintf(&b, "  %-22s errors=%d\n", a, s.ErrorsByServer[a])
		}
	}
	return b.String()
}
