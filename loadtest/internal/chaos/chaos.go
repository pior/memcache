// Package chaos defines the fault-injection schedule a server VM executes
// during a run: the schedule format, the preset timelines, and the shell
// commands each fault maps to. The orchestrator renders per-VM schedules and
// uploads them to GCS; the chaosd binary downloads its schedule at boot and
// executes it locally (there is no SSH path to the VMs).
//
// The faults are the real degraded-network conditions the client must
// survive, not just clean failures:
//
//   - latency: tc netem delay/jitter/loss on the NIC — a slow or lossy path
//   - blackhole: iptables DROP on the memcached ports — a partitioned server
//     (SYNs and established-flow packets vanish; nothing is refused)
//   - freeze: SIGSTOP all memcached processes — a hung-but-connected server
//     (TCP stays established, reads stall; the gray failure)
//   - kill: SIGKILL all memcached processes — a crash (clients see RST)
//   - heal: undo everything, restart killed instances
package chaos

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Action is one fault type.
type Action string

const (
	ActionLatency   Action = "latency"
	ActionBlackhole Action = "blackhole"
	ActionFreeze    Action = "freeze"
	ActionKill      Action = "kill"
	ActionHeal      Action = "heal"
)

// Event is one scheduled fault, at an offset from workload start.
type Event struct {
	AtSecs int    `json:"at_secs"`
	Action Action `json:"action"`

	// Latency parameters (ActionLatency only).
	DelayMS  int     `json:"delay_ms,omitempty"`
	JitterMS int     `json:"jitter_ms,omitempty"`
	LossPct  float64 `json:"loss_pct,omitempty"`
}

// Schedule is the per-VM chaos plan chaosd executes.
type Schedule struct {
	// Iface is the NIC for tc netem; empty means auto-detect the
	// default-route interface.
	Iface string `json:"iface,omitempty"`
	// Ports is the memcached port range in iptables --dport syntax
	// ("11211" or "11211:11213").
	Ports string `json:"ports"`
	// Relaunch is an idempotent shell command that (re)starts the VM's
	// memcached instances; heal runs it so a kill window ends with the
	// server back up.
	Relaunch string `json:"relaunch,omitempty"`
	Events   []Event `json:"events"`
}

// JSON serializes the schedule.
func (s Schedule) JSON() []byte {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err) // static struct, cannot fail
	}
	return data
}

// ParseSchedule deserializes a schedule and sorts its events by offset.
func ParseSchedule(data []byte) (Schedule, error) {
	var s Schedule
	if err := json.Unmarshal(data, &s); err != nil {
		return Schedule{}, fmt.Errorf("chaos schedule: %w", err)
	}
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].AtSecs < s.Events[j].AtSecs })
	return s, nil
}

// PortSpec renders the iptables --dport range for n instances at basePort.
func PortSpec(basePort, n int) string {
	if n <= 1 {
		return fmt.Sprintf("%d", basePort)
	}
	return fmt.Sprintf("%d:%d", basePort, basePort+n-1)
}

// chainName is the dedicated iptables chain holding the injected DROP rules,
// so heal can flush them without touching anything else on the VM.
const chainName = "MCLT-CHAOS"

// SetupCommands prepares the VM for fault injection: create the dedicated
// iptables chain and hook it into INPUT. Idempotent-enough for one boot.
func SetupCommands() [][]string {
	return [][]string{
		{"iptables", "-w", "-N", chainName},
		{"iptables", "-w", "-I", "INPUT", "-j", chainName},
	}
}

// Commands maps an event to the argv list to execute. iface and ports come
// from the schedule (iface already resolved); relaunch may be empty.
func Commands(ev Event, iface, ports, relaunch string) [][]string {
	switch ev.Action {
	case ActionLatency:
		args := []string{"tc", "qdisc", "replace", "dev", iface, "root", "netem"}
		if ev.DelayMS > 0 {
			args = append(args, "delay", fmt.Sprintf("%dms", ev.DelayMS))
			if ev.JitterMS > 0 {
				args = append(args, fmt.Sprintf("%dms", ev.JitterMS))
			}
		}
		if ev.LossPct > 0 {
			args = append(args, "loss", fmt.Sprintf("%g%%", ev.LossPct))
		}
		return [][]string{args}
	case ActionBlackhole:
		return [][]string{
			{"iptables", "-w", "-A", chainName, "-p", "tcp", "--dport", ports, "-j", "DROP"},
		}
	case ActionFreeze:
		return [][]string{{"pkill", "-STOP", "-x", "memcached"}}
	case ActionKill:
		return [][]string{{"pkill", "-KILL", "-x", "memcached"}}
	case ActionHeal:
		cmds := [][]string{
			{"tc", "qdisc", "del", "dev", iface, "root"},
			{"iptables", "-w", "-F", chainName},
			{"pkill", "-CONT", "-x", "memcached"},
		}
		if relaunch != "" {
			cmds = append(cmds, []string{"/bin/bash", "-c", relaunch})
		}
		return cmds
	}
	return nil
}

// HealMayFail reports whether a command's failure is expected during heal:
// `tc qdisc del` fails when no qdisc was installed, pkill -CONT fails when
// nothing is stopped. Those are cleanups of faults that may not have fired.
func HealMayFail(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	return argv[0] == "tc" || argv[0] == "pkill"
}

// Preset names.
const (
	// PresetSweep staggers one fault window per fault type across the run:
	// baseline, freeze, blackhole, latency, kill — each on a (round-robin)
	// server, each healed before the next, with a recovery tail. It is the
	// standard "did the robustness work hold up" run.
	PresetSweep = "sweep"
)

// sweepWindow places one fault window: fault at startFrac, heal at endFrac.
type sweepWindow struct {
	startFrac, endFrac float64
	event              Event
}

// sweepWindows is the fixed timeline of the sweep preset, as fractions of the
// run duration. The first quarter is a clean baseline; everything is healed
// by 90% so recovery is observable in the same run.
var sweepWindows = []sweepWindow{
	{0.25, 0.38, Event{Action: ActionFreeze}},
	{0.42, 0.55, Event{Action: ActionBlackhole}},
	{0.59, 0.72, Event{Action: ActionLatency, DelayMS: 30, JitterMS: 5, LossPct: 1}},
	{0.76, 0.88, Event{Action: ActionKill}},
}

// Preset builds the per-server-VM event lists for a named preset, given the
// number of server VMs and the run duration. Fault windows target servers
// round-robin, so with fewer VMs than windows one VM sees several
// (non-overlapping) faults.
func Preset(name string, serverVMs int, d time.Duration) (map[int][]Event, error) {
	if serverVMs <= 0 {
		return nil, fmt.Errorf("chaos preset needs at least one server VM")
	}
	switch name {
	case PresetSweep:
		events := make(map[int][]Event)
		for i, w := range sweepWindows {
			target := i % serverVMs
			fault := w.event
			fault.AtSecs = int(d.Seconds() * w.startFrac)
			heal := Event{Action: ActionHeal, AtSecs: int(d.Seconds() * w.endFrac)}
			events[target] = append(events[target], fault, heal)
		}
		return events, nil
	default:
		return nil, fmt.Errorf("unknown chaos preset %q (have %q)", name, PresetSweep)
	}
}
