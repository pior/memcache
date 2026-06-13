//go:build !linux

package hoststat

import (
	"runtime"
	"time"
)

// Sample on non-Linux returns only a timestamp and CPU count: /proc host
// metrics are unavailable. It still flows through computeSample so the warmup
// and delta logic is exercised in local dev; real collection happens on the
// Linux VMs.
func (s *Sampler) Sample() Sample {
	now := rawHost{time: time.Now(), numCPU: runtime.NumCPU()}
	out := computeSample(s.prev, now, s.prevSet)
	s.prev = now
	s.prevSet = true
	return out
}
