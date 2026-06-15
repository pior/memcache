# Memcache Speed Test

A pure speed testing tool for measuring maximum memcache throughput without validation overhead. Designed to benchmark raw command performance.

## Building

```bash
go build
```

## Usage

```bash
./speed [options]
```

### Options

- `-addr string` - Memcache server address (default: "127.0.0.1:11211")
- `-concurrency int` - Number of concurrent workers (default: 1)
- `-count int` - Target operation count (default: 1,000,000)
- `-runs int` - Repeat the suite N times; reported numbers are a trimmed mean, dropping the fastest and slowest run (default: 1)
- `-format string` - Output format: `text` (default) or `json`
- `-bradfitz` - Benchmark the `bradfitz/gomemcache` client instead of this one
- `-pool string` - Pool implementation for this client: `puddle` (default) or `channel`
- `-only string` - Run a single operation (e.g. `-only set`)

In `json` mode, progress and pool statistics go to stderr so stdout carries only the JSON report — redirect it with `> report.json`.

### Examples

**Single-threaded 1M operations:**
```bash
./speed
```

**Multi-threaded with 8 workers:**
```bash
./speed -concurrency 8
```

**Quick test with 100K operations:**
```bash
./speed -count 100000 -concurrency 4
```

**Target specific server:**
```bash
./speed -addr 192.168.1.100:11211 -concurrency 16
```

## Test Sequence

Each run uses a unique ID to ensure keys don't conflict with previous runs. Keys follow the pattern: `test-<uid>-<workerid>-<sequenceid>`

The tool executes these operations in sequence:

1. **Get (miss)** - Get 1M different keys that don't exist
2. **Set** - Set 1M keys with small values
3. **Get (hit)** - Get the same 1M keys (cache hits)
4. **Delete (found)** - Delete the 1M existing keys
5. **Delete (miss)** - Delete the same 1M keys again (already deleted)
6. **Increment** - Increment counters 1M times (each worker increments its own counter)

## Output

The tool provides real-time progress for each operation and a final summary table:

```
Memcache Speed Test
===================
Server:      127.0.0.1:11211
Concurrency: 4
Target:      10.00K operations

Running: Get (miss) with 10.00K operations...
  Completed: 10.00K ops in 1.69s (5922 ops/sec, 168.86µs avg latency)

Running: Set with 10.00K operations...
  Completed: 10.00K ops in 1.79s (5582 ops/sec, 179.15µs avg latency)

...

Summary
=======
Operation                   Count   Duration      Ops/sec  Avg Latency
─────────                   ─────   ────────      ───────  ───────────
Get (miss)                 10.00K      1.69s        5.92K     168.86µs
Set                        10.00K      1.79s        5.58K     179.15µs
Get (hit)                  10.00K      1.66s        6.01K     166.49µs
Delete (found)             10.00K      1.45s        6.91K     144.69µs
Delete (miss)              10.00K      1.43s        6.98K     143.21µs
Increment                  10.00K      2.26s        4.42K     226.17µs
```

## Performance Characteristics

- **No validation overhead** - Errors are silently ignored to measure pure throughput
- **Minimal allocations** - Reuses values and keys where possible
- **Sequential testing** - Each operation type runs to completion before the next starts
- **Unique keys per run** - Random UID prevents interference from previous runs
- **Per-worker counters** - Increment test uses one counter per worker to reduce contention

## Interpreting Results

- **Get (miss)** vs **Get (hit)** - Shows cache efficiency
- **Delete (found)** vs **Delete (miss)** - Both should be similar (memcache doesn't distinguish)
- **Increment** - Typically slower due to read-modify-write semantics
- **Concurrency scaling** - Compare single-worker vs multi-worker to see parallelization benefits

## Notes

- Health checks are disabled for maximum performance
- Errors are ignored (not counted or reported)
- Results show aggregate performance across all workers
- Each worker processes `count/concurrency` operations

## CI regression comparison

The `Benchmark` GitHub workflow (`.github/workflows/bench.yml`) runs this tool on
every PR and posts a `main` vs PR table as a sticky comment.

To keep the comparison meaningful on noisy shared runners, it:

1. Builds **the PR's harness twice** — once against the PR library, once against
   `main`'s library (via a `replace` to a worktree of the base commit). Only the
   library varies, never the measurement code, so a PR that changes `cmd/speed`
   can't skew its own numbers.
2. Runs **both binaries on the same runner**, so per-host speed cancels out of
   the delta.
3. Aggregates `-runs` repetitions with a **trimmed mean** (drop fastest + slowest)
   to damp transient stalls and noisy neighbours.

Use the same flags locally to reproduce a comparison:

```bash
go build -o /tmp/speed .
/tmp/speed -count 30000 -concurrency 8 -runs 5 -format json > main.json   # on main
/tmp/speed -count 30000 -concurrency 8 -runs 5 -format json > pr.json     # on your branch
/tmp/speed -baseline main.json -compare pr.json                          # markdown table, no server needed
```

**Caveat:** this is end-to-end throughput against a real server, so even with the
same-runner design the numbers carry network and host noise. The comment is a
**signal, not a gate** — only treat changes well beyond the flag threshold
(default ±10%) as real. For deterministic, allocation-level numbers, use the
`BenchmarkClient` Go benchmarks (mock connection, no network) instead.
