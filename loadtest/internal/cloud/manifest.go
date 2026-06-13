package cloud

import (
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// RunManifest is the provenance record written to <bucket>/<runID>/run.json so
// the GCS history shows what code and configuration produced each run, for
// tracking the library's performance/reliability over time.
type RunManifest struct {
	RunID     string        `json:"run_id"`
	Name      string        `json:"name,omitempty"`
	StartedAt time.Time     `json:"started_at"`
	Git       GitInfo       `json:"git"`
	Config    ConfigSummary `json:"config"`
}

// GitInfo captures the state of the checkout that built the binaries under test.
type GitInfo struct {
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Dirty   bool   `json:"dirty"`
}

// ConfigSummary is the human-readable subset of RunConfig worth comparing across
// runs (it omits credentials/bucket/labels noise).
type ConfigSummary struct {
	Profile        string `json:"profile"`
	Placement      string `json:"placement"`
	ClientZone     string `json:"client_zone"`
	ClientVMs      int    `json:"client_vms"`
	ServerVMs      int    `json:"server_vms"`
	InstancesPerVM int    `json:"instances_per_vm"`
	Duration       string `json:"duration"`
	Workers        int    `json:"workers"`
	Conns          int    `json:"conns"`
	OpTimeout      string `json:"op_timeout,omitempty"`
	Keyspace       int    `json:"keyspace"`
	Stress         bool   `json:"stress"`
	MachineClient  string `json:"machine_client"`
	MachineServer  string `json:"machine_server"`
	MemcachedMB    int    `json:"memcached_mb"`
}

// NewRunManifest assembles the manifest for a run from its config and the git
// state of the current checkout.
func NewRunManifest(cfg RunConfig, runID string, started time.Time) RunManifest {
	return RunManifest{
		RunID:     runID,
		Name:      cfg.Name,
		StartedAt: started.UTC(),
		Git:       gitInfo(),
		Config: ConfigSummary{
			Profile:        cfg.Profile,
			Placement:      cfg.Placement,
			ClientZone:     cfg.ClientZone,
			ClientVMs:      cfg.ClientVMs,
			ServerVMs:      cfg.ServerVMs,
			InstancesPerVM: cfg.InstancesPerVM,
			Duration:       cfg.Duration.String(),
			Workers:        cfg.Workers,
			Conns:          cfg.Conns,
			OpTimeout:      durStr(cfg.OpTimeout),
			Keyspace:       cfg.Keyspace,
			Stress:         cfg.Stress,
			MachineClient:  cfg.MachineTypeClient,
			MachineServer:  cfg.MachineTypeServer,
			MemcachedMB:    cfg.MemoryMB,
		},
	}
}

// durStr renders a duration, or "" when unset (so omitempty drops it).
func durStr(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// JSON renders the manifest as indented JSON.
func (m RunManifest) JSON() []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\n')
}

// gitInfo reads the current checkout's branch, HEAD commit, subject, and dirty
// state. Any field that can't be read is left empty rather than failing the run.
func gitInfo() GitInfo {
	return GitInfo{
		Branch:  git("rev-parse", "--abbrev-ref", "HEAD"),
		Commit:  git("rev-parse", "HEAD"),
		Subject: git("log", "-1", "--format=%s"),
		Dirty:   git("status", "--porcelain") != "",
	}
}

func git(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
