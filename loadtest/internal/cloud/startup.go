package cloud

import (
	"fmt"
	"strings"
	"time"
)

// metadataIP is the shell snippet that reads the VM's primary private IP.
const metadataIP = `$(curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/ip)`

// serverStatInterval is how often a server VM pushes its in-progress host
// metrics to GCS. A server has no natural end-of-run event (it lives until
// teardown), so it uploads on a timer; teardown loses at most one interval.
const serverStatInterval = 30 * time.Second

// gcsPreamble is the head of every startup-script. It defines GCS helpers that
// use the VM service-account token and the GCS XML API via curl. The stock
// debian-12 image ships no gcloud CLI, so we avoid it entirely: only curl and
// coreutils (grep/cut) are used, which are always present. The helpers are
// written to a file so transient systemd units can source them as well.
const gcsPreamble = `#!/bin/bash
set -euxo pipefail
cat >/usr/local/lib/mclt-gcs.sh <<'GCS'
gcs_url() { echo "https://storage.googleapis.com/${1#gs://}"; }
gcs_token() {
  curl -fsS -H "Metadata-Flavor: Google" \
    http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token \
    | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4
}
gcs_dl() { curl -fsS -H "Authorization: Bearer $(gcs_token)" -o "$2" "$(gcs_url "$1")"; }
gcs_up() { curl -fsS -X PUT -H "Authorization: Bearer $(gcs_token)" \
  -H "Content-Type: application/octet-stream" --upload-file "$1" "$(gcs_url "$2")"; }
GCS
source /usr/local/lib/mclt-gcs.sh
`

// ServerScriptParams configures a server VM startup-script.
type ServerScriptParams struct {
	RunID          string
	VMName         string
	InstancesPerVM int
	MemoryMB       int
	Bucket         string // gs://… root for this run
}

// ServerStartupScript builds the bash startup-script for a memcached server VM:
// it installs memcached, binds one instance per port to the private IP, starts
// the host-metrics sampler, and pushes those metrics to GCS on a timer.
func ServerStartupScript(p ServerScriptParams) string {
	if p.MemoryMB == 0 {
		p.MemoryMB = 256
	}
	lastPort := MemcachePort + p.InstancesPerVM - 1
	var b strings.Builder
	b.WriteString(gcsPreamble)
	b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
	b.WriteString("apt-get update && apt-get install -y memcached\n")
	b.WriteString("systemctl stop memcached || true\nsystemctl disable memcached || true\n")
	fmt.Fprintf(&b, "IP=%s\n", metadataIP)
	fmt.Fprintf(&b, "for PORT in $(seq 11211 %d); do\n", lastPort)
	// memcached refuses to run as root without -u; the startup-script runs as
	// root, so bind it to the package's unprivileged memcache user. Without this
	// it exits 64/USAGE immediately and nothing listens.
	fmt.Fprintf(&b, "  systemd-run --unit=memcached@${PORT} --collect "+
		"memcached -u memcache -l ${IP} -p ${PORT} -m %d -c 8192 -t 4\n", p.MemoryMB)
	b.WriteString("done\n")
	b.WriteString(downloadBinary(p.Bucket, "hoststat"))
	b.WriteString(startHoststat(p.RunID, p.VMName))
	// The server runs until teardown, so there is no end-of-run upload like the
	// client has. Push the in-progress sampler output on a timer so the latest
	// data is always in GCS; the loop dies with the VM at teardown.
	dst := fmt.Sprintf("%s/%s/server/%s/hoststat.jsonl", p.Bucket, p.RunID, p.VMName)
	fmt.Fprintf(&b, "systemd-run --unit=hoststat-upload --collect /bin/bash -c "+
		"'source /usr/local/lib/mclt-gcs.sh; while true; do sleep %d; "+
		"gcs_up /var/log/hoststat.jsonl %s || true; done'\n",
		int(serverStatInterval.Seconds()), dst)
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
	Conns           int
	OpTimeout       time.Duration
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
	b.WriteString(gcsPreamble)
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
	if p.Conns > 0 {
		args = append(args, fmt.Sprintf("-conns %d", p.Conns))
	}
	if p.OpTimeout > 0 {
		args = append(args, "-timeout "+p.OpTimeout.String())
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
	dst := fmt.Sprintf("%s/%s/client/%s", p.Bucket, p.RunID, p.VMName)
	fmt.Fprintf(&b, "gcs_up /var/log/loadgen-result.json %s/loadgen-result.json || true\n", dst)
	fmt.Fprintf(&b, "gcs_up /var/log/hoststat.jsonl %s/hoststat.jsonl || true\n", dst)
	if p.OpLog {
		fmt.Fprintf(&b, "gcs_up /var/log/oplog.zst %s/oplog.zst || true\n", dst)
	}
	return b.String()
}

func downloadBinary(bucket, name string) string {
	return fmt.Sprintf("gcs_dl %s/bin/%s /usr/local/bin/%s\nchmod +x /usr/local/bin/%s\n",
		bucket, name, name, name)
}

func startHoststat(runID, vmName string) string {
	return fmt.Sprintf("systemd-run --unit=hoststat --collect "+
		"/usr/local/bin/hoststat -interval 5s -run-id %s -vm %s -out /var/log/hoststat.jsonl\n",
		runID, vmName)
}
