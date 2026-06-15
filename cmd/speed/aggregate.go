package main

import "slices"

// OpResult is the aggregated outcome for a single operation across all runs.
type OpResult struct {
	Name         string  `json:"name"`
	ItemsPerOp   int     `json:"items_per_op"`
	OpsPerSec    float64 `json:"ops_per_sec"`
	ItemsPerSec  float64 `json:"items_per_sec"`
	AvgLatencyNs int64   `json:"avg_latency_ns"`
}

// BenchmarkReport is the machine-readable result of one speed run, suitable
// for storing as JSON and comparing across runs.
type BenchmarkReport struct {
	Client      string     `json:"client"`
	Pool        string     `json:"pool,omitempty"`
	Server      string     `json:"server"`
	Concurrency int        `json:"concurrency"`
	Count       int64      `json:"count"`
	Runs        int        `json:"runs"`
	Results     []OpResult `json:"results"`
}

// trimmedMean averages the samples after dropping the single highest and
// single lowest value. This damps the host noise (a stalled run, a noisy
// neighbour) that throughput tests pick up on shared CI runners.
//
// With fewer than three samples there is nothing to trim, so it returns the
// plain mean and degrades gracefully.
func trimmedMean(samples []float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	if n < 3 {
		return mean(samples)
	}
	s := slices.Clone(samples)
	slices.Sort(s)
	return mean(s[1 : n-1])
}

func mean(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		sum += v
	}
	return sum / float64(len(samples))
}
