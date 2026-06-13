package hoststat

import (
	"sort"
	"time"
)

// rawHost is one raw reading: cumulative counters (cpu, net, tcp) plus
// instantaneous gauges (mem, loadavg, psi).
type rawHost struct {
	time       time.Time
	numCPU     int
	cpu        cpuTimes
	net        map[string]netCounters
	tcpRetrans uint64
	loadavg    [3]float64
	memTotalKB uint64
	memAvailKB uint64
	psi        *Pressure
}

// Sampler turns successive raw readings into rate-bearing Samples. Not safe for
// concurrent use; call Sample from a single goroutine.
type Sampler struct {
	prev    rawHost
	prevSet bool
}

// NewSampler returns a Sampler. The first Sample is a warmup with zero rates.
func NewSampler() *Sampler { return &Sampler{} }

// computeSample derives a Sample from the previous and current raw readings.
func computeSample(prev rawHost, now rawHost, prevSet bool) Sample {
	s := Sample{
		Time:     now.time,
		NumCPU:   now.numCPU,
		LoadAvg:  now.loadavg,
		Pressure: now.psi,
	}
	if now.memTotalKB > 0 {
		used := now.memTotalKB - now.memAvailKB
		s.Mem = MemStat{
			TotalKB:      now.memTotalKB,
			AvailableKB:  now.memAvailKB,
			UsedFraction: float64(used) / float64(now.memTotalKB),
		}
	}

	dt := now.time.Sub(prev.time).Seconds()
	if !prevSet || dt <= 0 {
		s.Warmup = true
		return s
	}

	if dTotal := sub(now.cpu.total, prev.cpu.total); dTotal > 0 {
		dIdle := sub(now.cpu.idle, prev.cpu.idle)
		s.CPU.BusyFraction = float64(dTotal-dIdle) / float64(dTotal)
	}

	s.TCP.RetransSegsPerSec = float64(sub(now.tcpRetrans, prev.tcpRetrans)) / dt

	ifaces := make([]string, 0, len(now.net))
	for iface := range now.net {
		ifaces = append(ifaces, iface)
	}
	sort.Strings(ifaces)
	for _, iface := range ifaces {
		cur := now.net[iface]
		old, ok := prev.net[iface]
		if !ok {
			continue
		}
		s.Net = append(s.Net, NetStat{
			Iface:         iface,
			RxBytesPerSec: float64(sub(cur.rxBytes, old.rxBytes)) / dt,
			TxBytesPerSec: float64(sub(cur.txBytes, old.txBytes)) / dt,
			RxDropsPerSec: float64(sub(cur.rxDrop, old.rxDrop)) / dt,
			TxDropsPerSec: float64(sub(cur.txDrop, old.txDrop)) / dt,
		})
	}
	return s
}

// sub is a saturating subtraction that treats counter resets (b > a) as 0.
func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
