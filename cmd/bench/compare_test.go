package main

import (
	"fmt"
	"strings"
	"testing"
)

// roundsOf builds one single-operation report per round from the given ops/sec
// samples, so tests can describe a side as "this op measured X, Y, Z across
// three rounds".
func roundsOf(name string, opsPerRound ...float64) []BenchmarkReport {
	reports := make([]BenchmarkReport, len(opsPerRound))
	for i, ops := range opsPerRound {
		reports[i] = BenchmarkReport{
			Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000,
			Results: []OpResult{{Name: name, OpsPerSec: ops}},
		}
	}
	return reports
}

func TestAggregatePaired(t *testing.T) {
	t.Run("cancels common-mode host noise", func(t *testing.T) {
		// Each round carries a different host-speed factor, but the PR is a steady
		// 10% faster within every round. The paired ratio must recover that 10%
		// regardless of the per-round scaling, with essentially no scatter — the
		// whole point of comparing paired rather than absolute.
		factors := []float64{1.0, 0.5, 2.0, 0.8, 1.2}
		base := make([]BenchmarkReport, len(factors))
		cur := make([]BenchmarkReport, len(factors))
		for i, f := range factors {
			base[i] = roundsOf("get-hit", f*100_000)[0]
			cur[i] = roundsOf("get-hit", f*110_000)[0]
		}

		got := aggregatePaired(base, cur)
		if len(got) != 1 {
			t.Fatalf("expected 1 comparison, got %d", len(got))
		}
		c := got[0]
		if s := fmt.Sprintf("%.1f", c.deltaPct); s != "10.0" {
			t.Errorf("deltaPct = %s, want 10.0", s)
		}
		if c.sigmaPct > 0.001 {
			t.Errorf("sigmaPct = %f, want ~0 (PR is steady 10%% across all rounds)", c.sigmaPct)
		}
	})

	t.Run("trims a single outlier round", func(t *testing.T) {
		// Four rounds at a clean +10% and one wildly noisy round; the trimmed mean
		// of the per-round deltas drops the best and worst, so the outlier round
		// cannot drag the reported delta.
		base := roundsOf("set", 100_000, 100_000, 100_000, 100_000, 100_000)
		cur := roundsOf("set", 110_000, 110_000, 110_000, 110_000, 500_000)

		got := aggregatePaired(base, cur)
		if s := fmt.Sprintf("%.1f", got[0].deltaPct); s != "10.0" {
			t.Errorf("deltaPct = %s, want 10.0 (outlier round should be trimmed)", s)
		}
	})

	t.Run("op only in PR is reported as new", func(t *testing.T) {
		base := roundsOf("get-hit", 100_000)
		cur := []BenchmarkReport{{
			Results: []OpResult{
				{Name: "get-hit", OpsPerSec: 110_000},
				{Name: "increment", OpsPerSec: 50_000},
			},
		}}

		got := aggregatePaired(base, cur)
		byName := map[string]opComparison{}
		for _, c := range got {
			byName[c.name] = c
		}
		if byName["increment"].hasBase {
			t.Errorf("increment should have no baseline (new op)")
		}
		if !byName["get-hit"].hasBase {
			t.Errorf("get-hit should have a baseline")
		}
	})

	t.Run("pairs only the overlapping rounds", func(t *testing.T) {
		base := roundsOf("set", 100_000, 100_000, 100_000)
		cur := roundsOf("set", 110_000, 110_000) // one fewer round

		got := aggregatePaired(base, cur)
		if got[0].rounds != 2 {
			t.Errorf("rounds = %d, want 2 (min of the two sides)", got[0].rounds)
		}
	})
}

func TestFormatDelta(t *testing.T) {
	tests := []struct {
		name              string
		delta, sigma, thr float64
		want              string
	}{
		{"small change unflagged", 5, 1, 10, "+5.0%"},
		{"small regression unflagged", -5, 1, 10, "-5.0%"},
		{"clean regression flagged", -20, 3, 10, "-20.0% ⚠️"},
		{"clean improvement flagged", 30, 5, 10, "+30.0% 🚀"},
		{"at threshold, low noise, flagged", -10, 2, 10, "-10.0% ⚠️"},
		{"big but noisy delta stays unflagged", 30, 40, 10, "+30.0%"},
		{"delta equal to sigma stays unflagged", 20, 20, 10, "+20.0%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDelta(tt.delta, tt.sigma, tt.thr)
			if got != tt.want {
				t.Errorf("formatDelta(%v, %v, %v) = %q, want %q", tt.delta, tt.sigma, tt.thr, got, tt.want)
			}
		})
	}
}

func TestRenderMarkdown(t *testing.T) {
	// get-hit: clean +10% across three rounds → flagged.
	// set: a large but noisy swing → shown, not flagged.
	// increment: present only in the PR → new.
	base := []BenchmarkReport{
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 100_000}, {Name: "set", OpsPerSec: 100_000}}},
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 100_000}, {Name: "set", OpsPerSec: 100_000}}},
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 100_000}, {Name: "set", OpsPerSec: 100_000}}},
	}
	cur := []BenchmarkReport{
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 110_000}, {Name: "set", OpsPerSec: 60_000}, {Name: "increment", OpsPerSec: 50_000}}},
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 110_000}, {Name: "set", OpsPerSec: 160_000}, {Name: "increment", OpsPerSec: 50_000}}},
		{Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Results: []OpResult{{Name: "get-hit", OpsPerSec: 110_000}, {Name: "set", OpsPerSec: 100_000}, {Name: "increment", OpsPerSec: 50_000}}},
	}

	md := renderMarkdown(base, cur, 10)

	wants := []string{
		"### Benchmark — PR vs `main`",
		"A signal, not a verdict",
		"interleaved",
		"× 3 interleaved rounds",
		"Client `pior` (pool `puddle`)",
		"| operation | `main` ops/sec | PR ops/sec | Δ (paired) | σ |",
		"| get-hit | 100.00K | 110.00K | +10.0% 🚀 | ±0.0% |",
		"| increment | — | 50.00K | new | — |",
	}
	for _, want := range wants {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n--- got ---\n%s", want, md)
		}
	}

	// The noisy `set` row must appear with a delta but no 🚀/⚠️ flag.
	if !strings.Contains(md, "| set | 100.00K |") {
		t.Errorf("set row missing\n--- got ---\n%s", md)
	}
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "| set |") && (strings.Contains(line, "🚀") || strings.Contains(line, "⚠️")) {
			t.Errorf("noisy set row should not be flagged: %q", line)
		}
	}
}
