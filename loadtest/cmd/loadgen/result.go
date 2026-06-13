package main

import (
	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/metrics"
)

// runResult is the final per-VM artifact the orchestrator collects and merges.
type runResult struct {
	RunID       string           `json:"run_id,omitempty"`
	VM          string           `json:"vm,omitempty"`
	Profile     string           `json:"profile"`
	ElapsedSecs float64          `json:"elapsed_secs"`
	Snapshot    metrics.Snapshot `json:"metrics"`
	PoolStats   []poolStat       `json:"pool_stats"`
}

// poolStat is a JSON-friendly per-address pool snapshot, for validating key
// distribution and per-server churn across the fleet.
type poolStat struct {
	Addr           string `json:"addr"`
	CreatedConns   uint64 `json:"created"`
	DestroyedConns uint64 `json:"destroyed"`
	ActiveConns    int32  `json:"active"`
	IdleConns      int32  `json:"idle"`
	AcquireCount   uint64 `json:"acquires"`
	AcquireWaits   uint64 `json:"acquire_waits"`
	AcquireErrors  uint64 `json:"acquire_errors"`
}

func poolStatsJSON(client *memcache.Client) []poolStat {
	all := client.AllPoolStats()
	out := make([]poolStat, 0, len(all))
	for _, ps := range all {
		out = append(out, poolStat{
			Addr:           ps.Addr,
			CreatedConns:   ps.PoolStats.CreatedConns,
			DestroyedConns: ps.PoolStats.DestroyedConns,
			ActiveConns:    ps.PoolStats.ActiveConns,
			IdleConns:      ps.PoolStats.IdleConns,
			AcquireCount:   ps.PoolStats.AcquireCount,
			AcquireWaits:   ps.PoolStats.AcquireWaitCount,
			AcquireErrors:  ps.PoolStats.AcquireErrors,
		})
	}
	return out
}
