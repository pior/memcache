package main

import (
	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/report"
)

// poolStatsJSON snapshots per-address pool stats for the run result, to validate
// key distribution and per-server churn across the fleet.
func poolStatsJSON(client *memcache.Client) []report.PoolStat {
	all := client.PoolMetrics()
	out := make([]report.PoolStat, 0, len(all))
	for _, ps := range all {
		out = append(out, report.PoolStat{
			Addr:           ps.Addr,
			CreatedConns:   ps.Metrics.CreatedConns,
			DestroyedConns: ps.Metrics.DestroyedConns,
			ActiveConns:    ps.Metrics.ActiveConns,
			IdleConns:      ps.Metrics.IdleConns,
			AcquireCount:   ps.Metrics.AcquireCount,
			AcquireWaits:   ps.Metrics.AcquireWaitCount,
			AcquireErrors:  ps.Metrics.AcquireErrors,
		})
	}
	return out
}
