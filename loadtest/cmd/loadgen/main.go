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
)

func main() {
	var (
		serversFlag = flag.String("servers", "", "comma-separated server addresses (or set MEMCACHE_SERVERS)")
		profileName = flag.String("profile", "top-perf", "resource profile: top-perf | efficiency")
		duration    = flag.Duration("duration", time.Hour, "run duration")
		workers     = flag.Int("workers", 0, "override worker count (0 = profile default)")
		keyspace    = flag.Int("keyspace", 0, "override key space size (0 = profile default)")
		rate        = flag.Int("rate", 0, "fixed-rate target ops/sec (0 = saturation)")
		stress      = flag.Bool("stress", false, "shorten connection time-constants for lifecycle churn")
		report      = flag.Duration("report-interval", 10*time.Second, "periodic metrics interval")
		out         = flag.String("out", "", "final metrics JSON file (default stdout)")
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

	ticker := time.NewTicker(*report)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-done:
			break loop
		case <-ticker.C:
			snap := m.Snapshot()
			log.Info("progress", "elapsed", time.Since(start).Round(time.Second).String(),
				"ops", snap.Ops, "throughput", int(snap.Throughput(time.Since(start))),
				"errors", snap.Errors, "desyncs", snap.Desyncs)
		}
	}

	elapsed := time.Since(start)
	final := m.Snapshot()
	fmt.Fprint(os.Stderr, "\n=== final ===\n"+final.Text(elapsed))
	for _, ps := range client.AllPoolStats() {
		log.Info("pool", "addr", ps.Addr, "created", ps.PoolStats.CreatedConns,
			"destroyed", ps.PoolStats.DestroyedConns, "active", ps.PoolStats.ActiveConns,
			"acquire_waits", ps.PoolStats.AcquireWaitCount)
	}

	if err := writeResult(*out, runResult{
		RunID:       *runID,
		VM:          *vm,
		Profile:     prof.Name,
		ElapsedSecs: elapsed.Seconds(),
		Snapshot:    final,
		PoolStats:   poolStatsJSON(client),
	}); err != nil {
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

func writeResult(path string, r runResult) error {
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
