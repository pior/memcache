# misaki chaos replay — 2026-07-06

Plan step 4 (`docs/plan-real-conditions-next-steps.md`): replay the chaos
scenarios on current main on the misaki soak box, the point being the **hung**
case that stalled the client before the bounded-op-timeout fix (#119).

## Setup

- **Client under test**: loadgen built from `loadtest-chaos` (d9d7824), whose
  `replace github.com/pior/memcache => ..` pins the client to the #119 merge
  (0f70693) — i.e. the bounded 1s default op timeout plus the loadtest-chaos
  per-server error attribution and breaker flags. Behaviourally equivalent to
  current main (c03a938) for the degraded paths under test (the c03a938 delta —
  post-close err contract, uint64 counters, CI — doesn't touch this behaviour).
- **Topology**: 3× `memcached:1.6` + 1× loadgen, all docker on misaki, isolated
  network. `top-perf -stress`, 32 workers, GOMAXPROCS=8.
- **Op timeout**: profile default **1s** (no `-timeout` override) — exercises
  the #119-enforced default directly.
- **Breaker**: `-breaker-trip=5 -breaker-open=5s`.
- **Faults** (`chaos-drive.sh`, docker primitives against the real stack):
  - `hung`  = `docker pause`  (cgroup freezer = SIGSTOP gray failure) → unpause
  - `kill1` = `docker kill` one node → `docker start`
  - `kill2` = `docker kill` two nodes (majority down) → `docker start`
  - each fault: ~90s down, ~90s heal window; snapshot captured at each boundary.
- Run length 804s, 187.8M ops total.

## Result — every assertion holds

`analyze.sh` output (raw captures in `phases/`, `chaos.jsonl`, `result.json`):

| phase (t) | ops (+Δ) | errors (+Δ) | error attribution | breakers | desyncs |
|---|---|---|---|---|---|
| baseline (185s) | 37.4M | 0 | — | all closed | 0 |
| **HUNG-peak** (275s) | +24.8M | +10.47M | **mc2 only** | **mc2 open**, mc1/mc3 closed | 0 |
| hung-healed (365s) | +17.8M | +0.03M | mc2 | all closed | 0 |
| **KILL1-peak** (455s) | +25.0M | +10.54M | **mc2 only** | **mc2 open**, mc1/mc3 closed | 0 |
| kill1-healed (545s) | +18.1M | +0.63M | mc2 | all closed | 0 |
| **KILL2-peak** (635s) | +30.7M | +21.7M | **mc1 + mc3** (mc2 flat) | **mc1/mc3 open**, mc2 closed | 0 |
| kill2-healed (725s) | +18.4M | +1.36M | mc1/mc3 | all closed | 0 |
| final (785s) | +11.7M | +0 | — | all closed | 0 |

**Final:** ops=187.8M, errors=44.7M, timeouts=0, **desyncs=0**, all breakers closed.

- **desyncs = 0 in every phase and final** — the key-embedding invariant never
  broke, under any fault or heal transition.
- **Errors attributed to the faulted node only.** During hung and kill1 100% of
  errors land on `mc2`. During kill2 the errors split across the two killed nodes
  `mc1`+`mc3` while **`mc2`'s cumulative error count stays frozen** at 21,671,600
  — proof the surviving shard served cleanly while the majority was down.
- **Breaker opens only for the faulted node during its window and re-closes on
  heal.** Hung/kill1 → mc2 open; kill2 → mc1+mc3 open, mc2 stays closed; every
  heal window returns all three to closed within ≤90s.
- **No client-wide stall while a node hangs** — the pre-#119 failure. In the hung
  window the client completed **~14.3M successful ops in 90s (~159k ok-ops/s)** on
  the two healthy shards; throughput never collapsed toward zero (which is what a
  stall on the frozen node would have caused as all 32 workers piled up on it).
- **Fast recovery after kill1/kill2** — matches the earlier soak: heal windows
  add only residual reconnection errors (30k / 627k / 1.36M) that stop entirely
  (final phase adds 0 errors), breakers reclose promptly.

### Note on `timeouts = 0`

The loadgen's dedicated `timeouts` bucket (errors matching
`context.DeadlineExceeded`) stayed 0. Degraded-shard load was shed by the open
breaker as fast-failing errors and attributed to the faulted shard, so ops never
piled up against the 1s deadline — the breaker trips faster than ops accumulate.
The bounded 1s op timeout is what keeps the pre-trip ops and half-open probes
from blocking forever; the deterministic isolation of that specific bound is
`stress/degraded_stress_test.go::TestStress_HungServerDefaultTimeout`.

## Verdict

The degraded behaviours are verified under real (frozen/killed container)
conditions on current main: **0 desyncs, no client-wide stall on a hung node,
per-shard error attribution, and per-shard breaker open/re-close** — the hung
case that stalled the client before #119 is fixed.

Rig + raw data preserved on misaki (`~/memcache-chaos`) and here.
