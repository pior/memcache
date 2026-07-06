# loadtest — tier-3 cloud load & stress harness

A nested module (isolated from the main module's deps) for running the memcache
client under load against real memcached on Google Cloud VMs: long runs,
real RTT, a killable multi-address server pool, host observability, and a
key-embedding desync invariant. See [SPEC.md](SPEC.md) for the full design.

## Binaries

| binary | runs on | purpose |
|---|---|---|
| `loadgen` | client VMs | generates the workload, checks the invariant, emits metrics + optional op-log |
| `hoststat` | every VM | samples CPU/mem/net/PSI from `/proc` for verify-and-tune |
| `chaosd` | server VMs | executes a fault-injection timeline (netem latency/loss, iptables blackhole, SIGSTOP freeze, SIGKILL) against the local memcached |
| `orchestrator` | your laptop | provisions VMs, deploys, collects logs, tears down (GCP SDK) |

## Local development

Everything except live provisioning runs locally against `docker compose up`.

```sh
# one server
go run ./cmd/loadgen -servers 127.0.0.1:11211 -profile efficiency -duration 30s

# multi-address pooling smoke (jump-hash distribution); needs servers on 11211-11213
docker compose up -d   # plus extra instances, see docker-compose.yml
go run ./cmd/loadgen -servers 127.0.0.1:11211,127.0.0.1:11212,127.0.0.1:11213 -duration 30s

# host sampler (Linux collects real /proc; macOS emits warmup samples)
go run ./cmd/hoststat -interval 1s -duration 5s

# preview a cloud run without touching GCP
go run ./cmd/orchestrator dry-run -placement global -clients 3 -servers 3 -duration 1h -bucket gs://demo
```

A non-zero `desyncs` count, or a non-zero exit from `loadgen`, means the client
returned another key's data — the failure this harness exists to catch.

## loadgen flags

`-servers`, `-profile` (`top-perf`|`efficiency`), `-duration`, `-workers`,
`-conns` (max connections per server), `-timeout` (per-op + connect timeout),
`-keyspace`, `-rate` (fixed-rate ops/s; 0 = saturation), `-stress` (shorten
connection time-constants), `-breaker-trip`/`-breaker-open` (per-server
circuit breaker), `-oplog <file>` (full per-op compressed log),
`-flight-ring`, `-report-interval`, `-out`.

## Cloud run

`run` provisions real resources via the Compute + Storage SDKs (the
`GCEProvisioner`), using Application Default Credentials. Each run writes a
provenance manifest (`<bucket>/<run-id>/run.json`: `-name`, git
branch/commit/dirty, and the config) so the GCS history is comparable over time.

```sh
go run ./cmd/orchestrator build              # cross-compile loadgen + hoststat
go run ./cmd/orchestrator run \
  --project my-proj --placement global \
  --clients 3 --servers 3 --instances-per-vm 2 \
  --profile top-perf --duration 1h --oplog
```

`dry-run` (same flags) prints the full plan with no cloud calls. All resources
are labelled `app=memcache-loadtest run-id=<id> …`; `down --run-id <id>` tears a
run down and `reap --ttl-hours N` clears orphans. Teardown also runs
automatically at the end of `run` (unless `--keep`) and on Ctrl-C.

## Chaos runs

`--chaos` injects real degraded-network conditions on the server VMs during a
run. There is no SSH path to the VMs: the orchestrator renders one fault
timeline per server VM, uploads it to `<bucket>/<runID>/chaos/<vm>.json`, and
each VM's startup-script runs `chaosd` against its own schedule. Every action
is logged to `<bucket>/<runID>/server/<vm>/chaos.jsonl` for correlation with
the clients' error/latency timelines.

```sh
go run ./cmd/orchestrator run \
  --project my-proj --clients 2 --servers 3 --duration 30m \
  --chaos sweep --breaker-trip 5 --breaker-open 5s
```

The `sweep` preset staggers one window per fault type across the run —
baseline quarter, then **freeze** (SIGSTOP: hung-but-connected, the gray
failure), **blackhole** (iptables DROP: partition), **latency+loss** (netem),
**kill** (SIGKILL: crash, healed by restart) — each healed before the next,
everything healed by 90% so recovery is observable. A custom timeline is a
JSON file mapping server index to events (`--chaos my-timeline.json`).

`--breaker-trip N` enables the client's per-server circuit breaker (trip after
N consecutive failures, `--breaker-open` interval before half-open probes).

What to assert after a chaos run:

- `desyncs` = 0 — always, in every phase;
- `errors_by_server` concentrates on the faulted VM's addresses;
- `breaker_state` open only for the faulted server while faulted, closed
  again after heal;
- healthy-shard latency stays near baseline through each fault window;
- throughput recovers fully after the last heal.

## Stress & reliability runs

We periodically run hour-long stress tests on GCE: a load generator against a
multi-instance memcached pool under `-stress` (aggressive connection
rotation/eviction), checking the key-embedding desync invariant on every read
and tracking error/timeout rates, latency, and client memory over the run.

Latest (1c/3s, same-zone, `top-perf -stress`, 1h): **351M ops at ~98k ops/s,
0 desyncs, ~0 errors (8 / 351M), flat client memory (no leak)**. With a realistic
100ms op budget (`-timeout`) and moderate concurrency, single-op latency stayed
near the no-load floor — p50 ≈ 0.2 ms, p99 ≈ 1.4 ms, single-op max ≈ 12–17 ms
(vs a ~1s tail when the timeout was left at 1s and the client over-saturated).
