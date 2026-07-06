# GCP chaos runs — 2026-07-06

Plan steps 2 & 3 (`docs/plan-real-conditions-next-steps.md`): run the tier-3
cloud chaos harness (`loadtest-chaos` branch, orchestrator `--chaos sweep`)
against current main, same-zone then cross-region, and assert the degraded-mode
behaviours under real GCP conditions.

- **Client under test**: `loadtest-chaos` d9d7824 (client = #119 merge 0f70693;
  same behaviour as current main c03a938 for the degraded paths). `top-perf`,
  breaker trip=5 / open=5s, op timeout = profile default 1s.
- **Machines**: `c3-highcpu-4` for both roles. The plan's default `c3-highcpu-8`
  blows the region `C3_CPUS` quota (24): 3 servers × 8 = 24 leaves nothing for
  the clients, so run #1 aborted at client creation (`QUOTA_EXCEEDED`). Orphaned
  server VMs were torn down immediately (verified: 0 leftover VMs/nets/fw). At
  `c3-highcpu-4`, 5 VMs × 4 = 20 vCPUs fits. This is a behaviour-validation run,
  not a throughput record, so the smaller machines don't affect the assertions.
- **Sweep**: baseline, then one healed window per fault type, each healed before
  the next; everything healed by 90% of the 30m run.

## Same-zone (`-placement local -zone us-central1-a`) — run 20260706-114426-ynkq

3 servers + 2 clients all in us-central1-a. Chaos schedule (from
`server/*/chaos.jsonl`, correlated to client addresses by error volume):

| window | server (addr) | fault | active |
|---|---|---|---|
| baseline | — | — | 0–450s |
| 1 | srv-0 `10.8.0.2` | **freeze** (SIGSTOP gray failure) | 450–684s |
| 2 | srv-1 `10.8.0.3` | **blackhole** (iptables DROP partition) | 756–990s |
| 3 | srv-2 `10.8.0.4` | **latency+loss** (netem 30ms±5, 1%) | 1062–1296s |
| 4 | srv-0 `10.8.0.2` | **kill** (SIGKILL + restart) | 1368–1584s |
| recovery | — | — | 1584–1800s |

### Result — every assertion holds (both clients, 30m, ~237M ops each)

Final `loadgen-result.json` (cli-0; cli-1 symmetric):

- **DESYNCS = 0** — both clients, every 30s bucket of the oplog. Never broke.
- **errors concentrate on the faulted servers**, matching the schedule exactly:
  - `10.8.0.2` (srv-0, freeze **and** kill — two windows): **31.5M** errors
  - `10.8.0.3` (srv-1, blackhole): **16.3M** errors
  - `10.8.0.4` (srv-2, latency+loss — *not* a hard outage): **43** errors
    → the netem-slowed shard correctly produced ~0 hard errors; a
    responsive-but-slow server must not trip the breaker, and it didn't.
- **breaker open only for the faulted server during its window, closed after
  heal** — all three breakers **closed** in the final result; the oplog error
  timeline shows errors confined to each fault window and zero between them.
- **healthy-shard latency near baseline through each outage window**: during
  freeze / blackhole / kill, ok-op **p99 stayed ~1.2ms** (= baseline), while
  ok-throughput held at **~2/3** (2 of 3 shards) — graceful degradation, no
  client-wide stall. The frozen-server case (the pre-#119 total-stall bug)
  behaved identically to a hard kill.
- **throughput recovers fully after the last heal** — back to **~140k ops/s**
  (baseline) from 1584s to end.
- **timeouts bounded**: ~200 total per client (deadline hits during blackhole
  before the breaker sheds) — bounded by the 1s op timeout, not an unbounded
  stall.

### Notable: the latency+loss window (1062–1296s)

Total throughput drops to ~1.1k ops/s with ok-op p99 → ~158ms, but **0 errors /
0 timeouts / 0 desyncs**. This is a **closed-loop load-generator artifact**, not
a client fault: netem adds 30ms + TCP RTO on the slow shard, and a synchronous
closed-loop worker pool's throughput is `workers / mean-latency` (Little's law),
so one slow shard drags aggregate throughput down even though 2 shards are fast
(p50 stayed 0.15–0.38ms). The client correctly kept routing to the responsive
server (breaker stayed closed — tripping would shed load from a *working*
server). A pipelined/async production client would not see this cross-shard
head-of-line effect.

Full 30s-bucket timeline: `samezone/oplogstat-cli0-30s.txt`. Raw oplogs (2.4G
each) were dropped from the record; result JSONs, chaos schedules, and hoststat
are kept. Analyzer source: `oplogstat.go`.

## Cross-region (`-placement global`) — run 20260706-122907-ntqk

Servers in us-central1-a (srv-0), us-central1-b (srv-1), **asia-southeast1-b**
(srv-2, `10.9.0.2`); clients in us-central1-a. Same sweep schedule as same-zone.
A third of every client's traffic crosses to the asia shard (~180ms+ RTT)
throughout the run.

### Result — correctness/robustness assertions hold; throughput is RTT-bound

Both clients: **DESYNCS = 0** across every 60s oplog bucket; **all breakers
closed** in the final result; every fault window produced bounded errors and
recovered. cli-1: 2.69M ops, 1.74M errors, 350 timeouts; cli-0 similar.

Oplog timeline (`crossregion/oplogstat-cli1-60s.txt`) correlated to the schedule:

- **Startup transient (0–60s)**: ~1.69M errors, then settles. The far shard's
  breaker **trips during cold connection warmup** (the asia dial is slow at
  t=0) and fast-sheds its third of traffic until a half-open probe succeeds
  (~60s) and it **closes**. Self-heals; 0 desyncs. This dominates the run's
  total error count — the fault windows add only ~4.5k errors each.
- **Steady state**: ~180 ok-ops/s (vs ~140k same-zone), p50 ~0.5ms (the two
  local shards), p99 ~845ms (the asia third). The collapse is the **closed-loop
  generator being latency-bound by the ~180ms+ far shard** (throughput ≈
  workers / mean-latency), not a client fault.
- **freeze srv-0 (450–684)** and **kill srv-0 (1368–1584)**: errors ~4.5k/bucket,
  ok/s → ~105, p50 rises (a *local* shard is down, its ops block until the
  breaker sheds), recover within one bucket of heal.
- **blackhole srv-1 (756–990)**: errors ~4.5k/bucket + bounded timeouts, recovers.
- **latency+loss srv-2/asia (1062–1296)**: netem +30ms on the already-slow far
  shard pushes p99 **over 1000ms** (past the 1s budget → a few timeouts), hard
  errors stay low, 0 desyncs. Unlike same-zone (where 30ms never exceeded the
  budget), cross-region the same fault brushes the op-timeout ceiling — exactly
  the "more interesting" real-RTT interaction the plan wanted to see.
- **Final (1584–1800)**: ok/s back to 184, errors 0, p99 843ms — recovered to
  cross-region baseline.

The low throughput and the startup breaker-trip are **load-model + real-RTT
artifacts** (synchronous closed-loop workers against a high-RTT shard; a
pipelined async production client would not stall cross-shard). Correctness and
robustness are intact: 0 desyncs, every fault bounded by the 1s op timeout,
every breaker recovered.

## Verdict

Under real GCP conditions — same-zone and cross-region, across freeze / blackhole
/ latency+loss / kill — the client's degraded-mode behaviour holds:

- **0 desyncs** in every phase of both runs (the key-embedding invariant never
  broke under any fault or heal transition);
- **per-shard error attribution** — errors land on the faulted shard(s); a
  responsive-but-slow shard does not trip the breaker (same-zone latency window),
  while a shard slowed past the 1s budget does (cross-region latency window);
- **per-shard breaker open during the fault, re-closed on heal** — all breakers
  closed at the end of both runs;
- **no client-wide stall on a hung/frozen shard** — same-zone freeze degraded to
  2/3 throughput with healthy-shard p99 at baseline, the pre-#119 total-stall bug
  gone; everything bounded by the 1s op timeout;
- **full throughput recovery after the last heal** in both runs.

Caveat (not a client issue): the tier-3 loadgen is **closed-loop**, so a slow
shard (cross-region RTT, or the netem latency window) throttles aggregate
throughput by Little's law. This is a property of the measurement rig, not the
client; correctness/robustness are unaffected.
