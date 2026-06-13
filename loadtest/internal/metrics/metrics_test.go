package metrics

import (
	"testing"
	"time"

	"github.com/pior/memcache/loadtest/internal/workload"
)

func TestRecordAndSnapshot(t *testing.T) {
	m := New()
	m.Record(workload.OpGet, 100*time.Microsecond, OutcomeHit)
	m.Record(workload.OpGet, 200*time.Microsecond, OutcomeMiss)
	m.Record(workload.OpSet, 5*time.Millisecond, OutcomeError)
	m.Record(workload.OpGet, time.Second, OutcomeTimeout)

	s := m.Snapshot()
	if s.Ops != 4 {
		t.Errorf("ops = %d, want 4", s.Ops)
	}
	if s.Hits != 1 || s.Misses != 1 {
		t.Errorf("hits/misses = %d/%d, want 1/1", s.Hits, s.Misses)
	}
	// timeout counts as both timeout and error
	if s.Errors != 2 || s.Timeouts != 1 {
		t.Errorf("errors/timeouts = %d/%d, want 2/1", s.Errors, s.Timeouts)
	}
	if s.PerOp["get"].Count != 3 {
		t.Errorf("get count = %d, want 3", s.PerOp["get"].Count)
	}
}

func TestPercentile(t *testing.T) {
	var h Histogram
	for i := 1; i <= 1000; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	d := h.Data()
	// p50 of 1..1000ms should land near 500ms within bucket resolution.
	p50 := d.Percentile(50)
	if p50 < 450*time.Millisecond || p50 > 550*time.Millisecond {
		t.Errorf("p50 = %s, want ~500ms", p50)
	}
	p99 := d.Percentile(99)
	if p99 < 950*time.Millisecond || p99 > 1000*time.Millisecond {
		t.Errorf("p99 = %s, want ~990ms", p99)
	}
}

func TestMerge(t *testing.T) {
	a := New()
	a.Record(workload.OpGet, time.Millisecond, OutcomeHit)
	b := New()
	b.Record(workload.OpGet, time.Millisecond, OutcomeHit)
	b.Record(workload.OpSet, 2*time.Millisecond, OutcomeError)

	sa, sb := a.Snapshot(), b.Snapshot()
	sa.Merge(sb)
	if sa.Ops != 3 {
		t.Errorf("merged ops = %d, want 3", sa.Ops)
	}
	if sa.Latency.Total != 3 {
		t.Errorf("merged latency total = %d, want 3", sa.Latency.Total)
	}
	if got := sa.PerOp["get"].Count; got != 2 {
		t.Errorf("merged get count = %d, want 2", got)
	}
}
