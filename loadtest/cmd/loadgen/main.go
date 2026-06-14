// Command loadgen drives a memcache load/stress workload from one VM: it builds
// a real client against the server pool, runs concurrent workers checking the
// key-embedding invariant on every read, and emits periodic + final JSON
// metrics. It is the VM-side workload binary; it has no cloud dependencies.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/generator"
	"github.com/pior/memcache/loadtest/internal/metrics"
	"github.com/pior/memcache/loadtest/internal/oplog"
	"github.com/pior/memcache/loadtest/internal/profile"
	"github.com/pior/memcache/loadtest/internal/report"
)

func main() {
	var (
		serversFlag = flag.String("servers", "", "comma-separated server addresses (or set MEMCACHE_SERVERS)")
		profileName = flag.String("profile", "top-perf", "resource profile: top-perf | efficiency")
		duration    = flag.Duration("duration", time.Hour, "run duration")
		workers     = flag.Int("workers", 0, "override worker count (0 = profile default)")
		conns       = flag.Int("conns", 0, "override max connections per server (0 = profile default)")
		opTimeout   = flag.Duration("timeout", 0, "override per-op + connect timeout (0 = profile default)")
		keyspace    = flag.Int("keyspace", 0, "override key space size (0 = profile default)")
		rate        = flag.Int("rate", 0, "fixed-rate target ops/sec (0 = saturation)")
		stress      = flag.Bool("stress", false, "shorten connection time-constants for lifecycle churn")
		reportEvery = flag.Duration("report-interval", 10*time.Second, "periodic metrics interval")
		out         = flag.String("out", "", "final metrics JSON file (default stdout)")
		statusPath  = flag.String("status", "", "rewrite a human-readable status report (run time, totals, latency histogram) here every report-interval")
		snapPath    = flag.String("snapshot", "", "rewrite the full metrics JSON (RunResult) here every report-interval, for durability and offline analysis of a long run")
		oplogPath   = flag.String("oplog", "", "write the full per-op compressed log to this file (opt-in)")
		flightRing  = flag.Int("flight-ring", 128, "per-worker flight-recorder size (0 disables)")
		vm          = flag.String("vm", "", "vm name for the report")
		runID       = flag.String("run-id", "", "run id for the report")
	)
	flag.Parse()

	prof, err := profile.Lookup(*profileName)
	if err != nil {
		fatal(err)
	}
	if *workers > 0 {
		prof.Workers = *workers
	}
	if *conns > 0 {
		prof.MaxSize = int32(*conns)
	}
	if *opTimeout > 0 {
		prof.Timeout = *opTimeout
		prof.ConnectTimeout = *opTimeout
	}
	if *keyspace > 0 {
		prof.Keyspace = *keyspace
	}
	if *rate > 0 {
		prof.Intensity = profile.FixedRate
	}
	if *stress {
		prof = prof.WithStressTimeConstants()
	}
	if prof.GOMAXPROCS > 0 {
		runtime.GOMAXPROCS(prof.GOMAXPROCS)
	}

	servers, err := resolveServers(*serversFlag)
	if err != nil {
		fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("loadgen starting",
		"profile", prof.Name, "servers", len(servers.List()), "workers", prof.Workers,
		"keyspace", prof.Keyspace, "intensity", prof.Intensity, "duration", *duration, "gomaxprocs", runtime.GOMAXPROCS(0))

	client := memcache.NewClient(servers, prof.ClientConfig())
	defer client.Close()

	var opLog *oplog.Writer
	if *oplogPath != "" || prof.OpLog {
		path := *oplogPath
		if path == "" {
			path = "oplog.zst"
		}
		f, err := os.Create(path)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		opLog, err = oplog.NewWriter(f)
		if err != nil {
			fatal(err)
		}
		defer opLog.Close()
		log.Info("op-log enabled", "path", path)
	}

	m := metrics.New()
	var desyncOnce sync.Once
	g := generator.New(client, m, generator.Config{
		Workers:    prof.Workers,
		Keyspace:   prof.Keyspace,
		Duration:   *duration,
		Intensity:  prof.Intensity,
		TargetRate: *rate,
		OpLog:      opLog,
		FlightRing: *flightRing,
	}, func(d generator.DesyncInfo) {
		desyncOnce.Do(func() {
			log.Error("DESYNC DETECTED", "worker", d.Worker, "key", d.KeyID,
				"value", truncate(string(d.Value), 80), "recent_ops", len(d.Recent))
		})
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.Run(ctx)
	}()

	// writeProgress refreshes the on-disk status/snapshot files so a long run's
	// state (run time, totals, latency histogram) can be inspected without
	// waiting for the end. Both writes are atomic, so a reader never sees a torn
	// file. The result file shape matches -out for offline reuse.
	writeProgress := func(snap metrics.Snapshot, elapsed time.Duration) {
		if *statusPath != "" {
			if err := writeAtomic(*statusPath, []byte(statusText(start, elapsed, snap))); err != nil {
				log.Warn("status write failed", "err", err)
			}
		}
		if *snapPath != "" {
			if err := writeJSONAtomic(*snapPath, runResult(*runID, *vm, prof.Name, elapsed, snap, client)); err != nil {
				log.Warn("snapshot write failed", "err", err)
			}
		}
	}

	ticker := time.NewTicker(*reportEvery)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-done:
			break loop
		case <-ticker.C:
			elapsed := time.Since(start)
			snap := m.Snapshot()
			log.Info("progress", "elapsed", elapsed.Round(time.Second).String(),
				"ops", snap.Ops, "throughput", int(snap.Throughput(elapsed)),
				"errors", snap.Errors, "desyncs", snap.Desyncs,
				"p50", snap.Latency.Percentile(50).String(), "p99", snap.Latency.Percentile(99).String())
			writeProgress(snap, elapsed)
		}
	}

	elapsed := time.Since(start)
	final := m.Snapshot()
	writeProgress(final, elapsed)
	fmt.Fprint(os.Stderr, "\n=== final ===\n"+final.Text(elapsed))
	for _, ps := range client.AllPoolStats() {
		log.Info("pool", "addr", ps.Addr, "created", ps.PoolStats.CreatedConns,
			"destroyed", ps.PoolStats.DestroyedConns, "active", ps.PoolStats.ActiveConns,
			"acquire_waits", ps.PoolStats.AcquireWaitCount)
	}

	if err := writeResult(*out, runResult(*runID, *vm, prof.Name, elapsed, final, client)); err != nil {
		fatal(err)
	}

	if final.Desyncs > 0 {
		log.Error("RUN FAILED: desyncs detected", "count", final.Desyncs)
		os.Exit(2)
	}
}

func resolveServers(flagVal string) (memcache.Servers, error) {
	if flagVal != "" {
		var addrs []string
		for a := range strings.SplitSeq(flagVal, ",") {
			if a = strings.TrimSpace(a); a != "" {
				addrs = append(addrs, a)
			}
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("no valid server addresses in -servers")
		}
		return memcache.StaticServers(addrs...), nil
	}
	return memcache.ServersFromEnv("MEMCACHE_SERVERS")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "loadgen:", err)
	os.Exit(1)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func writeResult(path string, r report.RunResult) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if path == "" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// runResult assembles the per-VM result artifact from a metrics snapshot and the
// client's pool stats, shared by the periodic snapshot file and the final -out.
func runResult(runID, vm, profile string, elapsed time.Duration, snap metrics.Snapshot, client *memcache.Client) report.RunResult {
	return report.RunResult{
		RunID:       runID,
		VM:          vm,
		Profile:     profile,
		ElapsedSecs: elapsed.Seconds(),
		Snapshot:    snap,
		PoolStats:   poolStatsJSON(client),
	}
}

// statusText renders the human-readable status written to -status every tick:
// wall-clock run time, the counter/latency summary, and a latency histogram.
func statusText(start time.Time, elapsed time.Duration, snap metrics.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run time: %s (started %s, updated %s)\n\n",
		elapsed.Round(time.Second), start.Format(time.RFC3339), time.Now().Format(time.RFC3339))
	b.WriteString(snap.Text(elapsed))
	b.WriteString("\nlatency distribution (all ops):\n")
	b.WriteString(snap.Latency.DistributionText())
	return b.String()
}

// writeAtomic writes data to path via a temp file + rename, so a concurrent
// reader (e.g. `cat status.txt` during the run) never observes a partial file.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}
