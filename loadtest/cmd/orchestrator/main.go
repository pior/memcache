// Command orchestrator plans and drives tier-3 cloud load-test runs from the
// developer's machine. It provisions GCE VMs (servers + load generators),
// uploads the binaries, runs the workload, collects logs, and tears down —
// everything labelled for cost reporting and reaping.
//
// Subcommands:
//
//	build     cross-compile loadgen + hoststat for linux/amd64
//	dry-run   print the full plan (network, VMs, scripts, labels) — no GCP calls
//	run       execute a run (requires the live GCE provisioner; see SPEC §14)
//	down      tear down a run by id
//	reap      delete resources older than --ttl-hours
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pior/memcache/loadtest/internal/cloud"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "build":
		err = runBuild(args, log)
	case "dry-run":
		err = runPlan(args, log, true)
	case "run":
		err = runPlan(args, log, false)
	case "down":
		err = runDown(args, log)
	case "reap":
		err = runReap(args, log)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: orchestrator <build|dry-run|run|down|reap> [flags]")
}

// configFlags registers the run-configuration flags shared by run and dry-run.
func configFlags(fs *flag.FlagSet) *cloud.RunConfig {
	cfg := &cloud.RunConfig{}
	fs.StringVar(&cfg.Project, "project", envOr("CLOUDSDK_CORE_PROJECT", ""), "GCP project id")
	fs.StringVar(&cfg.Owner, "owner", envOr("USER", "unknown"), "owner label")
	fs.StringVar(&cfg.ClientZone, "zone", "us-central1-a", "client reference zone")
	fs.StringVar(&cfg.Placement, "placement", "local", "server placement: local|regional|global|zone:count,...")
	fs.IntVar(&cfg.ClientVMs, "clients", 3, "number of client VMs")
	fs.IntVar(&cfg.ServerVMs, "servers", 3, "number of server VMs")
	fs.IntVar(&cfg.InstancesPerVM, "instances-per-vm", 2, "memcached instances per server VM")
	fs.StringVar(&cfg.Profile, "profile", "top-perf", "resource profile: top-perf|efficiency")
	fs.DurationVar(&cfg.Duration, "duration", time.Hour, "workload duration")
	fs.IntVar(&cfg.Workers, "workers", 0, "override workers per client (0 = profile default)")
	fs.IntVar(&cfg.Conns, "conns", 0, "override max connections per server (0 = profile default)")
	fs.IntVar(&cfg.Keyspace, "keyspace", 0, "override key space (0 = profile default)")
	fs.BoolVar(&cfg.OpLog, "oplog", false, "enable the full per-op compressed log")
	fs.BoolVar(&cfg.Stress, "stress", false, "shorten connection time-constants")
	fs.IntVar(&cfg.CPUQuotaPercent, "cpu-quota", 0, "client CPU cap percent (0 = unconstrained)")
	fs.StringVar(&cfg.MachineTypeClient, "client-machine", "c3-highcpu-8", "client machine type")
	fs.StringVar(&cfg.MachineTypeServer, "server-machine", "c3-highcpu-8", "server machine type")
	fs.IntVar(&cfg.MemoryMB, "memcached-mb", 256, "memory per memcached instance")
	fs.BoolVar(&cfg.Spot, "spot", true, "use spot VMs (forced off for top-perf clients)")
	fs.IntVar(&cfg.TTLHours, "ttl-hours", 6, "reap resources older than this")
	fs.StringVar(&cfg.Bucket, "bucket", "", "GCS bucket root, e.g. gs://my-bucket (default gs://<project>-memcache-loadtest)")
	return cfg
}

func runPlan(args []string, log *slog.Logger, dry bool) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfg := configFlags(fs)
	outDir := fs.String("out", "./runs", "local directory for collected logs")
	keep := fs.Bool("keep", false, "leave resources running (skip teardown)")
	binDir := fs.String("bin", "./bin", "directory holding cross-compiled binaries")
	_ = fs.Parse(args)

	if cfg.Bucket == "" && cfg.Project != "" {
		cfg.Bucket = "gs://" + cfg.Project + "-memcache-loadtest"
	}
	if cfg.Bucket == "" {
		return fmt.Errorf("set -bucket or -project")
	}

	ctx := context.Background()
	var prov cloud.Provisioner
	if dry {
		prov = cloud.NewDryProvisioner(log)
	} else {
		if cfg.Project == "" {
			return fmt.Errorf("-project is required for a live run")
		}
		gce, err := cloud.NewGCEProvisioner(ctx, cfg.Project, log)
		if err != nil {
			return fmt.Errorf("init GCP clients: %w", err)
		}
		defer gce.Close()
		prov = gce
	}

	bins := map[string]string{
		"loadgen":  filepath.Join(*binDir, "loadgen"),
		"hoststat": filepath.Join(*binDir, "hoststat"),
	}
	o := cloud.NewOrchestrator(prov, log)
	o.SkipWait = dry // dry-run prints the plan without waiting for a workload
	runID, err := o.Run(ctx, *cfg, bins, *outDir, *keep)
	if err != nil {
		return err
	}
	log.Info("plan complete", "run_id", runID)
	return nil
}

func runDown(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	project := fs.String("project", envOr("CLOUDSDK_CORE_PROJECT", ""), "GCP project id")
	runID := fs.String("run-id", "", "run id to tear down")
	_ = fs.Parse(args)
	if *runID == "" {
		return fmt.Errorf("-run-id is required")
	}
	ctx := context.Background()
	prov, err := provisioner(ctx, *project, log)
	if err != nil {
		return err
	}
	defer prov.Close()
	return cloud.NewOrchestrator(prov, log).Down(ctx, *runID)
}

func runReap(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("reap", flag.ExitOnError)
	project := fs.String("project", envOr("CLOUDSDK_CORE_PROJECT", ""), "GCP project id")
	ttl := fs.Int("ttl-hours", 6, "delete resources older than this")
	_ = fs.Parse(args)
	ctx := context.Background()
	prov, err := provisioner(ctx, *project, log)
	if err != nil {
		return err
	}
	defer prov.Close()
	deleted, err := cloud.NewOrchestrator(prov, log).Reap(ctx, *ttl)
	if err != nil {
		return err
	}
	log.Info("reap complete", "deleted", len(deleted))
	return nil
}

// provisioner builds the live GCE provisioner (down/reap touch real resources).
func provisioner(ctx context.Context, project string, log *slog.Logger) (*cloud.GCEProvisioner, error) {
	if project == "" {
		return nil, fmt.Errorf("-project is required")
	}
	return cloud.NewGCEProvisioner(ctx, project, log)
}

// runBuild cross-compiles the VM binaries for linux/amd64.
func runBuild(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	outDir := fs.String("out", "./bin", "output directory")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	for _, bin := range []string{"loadgen", "hoststat"} {
		out := filepath.Join(*outDir, bin)
		cmd := exec.Command("go", "build", "-o", out, "./cmd/"+bin)
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", bin, err)
		}
		log.Info("built", "binary", out, "target", "linux/amd64")
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
