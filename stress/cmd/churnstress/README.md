# churnstress — large-fleet churn / breaker / health-check stress

Pushes three robustness subsystems that the fixed-set chaos runs never exercised
under load, against **dozens of real memcached** containers on one box (misaki):

1. **Endpoint churn → pool reaping + selector stability.** A `mutableServers`
   whose active set oscillates (70–100% of the fleet) drives
   `reapDepartedPools`, the 2-pass mark-and-sweep grace, and rendezvous
   (`StableServerSelector`) routing. Asserts: 0 desyncs, pool count converges to
   the active set (no leak), goroutines/heap bounded.
2. **Circuit breaker at its limits.** A subset flaps *faster than the breaker
   open timeout* (mixing `docker pause`=hung and `docker kill`=crash), and a
   mass thaw fires a half-open thundering herd. Asserts: breakers open on the
   faulted subset only, re-close on heal, none wedged open at the end.
3. **Health-check pass under many frozen pools.** Half the fleet is `docker
   pause`d at once; the concurrent health pass must stay bounded (goroutines
   spike then return, reaping not stalled).

Every stored value embeds its key; every read verifies it. A desync (another
key's value) is the headline failure and must stay 0.

## Modes

- **`chaos`** (default) — a fault schedule (churn / flap / mass-freeze / churn+flap)
  over a *static* fleet started by `fleet.sh`. Pushes reaping, the breaker, and
  the health-check pass.
- **`discovery`** — models service discovery / a k8s rolling deploy: the harness
  owns container lifecycle, holding `-fleet` live memcached and every
  `-redeploy-interval` retiring `-redeploy-batch` of them (drop from the Servers
  list, then terminate the container) and deploying the same number with **new
  identities** (new container, new port). The distinct-address count
  (`deployed_total`) grows monotonically. This is the **D2 probe**: with reaping
  on, `pools` stays ~`-fleet`; with reaping off (`-health-interval=-1s`), `pools`
  tracks `deployed_total` — an unbounded leak of pools, breakers, and sockets
  (`open_fds`).

## Run (on the fleet box)

```sh
# chaos mode
./fleet.sh up 48                 # start cs00..cs47 on 127.0.0.1:11300+i
./churnstress -fleet 48 -out churn.jsonl   # ~23 min full schedule
./churnstress -fleet 16 -quick   # ~6 min harness validation
./fleet.sh down 48               # teardown

# discovery mode (manages its own containers; free the ports first)
./churnstress -mode discovery -fleet 24 -redeploy-interval 4s -redeploy-batch 2 \
  -duration 4m -health-interval=-1s -out disco-off.jsonl   # D2 leak
./churnstress -mode discovery -fleet 24 -redeploy-interval 4s -redeploy-batch 2 \
  -duration 4m -health-interval 2s   -out disco-on.jsonl    # control (no leak)
```

The client runs on the same box as the servers (loopback) so the results reflect
the client/breaker/health-check code, not the network.

## The caller-budget subtlety (why `-op-budget` exists)

The per-server breaker excludes a socket timeout when the **caller's context
deadline was the binding one** (the #118 rule: an impatient caller must not trip
a healthy server's breaker). If the caller's per-op budget equals the client
`Config.Timeout`, a *hung* server's timeout is attributed to the caller and the
breaker **never opens** — no shedding, every op pays the full timeout. So the
harness sets the caller budget looser than `-timeout` (default `4×`) so a hung
server's timeout binds on the *server* side and trips the breaker. Operationally:
callers should give a budget ≥ the client Timeout for hung-server shedding to
engage.

## Output

`-out` is a JSONL sample per `-sample-interval` (default 1s): phase, active-set
size, pool count, goroutines, heap, cumulative ops/errors/timeouts/misses/desyncs,
breaker state histogram, open-breaker addresses, breaker transition count. The
final SUMMARY prints pass/fail on desyncs==0, pool convergence, and no wedged
breakers (non-zero exit on FAIL).
