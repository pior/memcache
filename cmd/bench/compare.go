package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
)

// runCompare loads the per-round reports for each side and writes the markdown
// comparison table to stdout. It runs no benchmarks and needs no server.
//
// Each side is a comma-separated list of JSON report paths, one per interleaved
// round, in matching round order: baseline[i] and current[i] were measured back
// to back in the same round, so they share that round's host noise. Comparing
// them paired (per-round ratio) cancels the slow, between-round drift that a
// shared CI runner picks up — the noise an all-baseline-then-all-PR layout would
// otherwise charge to the code.
func runCompare(baselineArg, currentArg string, threshold float64) {
	base, err := loadReports(splitPaths(baselineArg))
	if err != nil {
		log.Fatalf("loading baseline: %v", err)
	}
	cur, err := loadReports(splitPaths(currentArg))
	if err != nil {
		log.Fatalf("loading current: %v", err)
	}
	if len(base) == 0 || len(cur) == 0 {
		log.Fatalf("need at least one baseline and one current report")
	}
	fmt.Print(renderMarkdown(base, cur, threshold))
}

// splitPaths turns a comma-separated path list into a slice, dropping empties so
// a trailing comma or an empty element is harmless.
func splitPaths(arg string) []string {
	var paths []string
	for _, p := range strings.Split(arg, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func loadReports(paths []string) ([]BenchmarkReport, error) {
	reports := make([]BenchmarkReport, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var report BenchmarkReport
		if err := json.Unmarshal(data, &report); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// opComparison is the aggregated paired result for a single operation.
type opComparison struct {
	name     string
	baseOps  float64 // trimmed mean of baseline ops/sec across rounds
	curOps   float64 // trimmed mean of current ops/sec across rounds
	deltaPct float64 // trimmed mean of the per-round PR/main ratio, as a percent change
	sigmaPct float64 // run-to-run scatter (sample stddev) of the per-round delta
	rounds   int     // number of rounds where both sides measured this op
	hasBase  bool    // false for an op present only in the PR (reported as "new")
}

// aggregatePaired pairs the reports round by round (base[i] with cur[i]) and, for
// each operation, derives the central delta from the per-round ratios. Working in
// ratios rather than absolute ops/sec is what cancels the common-mode host noise:
// if a round runs 20% slow, both sides run 20% slow and the ratio is unchanged.
// The trimmed mean then drops the single best and worst round to absorb a lone
// outlier, and the stddev of the per-round deltas records how noisy the run was.
func aggregatePaired(base, cur []BenchmarkReport) []opComparison {
	rounds := min(len(base), len(cur))

	baseByRound := make([]map[string]OpResult, rounds)
	curByRound := make([]map[string]OpResult, rounds)
	for i := range rounds {
		baseByRound[i] = indexResults(base[i])
		curByRound[i] = indexResults(cur[i])
	}

	// Use the last round's result order as the canonical operation list; every
	// round runs the same suite in the same order.
	order := cur[len(cur)-1].Results

	comparisons := make([]opComparison, 0, len(order))
	for _, op := range order {
		var curOps, baseOps, deltas []float64
		for i := range rounds {
			cv, ok := curByRound[i][op.Name]
			if !ok {
				continue
			}
			curOps = append(curOps, cv.OpsPerSec)

			bv, ok := baseByRound[i][op.Name]
			if !ok || bv.OpsPerSec == 0 {
				continue
			}
			baseOps = append(baseOps, bv.OpsPerSec)
			deltas = append(deltas, (cv.OpsPerSec/bv.OpsPerSec-1)*100)
		}

		c := opComparison{name: op.Name, curOps: trimmedMean(curOps)}
		if len(deltas) > 0 {
			c.hasBase = true
			c.baseOps = trimmedMean(baseOps)
			c.deltaPct = trimmedMean(deltas)
			c.sigmaPct = stddev(deltas)
			c.rounds = len(deltas)
		}
		comparisons = append(comparisons, c)
	}
	return comparisons
}

func indexResults(report BenchmarkReport) map[string]OpResult {
	m := make(map[string]OpResult, len(report.Results))
	for _, r := range report.Results {
		m[r.Name] = r
	}
	return m
}

// renderMarkdown produces a PR-comment table comparing the baseline rounds (main)
// against the current rounds (PR). Both sides run on the same runner, interleaved,
// so the paired delta is comparable; positive deltas are faster (more ops/sec).
func renderMarkdown(base, cur []BenchmarkReport, threshold float64) string {
	comparisons := aggregatePaired(base, cur)
	rounds := min(len(base), len(cur))
	head := cur[len(cur)-1]

	var b strings.Builder
	b.WriteString("### Benchmark — PR vs `main`\n\n")
	fmt.Fprintf(&b, "> ⚠️ **A signal, not a verdict.** This is end-to-end throughput against a live "+
		"server on a shared CI runner, so the numbers carry real network and host noise. To fight that, "+
		"the two builds are run **interleaved** — %d rounds, alternating which goes first — and each "+
		"operation's change is the trimmed mean of the **per-round** PR/main ratios, which cancels noise "+
		"common to both sides. The σ column is the run-to-run scatter of that delta; a change is only "+
		"flagged when it clears ±%.0f%% **and** exceeds its own scatter, so a noisy result stays unflagged. "+
		"For deterministic, allocation-level numbers, use the `BenchmarkClient` Go benchmarks instead.\n\n",
		rounds, threshold)
	fmt.Fprintf(&b, "Client `%s`", head.Client)
	if head.Pool != "" {
		fmt.Fprintf(&b, " (pool `%s`)", head.Pool)
	}
	fmt.Fprintf(&b, ", concurrency %d, %s ops/run × %d interleaved rounds.\n\n",
		head.Concurrency, formatNumber(head.Count), rounds)

	b.WriteString("| operation | `main` ops/sec | PR ops/sec | Δ (paired) | σ |\n")
	b.WriteString("|---|---:|---:|---:|---:|\n")

	for _, c := range comparisons {
		if !c.hasBase {
			fmt.Fprintf(&b, "| %s | — | %s | new | — |\n", c.name, formatNumber(int64(c.curOps)))
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | ±%.1f%% |\n",
			c.name,
			formatNumber(int64(c.baseOps)),
			formatNumber(int64(c.curOps)),
			formatDelta(c.deltaPct, c.sigmaPct, threshold),
			c.sigmaPct,
		)
	}

	return b.String()
}

// formatDelta renders the paired percentage change, flagging it only when it
// both reaches the threshold and exceeds its run-to-run scatter (σ). Requiring
// the delta to clear its own scatter is a deliberately conservative significance
// gate: on a noisy runner a large σ means the change is not separable from host
// noise, so it is shown but left unflagged rather than raising a false alarm.
func formatDelta(deltaPct, sigmaPct, threshold float64) string {
	flag := ""
	if math.Abs(deltaPct) >= threshold && math.Abs(deltaPct) > sigmaPct {
		if deltaPct < 0 {
			flag = " ⚠️"
		} else {
			flag = " 🚀"
		}
	}
	return fmt.Sprintf("%+.1f%%%s", deltaPct, flag)
}
