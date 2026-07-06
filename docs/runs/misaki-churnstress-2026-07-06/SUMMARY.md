# misaki churnstress — 2026-07-06

Deep stress of three robustness subsystems the fixed-set chaos runs never
exercised under load, via `stress/cmd/churnstress` against **48 real memcached**
on misaki (loopback). Harness + method: `stress/cmd/churnstress/README.md`.

- 48 containers (`cs00..cs47`), client on the same box, 128 workers, key-embedding
  desync invariant on every read.
- Breaker: trip=5, open=2s. Client `Timeout`=1s; caller op-budget=4s (looser than
  Timeout so a hung server's timeout binds server-side and trips the breaker — see
  finding D1 in `docs/dangerous-config-combinations.md`).
- Health-check interval 2s; 23-minute phased schedule.

## Result — PASS (182.6M ops)

`DESYNCS=0`, final pools==active (reaping converged), no breaker wedged open.
Per-phase peaks:

| phase | breakers open | pools | goroutines | heap | desync |
|---|---|---|---|---|---|
| baseline | 0 | 48 | ≤723 | 15.8MB | 0 |
| **churn** | 0 | **33–48** | ≤795 | 16.0 | 0 |
| settle | 0 | 48 | ≤805 | 16.1 | 0 |
| **flap** | **8** | 48 | ≤821 | 16.2 | 0 |
| settle | 0 | 48 | ≤776 | 16.1 | 0 |
| **mass-freeze** | **24** | 48 | ≤415 | 15.6 | 0 |
| mass-thaw | 12→0 | 48 | ≤827 | 16.3 | 0 |
| settle | 0 | 48 | ≤854 | 16.1 | 0 |
| **churn+flap** | 8 | **33–48** | ≤733 | 15.7 | 0 |
| final | 0 | 48 | ≤755 | 16.4 | 0 |

### What each target proved

- **Churn → reaping (48-node scale).** Active set oscillated 33–48; pool count
  tracked it with the 2-pass grace and converged back to 48 — reaping keeps up at
  scale, no pool/goroutine/heap leak. Heap flat at ~16MB across 182M ops.
- **Breaker at limits.** flap opened exactly the 8 flapped shards; **mass-freeze
  opened exactly 24/48** (the frozen half) while the healthy half stayed closed;
  mass-thaw reclosed them (half-open herd handled); 3,340 total state transitions,
  none wedged open at the end.
- **Health-check scaling.** With 24 frozen pools the concurrent health pass stayed
  bounded — goroutines peaked in the ~700–850 band during passes (48 pools ×
  concurrent idle pings) and returned to baseline; final goroutines=2 after Close
  (clean shutdown, no leak). Reaping never stalled.
- **Combined churn+flap** (reap a server whose breaker is open, ops in flight): no
  panic, no use-after-close, desyncs 0.

## Findings

The harness surfaced the dangerous-config-combination class documented in
`docs/dangerous-config-combinations.md`, notably **D1** (caller budget ≤
`Config.Timeout` ⇒ the breaker never sheds a hung server) and **D2**
(`HealthCheckInterval<0` ⇒ departed-pool reaping silently off ⇒ leak under churn).
