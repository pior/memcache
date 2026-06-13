package cloud

import (
	"fmt"
	"time"
)

// RunConfig is the user-facing configuration for a tier-3 run.
type RunConfig struct {
	Project    string
	Owner      string
	ClientZone string
	Placement  string

	ClientVMs      int
	ServerVMs      int
	InstancesPerVM int

	Profile         string
	Duration        time.Duration
	Workers         int
	Keyspace        int
	OpLog           bool
	Stress          bool
	CPUQuotaPercent int // client CPU cap; 0 = unconstrained

	MachineTypeClient string
	MachineTypeServer string
	MemoryMB          int
	Spot              bool
	TTLHours          int
	Bucket            string

	// BootMargin is extra wall-clock added to Duration to cover VM boot, binary
	// download, and artifact upload before logs are collected. Zero defaults to
	// 3 minutes.
	BootMargin time.Duration
}

// PlannedVM is one VM the orchestrator will create.
type PlannedVM struct {
	Name          string
	Role          Role
	Zone          string
	MachineType   string
	Spot          bool
	Labels        map[string]string
	StartupScript string
}

// BuildServerVMs resolves the placement and produces the server VM specs.
func BuildServerVMs(cfg RunConfig, runID string, created int64) ([]PlannedVM, []ServerPlacement, error) {
	placements, err := ParsePlacement(cfg.Placement, cfg.ClientZone, cfg.ServerVMs)
	if err != nil {
		return nil, nil, err
	}
	var vms []PlannedVM
	idx := 0
	for _, pl := range placements {
		for range pl.Count {
			name := fmt.Sprintf("mclt-%s-srv-%d", runID, idx)
			vms = append(vms, PlannedVM{
				Name:        name,
				Role:        RoleServer,
				Zone:        pl.Zone,
				MachineType: cfg.MachineTypeServer,
				Spot:        cfg.Spot,
				Labels:      Labels(runID, RoleServer, cfg.Profile, cfg.Owner, created, cfg.TTLHours),
				StartupScript: ServerStartupScript(ServerScriptParams{
					RunID:          runID,
					VMName:         name,
					InstancesPerVM: cfg.InstancesPerVM,
					MemoryMB:       cfg.MemoryMB,
					Bucket:         cfg.Bucket,
				}),
			})
			idx++
		}
	}
	return vms, placements, nil
}

// ServerAddresses expands server private IPs into the host:port address list
// the client pools over (IPs × instance ports).
func ServerAddresses(ips []string, instancesPerVM int) []string {
	var addrs []string
	for _, ip := range ips {
		for p := range instancesPerVM {
			addrs = append(addrs, fmt.Sprintf("%s:%d", ip, 11211+p))
		}
	}
	return addrs
}

// BuildClientVMs produces the client VM specs, given the resolved server
// address list (known only after servers are created; dry-run passes
// placeholders).
func BuildClientVMs(cfg RunConfig, runID string, created int64, addresses []string) []PlannedVM {
	vms := make([]PlannedVM, 0, cfg.ClientVMs)
	for i := range cfg.ClientVMs {
		name := fmt.Sprintf("mclt-%s-cli-%d", runID, i)
		vms = append(vms, PlannedVM{
			Name:        name,
			Role:        RoleClient,
			Zone:        cfg.ClientZone,
			MachineType: cfg.MachineTypeClient,
			Spot:        cfg.Spot && cfg.Profile != "top-perf", // perf wants stable, non-preemptible
			Labels:      Labels(runID, RoleClient, cfg.Profile, cfg.Owner, created, cfg.TTLHours),
			StartupScript: ClientStartupScript(ClientScriptParams{
				RunID:           runID,
				VMName:          name,
				Servers:         addresses,
				Profile:         cfg.Profile,
				Duration:        cfg.Duration,
				Workers:         cfg.Workers,
				Keyspace:        cfg.Keyspace,
				OpLog:           cfg.OpLog,
				Stress:          cfg.Stress,
				CPUQuotaPercent: cfg.CPUQuotaPercent,
				Bucket:          cfg.Bucket,
			}),
		})
	}
	return vms
}
