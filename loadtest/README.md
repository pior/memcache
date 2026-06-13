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
connection time-constants), `-oplog <file>` (full per-op compressed log),
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

## Stress & reliability runs

We periodically run hour-long stress tests on GCE: a load generator against a
multi-instance memcached pool under `-stress` (aggressive connection
rotation/eviction), checking the key-embedding desync invariant on every read
and tracking error/timeout rates, latency, and client memory over the run.

Latest (1c/3s, same-zone, `top-perf -stress`): **374M ops at ~104k ops/s,
0 desyncs, 0.001% error rate, flat client memory (no leak)**. Operation latency
held at p50 ≈ 0.9 ms / p99 ≈ 5 ms; we cap the per-op timeout (`-timeout`) to a
realistic budget so the tail can't silently degrade toward seconds.
