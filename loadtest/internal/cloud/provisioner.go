package cloud

import (
	"context"
	"fmt"
	"log/slog"
)

// Provisioner is the GCP-facing surface. The orchestrator drives a run through
// it; the live implementation uses the Compute/Storage SDK, while DryProvisioner
// records intended actions so a run can be fully planned without touching GCP.
type Provisioner interface {
	// EnsureNetwork creates/ensures the global VPC and a subnet per region.
	EnsureNetwork(ctx context.Context, runID string, regions []string) error
	// EnsureFirewall opens the memcache ports (one per instance-per-VM) from
	// client tag to server tag only.
	EnsureFirewall(ctx context.Context, runID string, instancesPerVM int) error
	// UploadBinaries uploads name->localPath binaries to <bucket>/bin/.
	UploadBinaries(ctx context.Context, bucket string, bins map[string]string) error
	// CreateVM creates a VM and returns its private IP.
	CreateVM(ctx context.Context, vm PlannedVM) (privateIP string, err error)
	// CollectLogs downloads <bucket>/<runID>/** into localDir.
	CollectLogs(ctx context.Context, bucket, runID, localDir string) error
	// DeleteByRun deletes all resources labelled with runID.
	DeleteByRun(ctx context.Context, runID string) error
	// Reap deletes resources older than ttlHours (by the created label).
	Reap(ctx context.Context, ttlHours int) ([]string, error)
}

// DryProvisioner implements Provisioner by logging intended actions and
// returning synthetic private IPs. It performs no cloud calls.
type DryProvisioner struct {
	log    *slog.Logger
	nextIP int
}

// NewDryProvisioner returns a dry-run provisioner.
func NewDryProvisioner(log *slog.Logger) *DryProvisioner {
	return &DryProvisioner{log: log, nextIP: 2}
}

func (d *DryProvisioner) EnsureNetwork(_ context.Context, runID string, regions []string) error {
	d.log.Info("[dry] ensure network", "run", runID, "vpc", "mclt-"+runID, "subnets", regions)
	return nil
}

func (d *DryProvisioner) EnsureFirewall(_ context.Context, runID string, instancesPerVM int) error {
	d.log.Info("[dry] ensure firewall", "run", runID,
		"rule", "allow "+MemcachePortRange(instancesPerVM)+" client->server (internal)")
	return nil
}

func (d *DryProvisioner) UploadBinaries(_ context.Context, bucket string, bins map[string]string) error {
	for name, path := range bins {
		d.log.Info("[dry] upload binary", "from", path, "to", bucket+"/bin/"+name)
	}
	return nil
}

func (d *DryProvisioner) CreateVM(_ context.Context, vm PlannedVM) (string, error) {
	ip := fmt.Sprintf("10.128.0.%d", d.nextIP)
	d.nextIP++
	d.log.Info("[dry] create VM", "name", vm.Name, "role", vm.Role, "zone", vm.Zone,
		"machine", vm.MachineType, "spot", vm.Spot, "ip", ip)
	return ip, nil
}

func (d *DryProvisioner) CollectLogs(_ context.Context, bucket, runID, localDir string) error {
	d.log.Info("[dry] collect logs", "from", fmt.Sprintf("%s/%s/**", bucket, runID), "to", localDir)
	return nil
}

func (d *DryProvisioner) DeleteByRun(_ context.Context, runID string) error {
	d.log.Info("[dry] delete by run", "run", runID, "filter", "app="+AppLabel+" run-id="+runID)
	return nil
}

func (d *DryProvisioner) Reap(_ context.Context, ttlHours int) ([]string, error) {
	d.log.Info("[dry] reap", "older_than_hours", ttlHours)
	return nil, nil
}
