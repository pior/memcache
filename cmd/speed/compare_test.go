package main

import (
	"strings"
	"testing"
)

func TestFormatDelta(t *testing.T) {
	tests := []struct {
		name      string
		base, cur float64
		threshold float64
		want      string
	}{
		{"no baseline", 0, 100, 10, "—"},
		{"small change unflagged", 100, 105, 10, "+5.0%"},
		{"small regression unflagged", 100, 95, 10, "-5.0%"},
		{"regression flagged", 100, 80, 10, "-20.0% ⚠️"},
		{"improvement flagged", 100, 130, 10, "+30.0% 🚀"},
		{"exactly at threshold flagged", 100, 90, 10, "-10.0% ⚠️"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDelta(tt.base, tt.cur, tt.threshold)
			if got != tt.want {
				t.Errorf("formatDelta(%v, %v, %v) = %q, want %q", tt.base, tt.cur, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestRenderMarkdown(t *testing.T) {
	base := BenchmarkReport{
		Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Runs: 7,
		Results: []OpResult{
			{Name: "get-hit", OpsPerSec: 100_000},
			{Name: "set", OpsPerSec: 90_000},
		},
	}
	cur := BenchmarkReport{
		Client: "pior", Pool: "puddle", Concurrency: 8, Count: 200_000, Runs: 7,
		Results: []OpResult{
			{Name: "get-hit", OpsPerSec: 130_000}, // improvement, flagged
			{Name: "set", OpsPerSec: 88_000},      // small change, unflagged
			{Name: "increment", OpsPerSec: 50_000}, // not in baseline
		},
	}

	md := renderMarkdown(base, cur, 10)

	wants := []string{
		"### Speed benchmark — PR vs `main`",
		"trimmed mean of 7 runs",
		"Client `pior` (pool `puddle`)",
		"| get-hit | 100.00K | 130.00K | +30.0% 🚀 |",
		"| set | 90.00K | 88.00K | -2.2% |",
		"| increment | — | 50.00K | new |",
	}
	for _, want := range wants {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n--- got ---\n%s", want, md)
		}
	}
}
