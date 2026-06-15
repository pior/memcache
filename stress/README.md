# stress — in-process stress & failure-injection tests

A nested module (isolated from the main module's deps) holding the stress and
load tests for the memcache client. It lives in its own module so the heavy
failure-injection dependencies — [toxiproxy](https://github.com/Shopify/toxiproxy)
and its transitive packages — stay out of the main module's dependency graph.

## The invariant

Every stored value embeds its key. Any response that returns a value not
matching the requested key proves the connection desynchronized — the worst
possible failure for a cache client. Under churn and failure injection, errors
are acceptable; **wrong data never is**.

Network failures are injected in-process, with no external daemon:

- `flakyProxy` — a TCP proxy that abruptly kills random connections mid-stream,
  simulating network failures and server restarts.
- an embedded toxiproxy — adds latency and jitter through its Go API (no
  toxiproxy daemon or HTTP API involved).

## Running

Requires memcached on `127.0.0.1:11211`:

```sh
docker compose up -d        # from the repo root
cd stress
go test -race -v -run TestStress ./...
```

Or via DevBuddy from the repo root: `bud test-stress`.

### Tunables

| env var | default | meaning |
|---|---|---|
| `STRESS_DURATION` | `5s` | duration of each scenario |
| `STRESS_WORKERS` | `16` | concurrent workers per scenario |

## Scenarios

| test | exercises |
|---|---|
| `TestStress_MixedWorkload` | mixed get/set/delete/add/low-level traffic on a shared key space |
| `TestStress_BatchWorkload` | concurrent pipelined batches; positional integrity (response i belongs to key i) |
| `TestStress_ErrorInjection` | per-request `CLIENT_ERROR` responses must not desync other requests |
| `TestStress_ConnectionChurn` | aggressive lifecycle limits forcing constant reconnection under a saturated pool |
| `TestStress_Counters` | concurrent increments; final values must be exact |
| `TestStress_FlakyNetwork` | proxy randomly kills connections; client must recover on its own |
| `TestStress_SlowNetwork` | high latency + jitter below the timeout; correctness independent of packet timing |
| `TestStress_LatencySpikes` | spikes above the timeout; timed-out responses must never reach the next caller |
| `TestStress_ServerOutage` | server unreachable mid-workload, then back; errors during, full recovery after |
