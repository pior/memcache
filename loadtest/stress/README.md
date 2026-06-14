# stress — long-soak local stress run (docker)

A self-contained, multi-day **saturation soak** of the memcache client against
three local memcached servers, all in Docker on one dedicated box (default host
`misaki`). It runs `loadgen` from the parent module in `top-perf -stress`
saturation mode — closed-loop at full concurrency, aggressive connection
lifecycle churn — checking the **key-embedding desync invariant on every read**.
A non-zero `desyncs` count, or a non-zero loadgen exit, means the client returned
another key's data; that is the failure this soak exists to catch.

This is the local sibling of the tier-3 cloud harness (see `../SPEC.md`). It
trades the cloud harness's real-RTT / multi-VM realism for a zero-cost rig that
can run for days. Because loadgen and the three servers share one box, the
latency numbers include local CPU contention — read them as relative
distribution/regression signal, not an SLO.

## Start / redeploy

The host needs only Docker — no Go. `deploy.sh` cross-compiles `loadgen` locally,
ships it with the compose files, builds the image, and brings the stack up.

```sh
./deploy.sh                          # default host (misaki), DURATION=336h, WORKERS=48, GOMAXPROCS=8
DURATION=72h WORKERS=64 ./deploy.sh  # override run length / load
HOST=otherbox ./deploy.sh            # different host
```

Re-running `deploy.sh` rebuilds from current source and restarts the stack.

## Topology & tuning

- 3× `memcached:1.6`, each `-m 2048 -c 4096 -t 2` (2 GB, 2 threads).
- 1× `loadgen` → `memcached{1,2,3}:11211`, `top-perf -stress -timeout 100ms`,
  saturation intensity, histogram snapshot every **10s**, op-log **off**.
- Tunables (env, honored by both `deploy.sh` and `docker-compose.yml`):
  `DURATION` (default `336h` = 14 days), `WORKERS` (concurrency, default 48),
  `GOMAXPROCS` (loadgen CPU cap, default 8 — leaves cores for the servers).

## Check state mid-run (no need to wait for the end)

`loadgen` atomically rewrites two files in `./data` on the host every 10s, so a
reader never sees a torn file:

| file | what |
|---|---|
| `data/status.txt` | human-readable: **run time**, total stats (ops, throughput, hits/misses/errors/timeouts/**desyncs**), latency percentiles per op, and a **latency histogram** |
| `data/snapshot.json` | the full `RunResult` (counters + raw histogram buckets) for offline analysis; same shape as the final `data/result.json` |

```sh
# run time + totals + latency histogram, as of the last 10s tick
ssh misaki 'cat memcache-stress/data/status.txt'

# live progress stream (ops/s, p50, p99, errors, desyncs)
ssh misaki 'cd memcache-stress && docker compose logs --tail=30 -f loadgen'

# the headline invariant — must stay 0
ssh misaki 'cat memcache-stress/data/snapshot.json' | jq '.metrics.desyncs, .metrics.ops, .elapsed_secs'

# is the stack still up?
ssh misaki 'cd memcache-stress && docker compose ps'
```

`status.txt` looks like:

```
run time: 3h12m0s (started 2026-06-14T16:00:00Z, updated 2026-06-14T19:12:00Z)

ops=270000000 (23400/s) hits=... misses=... errors=8 (0.00%) timeouts=0 desyncs=0
latency: p50=928µs p95=1.66ms p99=2.56ms max=21.5ms mean=1.02ms
  add       count=... p99=2.30ms
  ...
latency distribution (all ops):
  < 1ms         168042  59.81% (cum  61.30%) ########################################
  < 2ms         101360  36.08% (cum  97.37%) ########################
  < 5ms           7245   2.58% (cum  99.95%) #
  ...
```

## Stop / inspect after

```sh
ssh misaki 'cd memcache-stress && docker compose down'   # graceful; flushes data/result.json
ssh misaki 'cat memcache-stress/data/result.json' | jq '.metrics.desyncs, .elapsed_secs'
```

Periodic snapshots cap data loss at one 10s interval even on an ungraceful kill.
If `loadgen` exited on its own with a non-zero status, the container stays down
(no auto-restart) and `data/` holds the evidence; `docker compose logs loadgen`
shows the `DESYNC DETECTED` line.
