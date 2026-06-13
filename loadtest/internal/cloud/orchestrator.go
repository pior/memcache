package cloud

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Orchestrator drives a run through a Provisioner. It is provider-agnostic:
// pass DryProvisioner to plan a run, or the GCE provisioner to execute it.
type Orchestrator struct {
	p   Provisioner
	log *slog.Logger
	// SkipWait skips the workload wait (used by dry-run, where no VMs run).
	SkipWait bool
}

// NewOrchestrator wires an orchestrator to a provisioner.
func NewOrchestrator(p Provisioner, log *slog.Logger) *Orchestrator {
	return &Orchestrator{p: p, log: log}
}

// Run executes the full lifecycle: network → firewall → upload → servers →
// resolve addresses → clients → wait → collect → teardown. bins maps binary
// name to local path. localDir receives collected logs. If keep is true the
// resources are left running (no teardown).
func (o *Orchestrator) Run(ctx context.Context, cfg RunConfig, bins map[string]string, localDir string, keep bool) (runID string, err error) {
	runID = NewRunID(time.Now())
	created := time.Now().Unix()
	o.log.Info("run starting", "run_id", runID, "profile", cfg.Profile,
		"clients", cfg.ClientVMs, "servers", cfg.ServerVMs, "instances_per_vm", cfg.InstancesPerVM,
		"placement", cfg.Placement, "duration", cfg.Duration)

	serverVMs, placements, err := BuildServerVMs(cfg, runID, created)
	if err != nil {
		return runID, err
	}
	regions := Regions(placements, cfg.ClientZone)

	// Always attempt teardown unless asked to keep — even on error.
	if !keep {
		defer func() {
			if derr := o.p.DeleteByRun(context.WithoutCancel(ctx), runID); derr != nil {
				o.log.Error("teardown failed", "run_id", runID, "err", derr)
			}
		}()
	}

	if err := o.p.EnsureNetwork(ctx, runID, regions); err != nil {
		return runID, fmt.Errorf("network: %w", err)
	}
	if err := o.p.EnsureFirewall(ctx, runID, cfg.InstancesPerVM); err != nil {
		return runID, fmt.Errorf("firewall: %w", err)
	}
	if err := o.p.UploadBinaries(ctx, cfg.Bucket, bins); err != nil {
		return runID, fmt.Errorf("upload: %w", err)
	}

	ips, err := o.createAll(ctx, serverVMs)
	if err != nil {
		return runID, fmt.Errorf("servers: %w", err)
	}
	addresses := ServerAddresses(ips, cfg.InstancesPerVM)
	o.log.Info("servers ready", "count", len(serverVMs), "addresses", len(addresses))

	clientVMs := BuildClientVMs(cfg, runID, created, addresses)
	if _, err := o.createAll(ctx, clientVMs); err != nil {
		return runID, fmt.Errorf("clients: %w", err)
	}
	o.log.Info("clients ready, workload running", "count", len(clientVMs), "duration", cfg.Duration)

	// Clients run the workload for cfg.Duration then upload artifacts. Wait for
	// that plus a margin for boot, binary download, and upload.
	if !o.SkipWait {
		margin := cfg.BootMargin
		if margin == 0 {
			margin = 3 * time.Minute
		}
		select {
		case <-ctx.Done():
			return runID, ctx.Err()
		case <-time.After(cfg.Duration + margin):
		}
	}

	if err := o.p.CollectLogs(ctx, cfg.Bucket, runID, localDir); err != nil {
		return runID, fmt.Errorf("collect: %w", err)
	}
	o.log.Info("run complete", "run_id", runID, "logs", localDir)
	return runID, nil
}

func (o *Orchestrator) createAll(ctx context.Context, vms []PlannedVM) ([]string, error) {
	ips := make([]string, 0, len(vms))
	for _, vm := range vms {
		ip, err := o.p.CreateVM(ctx, vm)
		if err != nil {
			return ips, fmt.Errorf("create %s: %w", vm.Name, err)
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

// Down tears down a run's resources.
func (o *Orchestrator) Down(ctx context.Context, runID string) error {
	return o.p.DeleteByRun(ctx, runID)
}

// Reap deletes resources older than ttlHours.
func (o *Orchestrator) Reap(ctx context.Context, ttlHours int) ([]string, error) {
	return o.p.Reap(ctx, ttlHours)
}
