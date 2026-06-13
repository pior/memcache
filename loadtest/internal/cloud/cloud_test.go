package cloud

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewRunID(t *testing.T) {
	id := NewRunID(time.Date(2026, 6, 13, 6, 7, 30, 0, time.UTC))
	if !strings.HasPrefix(id, "20260613-060730-") {
		t.Errorf("run id = %q, want timestamp prefix", id)
	}
	if len(id) != len("20260613-060730-")+4 {
		t.Errorf("run id = %q, want 4-char suffix", id)
	}
}

func TestLabelsSanitized(t *testing.T) {
	l := Labels("RUN/Id", RoleServer, "Top Perf", "Pior@x", 1234, 6)
	if l["app"] != AppLabel || l["role"] != "server" {
		t.Errorf("labels = %v", l)
	}
	if l["run-id"] != "run-id" {
		t.Errorf("run-id sanitize = %q, want run-id", l["run-id"])
	}
	if l["profile"] != "top-perf" || l["owner"] != "pior-x" {
		t.Errorf("sanitize = %v", l)
	}
}

func TestRegionOfAndTier(t *testing.T) {
	if RegionOf("us-central1-a") != "us-central1" {
		t.Error("RegionOf")
	}
	cases := []struct {
		server, client string
		want           Tier
	}{
		{"us-central1-a", "us-central1-a", TierSameZone},
		{"us-central1-b", "us-central1-a", TierCrossZone},
		{"us-east1-b", "us-central1-a", TierCrossRegion},
	}
	for _, c := range cases {
		if got := TierOf(c.server, c.client); got != c.want {
			t.Errorf("TierOf(%s,%s) = %s, want %s", c.server, c.client, got, c.want)
		}
	}
}

func TestParsePlacement(t *testing.T) {
	local, err := ParsePlacement("local", "us-central1-a", 3)
	if err != nil || len(local) != 1 || local[0] != (ServerPlacement{"us-central1-a", 3}) {
		t.Errorf("local = %v err=%v", local, err)
	}

	regional, _ := ParsePlacement("regional", "us-central1-a", 4)
	total := 0
	for _, p := range regional {
		total += p.Count
		if RegionOf(p.Zone) != "us-central1" {
			t.Errorf("regional zone outside region: %s", p.Zone)
		}
	}
	if total != 4 {
		t.Errorf("regional total = %d, want 4", total)
	}

	custom, err := ParsePlacement("us-central1-a:2,us-east1-b:1", "us-central1-a", 99)
	if err != nil || len(custom) != 2 || custom[1] != (ServerPlacement{"us-east1-b", 1}) {
		t.Errorf("custom = %v err=%v", custom, err)
	}

	if _, err := ParsePlacement("bad-entry", "us-central1-a", 1); err == nil {
		t.Error("expected error on malformed custom spec")
	}
}

func TestServerAddresses(t *testing.T) {
	addrs := ServerAddresses([]string{"10.0.0.1", "10.0.0.2"}, 2)
	want := "10.0.0.1:11211,10.0.0.1:11212,10.0.0.2:11211,10.0.0.2:11212"
	if strings.Join(addrs, ",") != want {
		t.Errorf("addresses = %v", addrs)
	}
}

func TestServerStartupScript(t *testing.T) {
	s := ServerStartupScript(ServerScriptParams{RunID: "r1", VMName: "srv0", InstancesPerVM: 3, Bucket: "gs://b"})
	for _, want := range []string{"apt-get install -y memcached", "seq 11211 11213", "gs://b/bin/hoststat", "hoststat"} {
		if !strings.Contains(s, want) {
			t.Errorf("server script missing %q", want)
		}
	}
}

func TestClientStartupScriptCPUQuota(t *testing.T) {
	s := ClientStartupScript(ClientScriptParams{
		RunID: "r1", VMName: "cli0", Servers: []string{"10.0.0.1:11211"},
		Profile: "efficiency", Duration: time.Hour, OpLog: true, CPUQuotaPercent: 100, Bucket: "gs://b",
	})
	for _, want := range []string{"CPUQuota=100%", "-servers 10.0.0.1:11211", "-oplog", "gcloud storage cp", "gs://b/r1/client/cli0/"} {
		if !strings.Contains(s, want) {
			t.Errorf("client script missing %q", want)
		}
	}
}

func TestOrchestratorDryRun(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	o := NewOrchestrator(NewDryProvisioner(log), log)
	cfg := RunConfig{
		ClientZone: "us-central1-a", Placement: "us-central1-a:1,us-east1-b:1",
		ClientVMs: 2, ServerVMs: 2, InstancesPerVM: 2, Profile: "top-perf",
		Duration: time.Millisecond, MachineTypeClient: "c3-highcpu-8", MachineTypeServer: "c3-highcpu-8",
		Bucket: "gs://b", TTLHours: 6, BootMargin: time.Millisecond,
	}
	runID, err := o.Run(context.Background(), cfg, map[string]string{"loadgen": "/tmp/loadgen"}, t.TempDir(), false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.HasPrefix(runID, "20") {
		t.Errorf("run id = %q", runID)
	}
}

func TestBuildServerVMs(t *testing.T) {
	cfg := RunConfig{
		ClientZone: "us-central1-a", Placement: "regional", ServerVMs: 3,
		InstancesPerVM: 2, Profile: "top-perf", MachineTypeServer: "c3-highcpu-8",
		Owner: "pior", TTLHours: 6, Bucket: "gs://b",
	}
	vms, placements, err := BuildServerVMs(cfg, "r1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 3 {
		t.Errorf("got %d server VMs, want 3", len(vms))
	}
	if len(placements) == 0 {
		t.Error("no placements")
	}
	if vms[0].Labels["app"] != AppLabel || vms[0].Role != RoleServer {
		t.Errorf("vm labels/role = %v %s", vms[0].Labels, vms[0].Role)
	}
}
