// Package hoststat samples host CPU, memory, network, and pressure (PSI) from
// /proc so a run can be verified and tuned: are clients CPU-bound? is the NIC
// saturated? are the servers the bottleneck? Collection is Linux-only (the VMs);
// the pure parsers are unit-tested against captured /proc fixtures so they build
// and test on any platform. On non-Linux, Sample returns only a timestamp.
package hoststat

import "time"

// Sample is one point-in-time host observation. Rates are computed over the
// interval since the previous sample.
type Sample struct {
	Time     time.Time  `json:"t"`
	NumCPU   int        `json:"ncpu"`
	CPU      CPUStat    `json:"cpu"`
	Mem      MemStat    `json:"mem"`
	Net      []NetStat  `json:"net,omitempty"`
	TCP      TCPStat    `json:"tcp"`
	Pressure *Pressure  `json:"psi,omitempty"` // nil if /proc/pressure absent
	LoadAvg  [3]float64 `json:"loadavg"`
	// Warmup is true for the first sample, whose rates are not yet meaningful
	// (no previous reading to delta against).
	Warmup bool `json:"warmup,omitempty"`
}

// CPUStat reports CPU utilization over the interval.
type CPUStat struct {
	BusyFraction float64 `json:"busy"` // 0..1 aggregate utilization
}

// MemStat reports memory usage.
type MemStat struct {
	TotalKB      uint64  `json:"total_kb"`
	AvailableKB  uint64  `json:"avail_kb"`
	UsedFraction float64 `json:"used"`
}

// NetStat reports per-interface throughput and loss rates.
type NetStat struct {
	Iface         string  `json:"iface"`
	RxBytesPerSec float64 `json:"rx_bps"`
	TxBytesPerSec float64 `json:"tx_bps"`
	RxDropsPerSec float64 `json:"rx_drop_ps"`
	TxDropsPerSec float64 `json:"tx_drop_ps"`
}

// TCPStat reports TCP-level saturation symptoms.
type TCPStat struct {
	RetransSegsPerSec float64 `json:"retrans_ps"`
}

// Pressure holds PSI "some avg10" stall fractions (0..1), the headline
// saturation signal: the share of time at least one task was stalled on the
// resource over the last 10s.
type Pressure struct {
	CPUSome float64 `json:"cpu_some"`
	IOSome  float64 `json:"io_some"`
	MemSome float64 `json:"mem_some"`
}

// CPUSaturated reports whether CPU is the bottleneck: high utilization with
// meaningful stall pressure (PSI preferred when present).
func (s Sample) CPUSaturated() bool {
	if s.Pressure != nil {
		return s.Pressure.CPUSome > 0.20
	}
	return s.CPU.BusyFraction > 0.90
}
