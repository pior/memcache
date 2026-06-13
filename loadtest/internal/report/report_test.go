package report

import (
	"strings"
	"testing"
	"time"

	"github.com/pior/memcache/loadtest/internal/hoststat"
	"github.com/pior/memcache/loadtest/internal/metrics"
	"github.com/pior/memcache/loadtest/internal/workload"
)

func mkResult(vm string, ops int, addr string, acquires uint64) RunResult {
	m := metrics.New()
	for range ops {
		m.Record(workload.OpGet, time.Millisecond, metrics.OutcomeHit)
	}
	return RunResult{
		VM:          vm,
		ElapsedSecs: 10,
		Snapshot:    m.Snapshot(),
		PoolStats:   []PoolStat{{Addr: addr, AcquireCount: acquires}},
	}
}

func TestAggregate(t *testing.T) {
	f := Aggregate([]RunResult{
		mkResult("cli-0", 100, "10.0.0.1:11211", 60),
		mkResult("cli-1", 200, "10.0.0.1:11211", 40),
	})
	if f.Metrics.Ops != 300 {
		t.Errorf("fleet ops = %d, want 300", f.Metrics.Ops)
	}
	if f.AcquiresByAddr["10.0.0.1:11211"] != 100 {
		t.Errorf("acquires = %d, want 100", f.AcquiresByAddr["10.0.0.1:11211"])
	}
	if f.Throughput != 30 { // 300 ops / 10s
		t.Errorf("throughput = %g, want 30", f.Throughput)
	}
}

func TestSummarizeHostCPUBound(t *testing.T) {
	samples := []hoststat.Sample{
		{Warmup: true},
		{CPU: hoststat.CPUStat{BusyFraction: 0.95}, Pressure: &hoststat.Pressure{CPUSome: 0.4}},
	}
	h := SummarizeHost("client", "c3-highcpu-8", samples)
	if h.CPUStall != 0.4 {
		t.Errorf("cpu stall = %v, want 0.4", h.CPUStall)
	}
	if !strings.Contains(h.Note, "CPU-bound") {
		t.Errorf("note = %q, want CPU-bound", h.Note)
	}
}

func TestSummarizeHostNetBound(t *testing.T) {
	// c3-highcpu-8 -> 16 Gbps cap = 2e9 B/s; 1.9e9 B/s tx -> ~95% saturation.
	samples := []hoststat.Sample{
		{CPU: hoststat.CPUStat{BusyFraction: 0.4}, Net: []hoststat.NetStat{{TxBytesPerSec: 1.9e9}}},
	}
	h := SummarizeHost("server", "c3-highcpu-8", samples)
	if h.NetSatPercent < 80 {
		t.Errorf("net saturation = %.0f%%, want >80", h.NetSatPercent)
	}
	if !strings.Contains(h.Note, "network-bound") {
		t.Errorf("note = %q, want network-bound", h.Note)
	}
}

func TestSummarizeHostHeadroom(t *testing.T) {
	samples := []hoststat.Sample{
		{CPU: hoststat.CPUStat{BusyFraction: 0.3}, Pressure: &hoststat.Pressure{CPUSome: 0.01},
			Net: []hoststat.NetStat{{TxBytesPerSec: 1e6}}},
	}
	h := SummarizeHost("client", "c3-highcpu-8", samples)
	if !strings.Contains(h.Note, "headroom") {
		t.Errorf("note = %q, want headroom", h.Note)
	}
}
