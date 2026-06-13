//go:build !linux

package hoststat

import (
	"runtime"
	"time"
)

// Sample on non-Linux returns only a timestamp and CPU count: /proc host
// metrics are unavailable. This keeps the binary buildable for local dev; real
// collection happens on the Linux VMs.
func (s *Sampler) Sample() Sample {
	return Sample{Time: time.Now(), NumCPU: runtime.NumCPU(), Warmup: true}
}
