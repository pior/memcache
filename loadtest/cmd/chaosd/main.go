// Command chaosd executes a chaos schedule on a server VM: at each event's
// offset from process start it applies (or heals) a fault — netem latency,
// iptables blackhole, SIGSTOP freeze, SIGKILL — against the local memcached
// instances. It is started by the server VM's startup-script alongside
// memcached; there is no SSH path to the VMs, so the schedule arrives as a
// JSON file rendered by the orchestrator and downloaded from GCS.
//
// Every action is appended to a JSONL log (uploaded to GCS on the server's
// upload timer), so fault windows can be correlated with the clients' latency
// and error timelines during analysis.
//
// On SIGTERM/SIGINT — or after the last event — chaosd heals everything
// before exiting, so a torn-down or completed run never leaves a VM faulted.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pior/memcache/loadtest/internal/chaos"
)

func main() {
	var (
		schedulePath = flag.String("schedule", "", "chaos schedule JSON file (required)")
		logPath      = flag.String("log", "", "append actions to this JSONL file (default stderr only)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *schedulePath == "" {
		log.Error("chaosd: -schedule is required")
		os.Exit(2)
	}

	data, err := os.ReadFile(*schedulePath)
	if err != nil {
		log.Error("chaosd: read schedule", "err", err)
		os.Exit(1)
	}
	sched, err := chaos.ParseSchedule(data)
	if err != nil {
		log.Error("chaosd: parse schedule", "err", err)
		os.Exit(1)
	}

	iface := sched.Iface
	if iface == "" {
		iface, err = defaultRouteIface()
		if err != nil {
			log.Error("chaosd: detect interface", "err", err)
			os.Exit(1)
		}
	}

	actions := &actionLog{log: log}
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Error("chaosd: open action log", "err", err)
			os.Exit(1)
		}
		defer f.Close()
		actions.file = f
	}

	log.Info("chaosd starting", "iface", iface, "ports", sched.Ports, "events", len(sched.Events))
	for _, cmd := range chaos.SetupCommands() {
		actions.run("setup", cmd, true)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	for _, ev := range sched.Events {
		wait := time.Duration(ev.AtSecs)*time.Second - time.Since(start)
		if wait > 0 {
			select {
			case <-ctx.Done():
				healAll(actions, iface, sched)
				return
			case <-time.After(wait):
			}
		}
		log.Info("chaos event", "at_secs", ev.AtSecs, "action", ev.Action)
		for _, cmd := range chaos.Commands(ev, iface, sched.Ports, sched.Relaunch) {
			actions.run(string(ev.Action), cmd, ev.Action == chaos.ActionHeal && chaos.HealMayFail(cmd))
		}
	}

	// The run is over (or the schedule was empty): leave the VM healed no
	// matter how the timeline ended.
	healAll(actions, iface, sched)
	log.Info("chaosd done")
}

// healAll applies a final heal so no fault outlives the schedule.
func healAll(actions *actionLog, iface string, sched chaos.Schedule) {
	for _, cmd := range chaos.Commands(chaos.Event{Action: chaos.ActionHeal}, iface, sched.Ports, sched.Relaunch) {
		actions.run("final-heal", cmd, chaos.HealMayFail(cmd))
	}
}

// actionLog executes commands and records each one as a JSONL entry.
type actionLog struct {
	log  *slog.Logger
	file *os.File
}

// actionRecord is one executed command in the JSONL log.
type actionRecord struct {
	Time   time.Time `json:"t"`
	Action string    `json:"action"`
	Cmd    string    `json:"cmd"`
	Output string    `json:"output,omitempty"`
	Err    string    `json:"err,omitempty"`
}

// run executes argv. mayFail suppresses the error log for commands whose
// failure is expected (healing a fault that never fired, re-creating an
// existing iptables chain); the outcome is recorded either way.
func (a *actionLog) run(action string, argv []string, mayFail bool) {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	rec := actionRecord{Time: time.Now().UTC(), Action: action, Cmd: strings.Join(argv, " "), Output: strings.TrimSpace(string(out))}
	if err != nil {
		rec.Err = err.Error()
		if !mayFail {
			a.log.Error("chaos command failed", "cmd", rec.Cmd, "output", rec.Output, "err", err)
		}
	}
	if a.file != nil {
		if data, jerr := json.Marshal(rec); jerr == nil {
			fmt.Fprintln(a.file, string(data))
		}
	}
}

// defaultRouteIface returns the NIC of the default route (the interface all
// client traffic arrives on), via `ip route show default`.
func defaultRouteIface() (string, error) {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return "", fmt.Errorf("ip route show default: %w", err)
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("no default route interface in %q", strings.TrimSpace(string(out)))
}
