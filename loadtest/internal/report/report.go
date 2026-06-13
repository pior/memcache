// Package report defines the per-VM result artifact and aggregates a fleet of
// them — plus host-metric samples — into a run summary with throughput,
// per-address pool totals, and CPU/network bottleneck attribution for tuning.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pior/memcache/loadtest/internal/hoststat"
	"github.com/pior/memcache/loadtest/internal/metrics"
	"github.com/pior/memcache/loadtest/internal/profile"
)

// RunResult is the final per-VM artifact loadgen writes and the orchestrator
// collects. It lives here so both loadgen and the report tooling share it.
type RunResult struct {
	RunID       string           `json:"run_id,omitempty"`
	VM          string           `json:"vm,omitempty"`
	Profile     string           `json:"profile"`
	ElapsedSecs float64          `json:"elapsed_secs"`
	Snapshot    metrics.Snapshot `json:"metrics"`
	PoolStats   []PoolStat       `json:"pool_stats"`
}

// PoolStat is a JSON-friendly per-address pool snapshot.
type PoolStat struct {
	Addr           string `json:"addr"`
	CreatedConns   uint64 `json:"created"`
	DestroyedConns uint64 `json:"destroyed"`
	ActiveConns    int32  `json:"active"`
	IdleConns      int32  `json:"idle"`
	AcquireCount   uint64 `json:"acquires"`
	AcquireWaits   uint64 `json:"acquire_waits"`
	AcquireErrors  uint64 `json:"acquire_errors"`
}

// Fleet is the aggregate of all client VMs in a run.
type Fleet struct {
	Metrics        metrics.Snapshot
	ElapsedSecs    float64
	Throughput     float64           // fleet ops/sec
	AcquiresByAddr map[string]uint64 // key distribution across the server pool
}

// Aggregate merges per-VM results into a fleet summary. elapsed is the wall
// time used to compute throughput (the longest per-VM elapsed).
func Aggregate(results []RunResult) Fleet {
	f := Fleet{AcquiresByAddr: map[string]uint64{}}
	for _, r := range results {
		f.Metrics.Merge(r.Snapshot)
		if r.ElapsedSecs > f.ElapsedSecs {
			f.ElapsedSecs = r.ElapsedSecs
		}
		for _, ps := range r.PoolStats {
			f.AcquiresByAddr[ps.Addr] += ps.AcquireCount
		}
	}
	if f.ElapsedSecs > 0 {
		f.Throughput = float64(f.Metrics.Ops) / f.ElapsedSecs
	}
	return f
}

// HostFinding is a bottleneck attribution for one VM role.
type HostFinding struct {
	Role          string
	CPUBusy       float64 // peak aggregate CPU utilization (0..1)
	CPUStall      float64 // peak PSI cpu "some" (0..1), -1 if unavailable
	NetTxPeakBps  float64 // peak tx bytes/sec across NICs
	NetSatPercent float64 // peak tx as % of the egress cap, -1 if unknown
	Note          string
}

// SummarizeHost reduces a role's host samples to a bottleneck finding. samples
// should be the JSONL host samples for all VMs of one role; machineType is the
// role's machine type (for the egress cap).
func SummarizeHost(role, machineType string, samples []hoststat.Sample) HostFinding {
	f := HostFinding{Role: role, CPUStall: -1, NetSatPercent: -1}
	capGbps := profile.EgressCapGbps(machineType)
	for _, s := range samples {
		if s.Warmup {
			continue
		}
		if s.CPU.BusyFraction > f.CPUBusy {
			f.CPUBusy = s.CPU.BusyFraction
		}
		if s.Pressure != nil && s.Pressure.CPUSome > f.CPUStall {
			f.CPUStall = s.Pressure.CPUSome
		}
		for _, n := range s.Net {
			if n.TxBytesPerSec > f.NetTxPeakBps {
				f.NetTxPeakBps = n.TxBytesPerSec
			}
		}
	}
	if capGbps > 0 {
		capBps := capGbps * 1e9 / 8
		f.NetSatPercent = 100 * f.NetTxPeakBps / capBps
	}
	f.Note = f.attribute()
	return f
}

func (f HostFinding) attribute() string {
	switch {
	case f.CPUStall >= 0 && f.CPUStall > 0.20:
		return "CPU-bound (PSI cpu stall high) — add VMs or use a larger machine type"
	case f.CPUStall < 0 && f.CPUBusy > 0.90:
		return "CPU-bound (utilization >90%, PSI unavailable)"
	case f.NetSatPercent > 80:
		return "network-bound (egress near the cap)"
	default:
		return "headroom available (neither CPU nor network saturated)"
	}
}

// Text renders a human-readable run summary.
func Text(f Fleet, host []HostFinding) string {
	var b strings.Builder
	b.WriteString("=== fleet ===\n")
	b.WriteString(f.Metrics.Text(time.Duration(f.ElapsedSecs * float64(time.Second))))
	fmt.Fprintf(&b, "fleet throughput: %.0f ops/s\n", f.Throughput)

	b.WriteString("\n=== key distribution across server pool ===\n")
	addrs := make([]string, 0, len(f.AcquiresByAddr))
	for a := range f.AcquiresByAddr {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		fmt.Fprintf(&b, "  %-22s acquires=%d\n", a, f.AcquiresByAddr[a])
	}

	b.WriteString("\n=== host / bottleneck ===\n")
	for _, h := range host {
		fmt.Fprintf(&b, "  %-7s cpu_busy=%.0f%% cpu_stall=%s net_tx=%.1fMB/s (%s of cap) — %s\n",
			h.Role, h.CPUBusy*100, pct(h.CPUStall), h.NetTxPeakBps/1e6, satPct(h.NetSatPercent), h.Note)
	}
	return b.String()
}

func pct(v float64) string {
	if v < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func satPct(v float64) string {
	if v < 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%%", v)
}
