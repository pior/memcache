//go:build linux

package hoststat

import (
	"os"
	"runtime"
	"time"
)

// Sample reads /proc and returns a rate-bearing observation.
func (s *Sampler) Sample() Sample {
	now := readRaw()
	out := computeSample(s.prev, now, s.prevSet)
	s.prev = now
	s.prevSet = true
	return out
}

func readRaw() rawHost {
	r := rawHost{time: time.Now(), numCPU: runtime.NumCPU()}

	if data, err := os.ReadFile("/proc/stat"); err == nil {
		if ct, ok := parseProcStat(string(data)); ok {
			r.cpu = ct
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if la, ok := parseLoadavg(string(data)); ok {
			r.loadavg = la
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		if total, avail, ok := parseMeminfo(string(data)); ok {
			r.memTotalKB, r.memAvailKB = total, avail
		}
	}
	if data, err := os.ReadFile("/proc/net/dev"); err == nil {
		r.net = parseNetDev(string(data))
	}
	if data, err := os.ReadFile("/proc/net/snmp"); err == nil {
		if v, ok := parseTCPRetrans(string(data)); ok {
			r.tcpRetrans = v
		}
	}
	r.psi = readPressure()
	return r
}

// readPressure reads the PSI files; returns nil if /proc/pressure is absent
// (kernel without CONFIG_PSI).
func readPressure() *Pressure {
	cpu, okCPU := readPSI("/proc/pressure/cpu")
	io, _ := readPSI("/proc/pressure/io")
	mem, _ := readPSI("/proc/pressure/memory")
	if !okCPU {
		return nil
	}
	return &Pressure{CPUSome: cpu, IOSome: io, MemSome: mem}
}

func readPSI(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return parsePSISome(string(data))
}
