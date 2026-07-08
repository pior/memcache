package main

import (
	"context"
	"math/rand/v2"
	"time"
)

// sleepCtx sleeps for d unless ctx is cancelled first; returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type sched struct {
	baseline, churn, settle, flap, massfreeze, churnflap, final time.Duration
	freezeHold, outage                                          time.Duration
}

func fullSchedule() sched {
	return sched{
		baseline: 60 * time.Second, churn: 300 * time.Second, settle: 60 * time.Second,
		flap: 300 * time.Second, massfreeze: 150 * time.Second, churnflap: 300 * time.Second,
		final: 90 * time.Second, freezeHold: 60 * time.Second, outage: 150 * time.Second,
	}
}

func quickSchedule() sched {
	return sched{
		baseline: 20 * time.Second, churn: 60 * time.Second, settle: 20 * time.Second,
		flap: 60 * time.Second, massfreeze: 70 * time.Second, churnflap: 60 * time.Second,
		final: 30 * time.Second, freezeHold: 25 * time.Second, outage: 60 * time.Second,
	}
}

// runSchedule executes the phased chaos program against the fleet. It returns
// when the whole program has run or ctx is cancelled.
func runSchedule(ctx context.Context, nodes []node, ms *mutableServers, allAddrs []string, quick bool) {
	s := fullSchedule()
	if quick {
		s = quickSchedule()
	}
	rng := rand.New(rand.NewPCG(42, 1))

	// PHASE baseline — full healthy fleet, establish footprint.
	setPhase("baseline")
	ms.set(allAddrs)
	if !sleepCtx(ctx, s.baseline) {
		return
	}

	// PHASE churn — oscillate membership (containers stay up). Exercises pool
	// reaping, the 2-pass grace, and rendezvous stability. Active set stays
	// between ~70% and 100% of the fleet.
	setPhase("churn")
	churnUntil(ctx, time.Now().Add(s.churn), ms, allAddrs, rng)

	// PHASE settle — full set restored; reaped pools + goroutines should return.
	setPhase("settle-after-churn")
	ms.set(allAddrs)
	if !sleepCtx(ctx, s.settle) {
		return
	}

	// PHASE flap — a subset toggles faster than the breaker open timeout.
	setPhase("flap")
	flapUntil(ctx, time.Now().Add(s.flap), nodes)

	setPhase("settle-after-flap")
	restoreAll(nodes)
	ms.set(allAddrs)
	if !sleepCtx(ctx, s.settle) {
		return
	}

	// PHASE mass-freeze — SIGSTOP half the fleet at once for several health
	// intervals, stressing the concurrent reaper pass, then thaw them
	// together (half-open thundering herd).
	setPhase("mass-freeze")
	frozen := make([]string, 0, len(nodes)/2)
	for i := 0; i < len(nodes)/2; i++ {
		frozen = append(frozen, nodes[i].name)
	}
	dockerDo("pause", frozen...)
	sleepCtx(ctx, s.freezeHold)
	setPhase("mass-thaw")
	dockerDo("unpause", frozen...)
	if !sleepCtx(ctx, s.massfreeze-s.freezeHold) {
		return
	}

	setPhase("settle-after-freeze")
	ms.set(allAddrs)
	if !sleepCtx(ctx, s.settle) {
		return
	}

	// PHASE total-outage (S2) — every server down for minutes under load, then
	// heal. Assert goroutines/heap/fds stay bounded while all-down (no per-
	// failed-op leak) and everything recovers.
	setPhase("total-outage")
	allNames := namesOf(nodes)
	dockerDo("pause", allNames...)
	hold := *outageHold
	if hold > s.outage {
		hold = s.outage
	}
	sleepCtx(ctx, hold)
	setPhase("total-recovery")
	dockerDo("unpause", allNames...)
	if !sleepCtx(ctx, s.outage-hold) {
		return
	}

	setPhase("settle-after-outage")
	ms.set(allAddrs)
	if !sleepCtx(ctx, s.settle) {
		return
	}

	// PHASE churn+flap — churn membership WHILE a subset flaps/dies, so a pool
	// can be reaped while its breaker is open and ops are in flight.
	setPhase("churn+flap")
	deadline := time.Now().Add(s.churnflap)
	go flapUntil(ctx, deadline, nodes[:8])
	churnUntil(ctx, deadline, ms, allAddrs, rng)

	// PHASE final — everything healthy, full set. Confirm full recovery.
	setPhase("final-baseline")
	restoreAll(nodes)
	ms.set(allAddrs)
	sleepCtx(ctx, s.final)
}

// churnUntil oscillates the active server set until the deadline.
func churnUntil(ctx context.Context, deadline time.Time, ms *mutableServers, allAddrs []string, rng *rand.Rand) {
	for time.Now().Before(deadline) {
		// keep a random 70-100% subset live
		keep := len(allAddrs)*7/10 + rng.IntN(len(allAddrs)*3/10+1)
		perm := rng.Perm(len(allAddrs))
		active := make([]string, 0, keep)
		for i := 0; i < keep; i++ {
			active = append(active, allAddrs[perm[i]])
		}
		ms.set(active)
		if !sleepCtx(ctx, 4*time.Second) {
			return
		}
	}
}

// flapUntil toggles the first up-to-8 nodes faster than a typical breaker open
// timeout, mixing pause/unpause (hung) with kill/start (crash+restart).
func flapUntil(ctx context.Context, deadline time.Time, nodes []node) {
	n := nodes
	if len(n) > 8 {
		n = n[:8]
	}
	round := 0
	for time.Now().Before(deadline) {
		round++
		if round%2 == 1 {
			// hung flap: freeze ~1.3s (below a 2s open), then thaw
			names := namesOf(n[:len(n)/2])
			dockerDo("pause", names...)
			if !sleepCtx(ctx, 1300*time.Millisecond) {
				dockerDo("unpause", names...)
				return
			}
			dockerDo("unpause", names...)
		} else {
			// crash flap: kill and restart the other half
			names := namesOf(n[len(n)/2:])
			dockerDo("kill", names...)
			if !sleepCtx(ctx, 1500*time.Millisecond) {
				dockerDo("start", names...)
				return
			}
			dockerDo("start", names...)
		}
		if !sleepCtx(ctx, 1500*time.Millisecond) {
			return
		}
	}
}

func namesOf(nodes []node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.name
	}
	return out
}

func restoreAll(nodes []node) {
	names := namesOf(nodes)
	dockerDo("unpause", names...)
	dockerDo("start", names...)
}
