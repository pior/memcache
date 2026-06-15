package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// runCompare loads two JSON reports and writes the markdown comparison table to
// stdout. It runs no benchmarks and needs no server.
func runCompare(baselinePath, currentPath string, threshold float64) {
	base, err := loadReport(baselinePath)
	if err != nil {
		log.Fatalf("loading baseline: %v", err)
	}
	cur, err := loadReport(currentPath)
	if err != nil {
		log.Fatalf("loading current: %v", err)
	}
	fmt.Print(renderMarkdown(base, cur, threshold))
}

func loadReport(path string) (BenchmarkReport, error) {
	var report BenchmarkReport
	data, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return report, fmt.Errorf("parsing %s: %w", path, err)
	}
	return report, nil
}

// renderMarkdown produces a PR-comment table comparing a baseline run (main)
// against the current run (PR). Both must come from the same runner for the
// absolute ops/sec to be comparable. Deltas at or beyond threshold percent are
// flagged; positive deltas are faster (more ops/sec).
func renderMarkdown(base, cur BenchmarkReport, threshold float64) string {
	baseByName := make(map[string]OpResult, len(base.Results))
	for _, r := range base.Results {
		baseByName[r.Name] = r
	}

	var b strings.Builder
	b.WriteString("### Speed benchmark — PR vs `main`\n\n")
	fmt.Fprintf(&b, "Server-based throughput, trimmed mean of %d runs on the same runner. "+
		"Absolute ops/sec; host noise makes small deltas unreliable, so only changes beyond ±%.0f%% are flagged.\n\n",
		cur.Runs, threshold)
	fmt.Fprintf(&b, "Client `%s`", cur.Client)
	if cur.Pool != "" {
		fmt.Fprintf(&b, " (pool `%s`)", cur.Pool)
	}
	fmt.Fprintf(&b, ", concurrency %d, %s ops/run.\n\n", cur.Concurrency, formatNumber(cur.Count))

	b.WriteString("| operation | `main` ops/sec | PR ops/sec | Δ |\n")
	b.WriteString("|---|---:|---:|---:|\n")

	for _, c := range cur.Results {
		baseRes, ok := baseByName[c.Name]
		if !ok {
			fmt.Fprintf(&b, "| %s | — | %s | new |\n", c.Name, formatNumber(int64(c.OpsPerSec)))
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			c.Name,
			formatNumber(int64(baseRes.OpsPerSec)),
			formatNumber(int64(c.OpsPerSec)),
			formatDelta(baseRes.OpsPerSec, c.OpsPerSec, threshold),
		)
	}

	return b.String()
}

// formatDelta renders the percentage change from base to cur, flagging changes
// that reach the threshold.
func formatDelta(base, cur, threshold float64) string {
	if base == 0 {
		return "—"
	}
	pct := (cur - base) / base * 100
	flag := ""
	if pct <= -threshold {
		flag = " ⚠️"
	} else if pct >= threshold {
		flag = " 🚀"
	}
	return fmt.Sprintf("%+.1f%%%s", pct, flag)
}
