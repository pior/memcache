package chaos

import (
	"strings"
	"testing"
	"time"
)

func TestPresetSweep(t *testing.T) {
	events, err := Preset(PresetSweep, 3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Four fault windows round-robin over 3 servers: server 0 gets freeze and
	// (wrapping) kill, server 1 blackhole, server 2 latency — each with a heal.
	wantActions := map[int][]Action{
		0: {ActionFreeze, ActionHeal, ActionKill, ActionHeal},
		1: {ActionBlackhole, ActionHeal},
		2: {ActionLatency, ActionHeal},
	}
	for idx, want := range wantActions {
		got := events[idx]
		if len(got) != len(want) {
			t.Fatalf("server %d: %d events, want %d (%+v)", idx, len(got), len(want), got)
		}
		for i, action := range want {
			if got[i].Action != action {
				t.Errorf("server %d event %d = %s, want %s", idx, i, got[i].Action, action)
			}
		}
	}

	// The first window must start after the baseline quarter, and every fault
	// must heal before 90% of the run so recovery is observable.
	for idx, evs := range events {
		for _, ev := range evs {
			if ev.AtSecs < int(time.Hour.Seconds()/4) {
				t.Errorf("server %d: event %s at %ds is inside the baseline quarter", idx, ev.Action, ev.AtSecs)
			}
			if ev.AtSecs > int(time.Hour.Seconds()*0.9) {
				t.Errorf("server %d: event %s at %ds leaves no recovery tail", idx, ev.Action, ev.AtSecs)
			}
		}
	}
}

func TestPresetSweep_SingleServer(t *testing.T) {
	events, err := Preset(PresetSweep, 1, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evs := events[0]
	if len(evs) != 8 { // all four windows on the only server
		t.Fatalf("got %d events, want 8: %+v", len(evs), evs)
	}
	// Windows must not overlap: sorted by design, strictly increasing.
	for i := 1; i < len(evs); i++ {
		if evs[i].AtSecs <= evs[i-1].AtSecs {
			t.Errorf("events overlap: %+v then %+v", evs[i-1], evs[i])
		}
	}
}

func TestPresetUnknown(t *testing.T) {
	if _, err := Preset("nope", 3, time.Hour); err == nil {
		t.Error("unknown preset must error")
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	s := Schedule{
		Ports:    "11211:11212",
		Relaunch: "restart-memcached",
		Events: []Event{
			{AtSecs: 60, Action: ActionLatency, DelayMS: 30, JitterMS: 5, LossPct: 1},
			{AtSecs: 30, Action: ActionFreeze},
		},
	}
	parsed, err := ParseSchedule(s.JSON())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Ports != s.Ports || parsed.Relaunch != s.Relaunch {
		t.Errorf("round trip lost fields: %+v", parsed)
	}
	// ParseSchedule sorts by offset.
	if parsed.Events[0].Action != ActionFreeze || parsed.Events[1].Action != ActionLatency {
		t.Errorf("events not sorted by offset: %+v", parsed.Events)
	}
}

func TestCommands(t *testing.T) {
	cases := map[string]struct {
		event Event
		want  []string // rendered "argv0 argv1 ..." lines, in order
	}{
		"latency": {
			Event{Action: ActionLatency, DelayMS: 30, JitterMS: 5, LossPct: 1},
			[]string{"tc qdisc replace dev eth0 root netem delay 30ms 5ms loss 1%"},
		},
		"latency delay only": {
			Event{Action: ActionLatency, DelayMS: 100},
			[]string{"tc qdisc replace dev eth0 root netem delay 100ms"},
		},
		"loss only": {
			Event{Action: ActionLatency, LossPct: 2.5},
			[]string{"tc qdisc replace dev eth0 root netem loss 2.5%"},
		},
		"blackhole": {
			Event{Action: ActionBlackhole},
			[]string{"iptables -w -A MCLT-CHAOS -p tcp --dport 11211:11212 -j DROP"},
		},
		"freeze": {
			Event{Action: ActionFreeze},
			[]string{"pkill -STOP -x memcached"},
		},
		"kill": {
			Event{Action: ActionKill},
			[]string{"pkill -KILL -x memcached"},
		},
		"heal": {
			Event{Action: ActionHeal},
			[]string{
				"tc qdisc del dev eth0 root",
				"iptables -w -F MCLT-CHAOS",
				"pkill -CONT -x memcached",
				"/bin/bash -c relaunch-snippet",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cmds := Commands(tc.event, "eth0", "11211:11212", "relaunch-snippet")
			if len(cmds) != len(tc.want) {
				t.Fatalf("%d commands, want %d: %v", len(cmds), len(tc.want), cmds)
			}
			for i, argv := range cmds {
				if got := strings.Join(argv, " "); got != tc.want[i] {
					t.Errorf("command %d:\n got  %s\n want %s", i, got, tc.want[i])
				}
			}
		})
	}
}

func TestHealWithoutRelaunch(t *testing.T) {
	cmds := Commands(Event{Action: ActionHeal}, "eth0", "11211", "")
	if len(cmds) != 3 {
		t.Fatalf("heal without relaunch must have 3 commands, got %v", cmds)
	}
}

func TestPortSpec(t *testing.T) {
	if got := PortSpec(11211, 1); got != "11211" {
		t.Errorf("PortSpec(11211, 1) = %q", got)
	}
	if got := PortSpec(11211, 3); got != "11211:11213" {
		t.Errorf("PortSpec(11211, 3) = %q", got)
	}
}
