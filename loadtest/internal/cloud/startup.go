package cloud

import (
	"fmt"
	"strings"
	"time"
)

// metadataIP is the shell snippet that reads the VM's primary private IP.
const metadataIP = `$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/ip)`

// ServerScriptParams configures a server VM startup-script.
type ServerScriptParams struct {
	RunID          string
	VMName         string
	InstancesPerVM int
	MemoryMB       int
	Bucket         string // gs://… root for this run
}

// ServerStartupScript builds the bash startup-script for a memcached server VM:
// it installs memcached, binds one instance per port to the private IP, and
// starts the host-metrics sampler.
func ServerStartupScript(p ServerScriptParams) string {
	if p.MemoryMB == 0 {
		p.MemoryMB = 256
	}
	lastPort := 11211 + p.InstancesPerVM - 1
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -euxo pipefail\n")
	b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
	b.WriteString("apt-get update && apt-get install -y memcached\n")
	b.WriteString("systemctl stop memcached || true\nsystemctl disable memcached || true\n")
	fmt.Fprintf(&b, "IP=%s\n", metadataIP)
	fmt.Fprintf(&b, "for PORT in $(seq 11211 %d); do\n", lastPort)
	fmt.Fprintf(&b, "  systemd-run --unit=memcached@${PORT} --collect "+
		"memcached -l ${IP} -p ${PORT} -m %d -c 8192 -t 4\n", p.MemoryMB)
	b.WriteString("done\n")
	b.WriteString(downloadBinary(p.Bucket, "hoststat"))
	b.WriteString(startHoststat(p.RunID, p.VMName))
	return b.String()
}

// ClientScriptParams configures a client VM startup-script.
type ClientScriptParams struct {
	RunID           string
	VMName          string
	Servers         []string // host:port addresses
	Profile         string
	Duration        time.Duration
	Workers         int
	Keyspace        int
	OpLog           bool
	Stress          bool
	CPUQuotaPercent int // 0 = unconstrained; e.g. 100 = one vCPU
	Bucket          string
}

// ClientStartupScript builds the bash startup-script for a load-generator VM:
// it downloads the binaries, starts host-metrics sampling, runs loadgen for the
// configured duration (optionally CPU-capped via a transient cgroup), and uploads
// all artifacts to GCS.
func ClientStartupScript(p ClientScriptParams) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -euxo pipefail\n")
	b.WriteString(downloadBinary(p.Bucket, "loadgen"))
	b.WriteString(downloadBinary(p.Bucket, "hoststat"))
	b.WriteString(startHoststat(p.RunID, p.VMName))

	args := []string{
		"-servers " + strings.Join(p.Servers, ","),
		"-profile " + p.Profile,
		"-duration " + p.Duration.String(),
		fmt.Sprintf("-run-id %s -vm %s", p.RunID, p.VMName),
		"-out /var/log/loadgen-result.json",
	}
	if p.Workers > 0 {
		args = append(args, fmt.Sprintf("-workers %d", p.Workers))
	}
	if p.Keyspace > 0 {
		args = append(args, fmt.Sprintf("-keyspace %d", p.Keyspace))
	}
	if p.OpLog {
		args = append(args, "-oplog /var/log/oplog.zst")
	}
	if p.Stress {
		args = append(args, "-stress")
	}
	loadgenCmd := "/usr/local/bin/loadgen " + strings.Join(args, " ")

	if p.CPUQuotaPercent > 0 {
		// Constrain CPU to the quota via a transient cgroup so the only variable
		// vs the unconstrained profile is CPU allowance.
		fmt.Fprintf(&b, "systemd-run --wait --collect --property=CPUQuota=%d%% %s\n",
			p.CPUQuotaPercent, loadgenCmd)
	} else {
		b.WriteString(loadgenCmd)
		b.WriteByte('\n')
	}

	// Stop sampling and upload all artifacts.
	b.WriteString("systemctl stop hoststat || true\n")
	dst := fmt.Sprintf("%s/%s/client/%s/", p.Bucket, p.RunID, p.VMName)
	fmt.Fprintf(&b, "gcloud storage cp /var/log/loadgen-result.json %s || true\n", dst)
	fmt.Fprintf(&b, "gcloud storage cp /var/log/hoststat.jsonl %s || true\n", dst)
	if p.OpLog {
		fmt.Fprintf(&b, "gcloud storage cp /var/log/oplog.zst %s || true\n", dst)
	}
	return b.String()
}

func downloadBinary(bucket, name string) string {
	return fmt.Sprintf("gcloud storage cp %s/bin/%s /usr/local/bin/%s\nchmod +x /usr/local/bin/%s\n",
		bucket, name, name, name)
}

func startHoststat(runID, vmName string) string {
	return fmt.Sprintf("systemd-run --unit=hoststat --collect "+
		"/usr/local/bin/hoststat -interval 5s -run-id %s -vm %s -out /var/log/hoststat.jsonl\n",
		runID, vmName)
}
