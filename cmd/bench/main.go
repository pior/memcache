package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/pior/memcache"
)

type Test struct {
	Name       string
	ItemsPerOp int // Number of items processed per operation (1 for single ops, 10 for batch-10, etc.)
	Operation  OperationFunc
}

type OperationFunc func(ctx context.Context, client Client, batchCmd *memcache.BatchCommands, uid int64, workerID int, operationID int64) error

type Result struct {
	name        string
	count       int64
	itemsPerOp  int
	duration    time.Duration
	opsPerSec   float64
	itemsPerSec float64
	avgLatency  time.Duration
}

type Config struct {
	addr        string
	pool        string
	bradfitz    bool
	concurrency int
	count       int64
	only        string
	runs        int
}

// info writes progress and diagnostics to stderr so that stdout carries only
// the result (the summary table in text mode, or the JSON report).
func info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func main() {
	config := Config{}
	flag.StringVar(&config.addr, "addr", "127.0.0.1:11211", "memcache server address")
	flag.BoolVar(&config.bradfitz, "bradfitz", false, "use bradfitz client implementation (default is pior)")
	flag.StringVar(&config.pool, "pool", "puddle", "pool implementation for pior client: channel or puddle")
	flag.IntVar(&config.concurrency, "concurrency", 1, "number of concurrent workers")
	flag.Int64Var(&config.count, "count", 1_000_000, "target operation count")
	flag.StringVar(&config.only, "only", "", "run only the specified operation (e.g., 'Set')")
	flag.IntVar(&config.runs, "runs", 1, "repeat the suite N times; reported numbers are a trimmed mean (drop fastest+slowest)")

	var (
		format    string
		baseline  string
		compare   string
		threshold float64
	)
	flag.StringVar(&format, "format", "text", "output format: text or json")
	flag.StringVar(&baseline, "baseline", "", "compare mode: path to the baseline (main) JSON report; requires -compare")
	flag.StringVar(&compare, "compare", "", "compare mode: path to the current (PR) JSON report; requires -baseline")
	flag.Float64Var(&threshold, "threshold", 10, "compare mode: percent change to flag in the comparison table")
	flag.Parse()

	// Compare mode reads two JSON reports and emits a markdown table. It runs no
	// benchmarks and needs no server.
	if baseline != "" || compare != "" {
		if baseline == "" || compare == "" {
			log.Fatalf("-baseline and -compare must be provided together")
		}
		runCompare(baseline, compare, threshold)
		return
	}

	if config.runs < 1 {
		log.Fatalf("-runs must be >= 1")
	}
	if format != "text" && format != "json" {
		log.Fatalf("invalid -format: %s (must be 'text' or 'json')", format)
	}
	if config.pool != "channel" && config.pool != "puddle" {
		log.Fatalf("Invalid pool: %s (must be 'channel' or 'puddle')", config.pool)
	}

	clientName := "pior"
	if config.bradfitz {
		clientName = "bradfitz"
	}

	info("Memcache Speed Test\n")
	info("===================\n")
	info("Client:      %s\n", clientName)
	if !config.bradfitz {
		info("Pool:        %s\n", config.pool)
	}
	info("Server:      %s\n", config.addr)
	info("Concurrency: %d\n", config.concurrency)
	info("Runs:        %d\n", config.runs)
	info("Target:      %s operations\n\n", formatNumber(config.count))

	client, batchCmd := createClient(config)
	defer client.Close()

	ctx := context.Background()

	// Verify server is reachable before starting benchmarks.
	preflightUID := rand.Int64N(1_000_000)
	info("Verifying connection to %s...\n", config.addr)

	testKey := fmt.Sprintf("test-%d-preflight", preflightUID)
	err := client.Set(ctx, memcache.Item{Key: testKey, Value: []byte(testKey), TTL: memcache.ExpiresIn(1 * time.Second)})
	if err != nil {
		log.Fatalf("Failed to set a test key to memcache server: %v\n", err)
	}
	item, err := client.Get(ctx, testKey)
	if err != nil {
		log.Fatalf("Failed to get the test key from memcache server: %v\n", err)
	}
	if !item.Found || string(item.Value) != testKey {
		log.Fatalf("Test key value mismatch: expected %q, got %q\n", testKey, item.Value)
	}
	info("Connection verified!\n\n")

	// Each run uses a distinct UID so repeated runs don't interfere: the suite
	// has inter-test key dependencies (get-hit reads what set wrote, get-miss
	// must not see them), so the UID for a given run index is shared across all
	// tests of that run.
	runUIDs := make([]int64, config.runs)
	for r := range runUIDs {
		runUIDs[r] = rand.Int64N(1_000_000_000)
	}

	tests := benchmarkTests()

	report := BenchmarkReport{
		Client:      clientName,
		Server:      config.addr,
		Concurrency: config.concurrency,
		Count:       config.count,
		Runs:        config.runs,
	}
	if !config.bradfitz {
		report.Pool = config.pool
	}

	for _, test := range tests {
		if config.only != "" && test.Name != config.only {
			continue
		}

		info("Running: %s\n", test.Name)
		res := runAggregated(ctx, client, batchCmd, config, runUIDs, test)
		info("  %s ops/sec, %s items/sec, %s avg latency\n",
			formatNumber(int64(res.OpsPerSec)),
			formatNumber(int64(res.ItemsPerSec)),
			formatDuration(time.Duration(res.AvgLatencyNs)),
		)
		report.Results = append(report.Results, res)
	}

	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			log.Fatalf("encoding report: %v", err)
		}
	default:
		printTextSummary(report)
	}

	printPiorClientStats(client)
}

func printTextSummary(report BenchmarkReport) {
	fmt.Printf("\n")
	fmt.Printf("%-20s %12s %12s %12s %12s\n", "Operation", "Count", "Ops/sec", "Items/sec", "Avg Latency")
	for _, result := range report.Results {
		fmt.Printf("%-20s %12s %12s %12s %12s\n",
			result.Name,
			formatNumber(report.Count),
			formatNumber(int64(result.OpsPerSec)),
			formatNumber(int64(result.ItemsPerSec)),
			formatDuration(time.Duration(result.AvgLatencyNs)),
		)
	}
}

// runAggregated runs a test once per configured run and aggregates the
// per-run throughput with a trimmed mean to damp host noise.
func runAggregated(
	ctx context.Context,
	client Client,
	batchCmd *memcache.BatchCommands,
	config Config,
	runUIDs []int64,
	test Test,
) OpResult {
	opsSamples := make([]float64, len(runUIDs))
	itemsSamples := make([]float64, len(runUIDs))
	latencySamples := make([]float64, len(runUIDs))

	for r, uid := range runUIDs {
		res := runBenchmark(ctx, client, batchCmd, config, uid, test)
		opsSamples[r] = res.opsPerSec
		itemsSamples[r] = res.itemsPerSec
		latencySamples[r] = float64(res.avgLatency)
	}

	return OpResult{
		Name:         test.Name,
		ItemsPerOp:   test.ItemsPerOp,
		OpsPerSec:    trimmedMean(opsSamples),
		ItemsPerSec:  trimmedMean(itemsSamples),
		AvgLatencyNs: int64(trimmedMean(latencySamples)),
	}
}

// runBenchmark is a generic benchmark runner that executes an operation function
func runBenchmark(
	ctx context.Context,
	client Client,
	batchCmd *memcache.BatchCommands,
	config Config,
	uid int64,
	test Test,
) Result {
	var wg sync.WaitGroup

	opsPerWorker := config.count / int64(config.concurrency)
	start := time.Now()

	for i := range config.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := range opsPerWorker {
				if err := test.Operation(ctx, client, batchCmd, uid, workerID, j); err != nil {
					log.Fatalf("Operation %s failed: %v\n", test.Name, err)
				}
			}
		}(i)
	}

	wg.Wait()

	duration := time.Since(start)
	opsPerSec := float64(config.count) / duration.Seconds()
	totalItems := config.count * int64(test.ItemsPerOp)
	itemsPerSec := float64(totalItems) / duration.Seconds()

	return Result{
		name:        test.Name,
		count:       config.count,
		itemsPerOp:  test.ItemsPerOp,
		duration:    duration,
		opsPerSec:   opsPerSec,
		itemsPerSec: itemsPerSec,
		avgLatency:  duration / time.Duration(opsPerWorker),
	}
}

func formatNumber(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	} else if n >= 1_000 {
		return fmt.Sprintf("%.2fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	} else if d >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fµs", float64(d.Nanoseconds())/1000)
}

func printPiorClientStats(client Client) {
	piorCli, ok := client.(*memcache.Client)
	if !ok {
		return
	}

	allPoolStats := piorCli.PoolMetrics()

	info("\n")
	info("Pool Statistics\n")
	info("===============\n")
	for _, serverStats := range allPoolStats {
		poolStats := serverStats.Metrics
		info("\nServer: %s\n", serverStats.Addr)
		info("Connections:\n")
		info("  Total:    %d\n", poolStats.TotalConns)
		info("  Active:   %d\n", poolStats.ActiveConns)
		info("  Idle:     %d\n", poolStats.IdleConns)
		info("  Created:  %s\n", formatNumber(int64(poolStats.CreatedConns)))
		info("  Destroyed: %s\n", formatNumber(int64(poolStats.DestroyedConns)))

		info("\nAcquire Performance:\n")
		info("  Total:    %s\n", formatNumber(int64(poolStats.AcquireCount)))
		if poolStats.AcquireWaitCount > 0 {
			waitPct := float64(poolStats.AcquireWaitCount) / float64(poolStats.AcquireCount) * 100
			avgWait := time.Duration(poolStats.AcquireWaitTimeNs / poolStats.AcquireWaitCount)
			info("  Waited:   %s (%.1f%%, avg %s)\n",
				formatNumber(int64(poolStats.AcquireWaitCount)),
				waitPct,
				formatDuration(avgWait))
		}
		if poolStats.AcquireErrors > 0 {
			info("  Errors:   %s\n", formatNumber(int64(poolStats.AcquireErrors)))
		}
	}
}
