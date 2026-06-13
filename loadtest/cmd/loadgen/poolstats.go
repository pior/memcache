package main

import (
	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/report"
)

// poolStatsJSON snapshots per-address pool stats for the run result, to validate
// key distribution and per-server churn across the fleet.
func poolStatsJSON(client *memcache.Client) []report.PoolStat {
	all := client.AllPoolStats()
	out := make([]report.PoolStat, 0, len(all))
	for _, ps := range all {
		out = append(out, report.PoolStat{
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
