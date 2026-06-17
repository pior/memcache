package main

import (
	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/report"
)

// poolMetricsJSON snapshots per-address pool metrics for the run result, to
// validate key distribution and per-server churn across the fleet.
func poolMetricsJSON(client *memcache.Client) []report.PoolMetric {
	all := client.PoolMetrics()
	out := make([]report.PoolMetric, 0, len(all))
	for _, pm := range all {
		out = append(out, report.PoolMetric{
			Addr:           pm.Addr,
			CreatedConns:   pm.Conns.CreatedConns,
			DestroyedConns: pm.Conns.DestroyedConns,
			ActiveConns:    pm.Conns.ActiveConns,
			IdleConns:      pm.Conns.IdleConns,
			AcquireCount:   pm.Conns.AcquireCount,
			AcquireWaits:   pm.Conns.AcquireWaitCount,
			AcquireErrors:  pm.Conns.AcquireErrors,
		})
	}
	return out
}
