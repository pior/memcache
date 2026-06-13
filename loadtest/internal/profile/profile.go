// Package profile defines load-test run configuration: named resource profiles,
// load intensity, the memcache client knobs, and the GCE egress-cap table used
// to compute network saturation.
package profile

import (
	"fmt"
	"time"

	memcache "github.com/pior/memcache"
)

// Intensity selects how the generator drives load.
type Intensity string

const (
	// Saturation drives closed-loop at full concurrency to find/hold the
	// throughput ceiling — the stress mode that compresses operation-count wear.
	Saturation Intensity = "saturation"
	// FixedRate drives open-loop at TargetRate for clean latency measurement.
	FixedRate Intensity = "fixed-rate"
)

// Profile bundles everything a loadgen process needs for one run.
type Profile struct {
	Name       string
	Workers    int // concurrent workers per VM
	Intensity  Intensity
	TargetRate int  // ops/sec/VM for FixedRate; 0 = unlimited
	Keyspace   int  // number of distinct keys
	OpLog      bool // write the full per-op compressed log
	GOMAXPROCS int  // 0 = all cores (set by the efficiency profile / cgroup)

	// memcache client knobs (the wall-clock time constants are the lever for
	// exercising lifecycle churn within a bounded run).
	MaxSize         int32
	Timeout         time.Duration
	ConnectTimeout  time.Duration
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	HealthCheck     time.Duration
}

// ClientConfig builds the memcache client configuration from the profile.
func (p Profile) ClientConfig() memcache.Config {
	return memcache.Config{
		MaxSize:             p.MaxSize,
		Timeout:             p.Timeout,
		ConnectTimeout:      p.ConnectTimeout,
		MaxConnLifetime:     p.MaxConnLifetime,
		MaxConnIdleTime:     p.MaxConnIdleTime,
		HealthCheckInterval: p.HealthCheck,
	}
}

// topPerf: CPU unconstrained, high concurrency, production-like time constants.
var topPerf = Profile{
	Name:           "top-perf",
	Workers:        64,
	Intensity:      Saturation,
	Keyspace:       100_000,
	MaxSize:        16,
	Timeout:        time.Second,
	ConnectTimeout: time.Second,
}

// efficiency: CPU constrained (1 vCPU via GOMAXPROCS + cgroup), modest pool.
var efficiency = Profile{
	Name:           "efficiency",
	Workers:        16,
	Intensity:      Saturation,
	Keyspace:       100_000,
	GOMAXPROCS:     1,
	MaxSize:        8,
	Timeout:        time.Second,
	ConnectTimeout: time.Second,
}

var presets = map[string]Profile{
	topPerf.Name:    topPerf,
	efficiency.Name: efficiency,
}

// Lookup returns a named preset.
func Lookup(name string) (Profile, error) {
	p, ok := presets[name]
	if !ok {
		return Profile{}, fmt.Errorf("unknown profile %q (have top-perf, efficiency)", name)
	}
	return p, nil
}

// WithStressTimeConstants shortens the wall-clock lifecycle knobs so connection
// rotation, idle eviction, and health checking churn within a bounded run
// regardless of throughput. This is the time-driven lever for "longer" runs.
func (p Profile) WithStressTimeConstants() Profile {
	p.MaxConnLifetime = 100 * time.Millisecond
	p.MaxConnIdleTime = 50 * time.Millisecond
	p.HealthCheck = 20 * time.Millisecond
	return p
}
