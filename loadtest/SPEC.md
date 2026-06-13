# `loadtest` — tier-3 cloud load & stress harness

Status: design + in-progress implementation.
Module: `github.com/pior/memcache/loadtest` (nested, isolated from the main module).

## 1. Purpose & context

The stress suite in the main module (`stress_test.go`, `//go:build stress`)
covers tiers 1–2: in-process latency/failure injection (toxiproxy + a flaky TCP
proxy) runnable on a laptop or in CI. **Tier 3** is this harness: a command,
run from a developer's machine (no CI), that provisions multiple Google Cloud
VMs, runs a purpose-built load generator against self-managed memcached for a
configurable duration (default **1h**), collects data-plane *and* control-plane
*and* host-resource logs locally, and tears everything down.

It serves two goals from one codebase:

- **Stress testing** — real networking, real RTT, killable servers, long soaks;
  the worst-case-finding mode. The non-negotiable invariant: every stored value
  embeds its key, so any read whose value lacks the `key|` prefix proves the
  connection desynchronized. Errors under failure injection are acceptable;
  wrong data never is.
- **Performance measurement** — latency percentiles and throughput under
  controlled load and resource profiles.

The system under test is the **memcache client library** itself (pooling,
routing, batching, breaker, correctness under load); memcached is the backend.

### Design principles
- **Dependency isolation.** The heavy GCP SDK lives only in the orchestrator,
  inside this nested module. The main module's `go.mod` is never touched. This
  mirrors the existing `cmd/speed/` nested-module precedent
  (`replace github.com/pior/memcache => ..`).
- **No extra agents on VMs.** Host metrics come from a tiny self-contained
  sampler reading `/proc`; logs are uploaded to GCS and pulled locally. No Ops
  Agent, no Prometheus server, no daemon.
- **Everything labelled.** Every cloud resource carries labels for cost
  reporting and for reaping orphans.
- **Trust through observability.** We must be able to confirm the harness is
  doing what we think (are clients CPU-bound? is the NIC saturated? are servers
  the bottleneck?) and tune from evidence, not guesswork. See §6.

## 2. Topology

```
   laptop                         Google Cloud (one global VPC)
 ┌────────────┐   gcloud SDK    ┌──────────────────────────────────────────┐
 │orchestrator│ ───────────────>│  client VMs (zone A, the reference zone)  │
 │  (GCP SDK) │                 │   loadgen + hoststat                       │
 │            │ <─── GCS ───────│            │ memcache traffic              │
 │ ./runs/<id>│   (log pull)    │            v                              │
 └────────────┘                 │  server VMs (zones/regions per placement) │
                                │   memcached@11211..N (systemd) + hoststat  │
                                └──────────────────────────────────────────┘
```

- **Client fleet** (default 3 VMs): each runs `loadgen` (the workload) and
  `hoststat` (host metrics). Anchored in one reference zone — latency variation
  is server-side only.
- **Server fleet**: each VM runs `instances-per-vm` memcached processes on
  consecutive ports via a `memcached@<port>` systemd template, plus `hoststat`.
- **Address list** (what the client pools over) = server VM private IPs ×
  ports. The client routes by `host:port` (jump hash), one pool + breaker per
  address — so ports on one VM already exercise the full pooling logic; multiple
  VMs add independent failure domains and divergent RTT.

## 3. Server topology & client pooling

The address count is decoupled from the VM count:

- `addresses = server-vms × instances-per-vm` (ports `11211..11211+N-1`, each
  bound to the VM's private IP). Client server list built from the cross-product.
- **Multiple ports/IPs on one VM** fully exercise *pooling logic* (independent
  per-address pools, jump-hash distribution, per-address breakers) — the client
  cannot distinguish ports-on-one-IP from ports-on-many.
- **Multiple VMs** add what ports cannot: independent failure domains and
  divergent per-server network latency, required for realistic failover stress.
- **Failure-injection granularity**: kill one memcached *process* → one address
  drops (per-server breaker + jump-hash reroute); stop a *VM* → a whole failure
  domain of addresses drops (multi-address outage + recovery).
- **Pooling validation**: loadgen samples `client.AllPoolStats()` into the
  metrics stream (per-address created/destroyed/in-use conns, acquire waits,
  breaker state) so the report shows key distribution and per-server churn,
  catching imbalance or a silently-starved server.

## 4. Latency mix (geographic placement)

Latency comes from *where* each server VM sits relative to the client zone:
same zone ≈ sub-ms, another zone same region ≈ ~1ms, another region ≈ tens of ms.

A `placement` spec assigns each server VM a location. Named presets:
- `local` (default) — all servers in the clients' zone. Cheap baseline.
- `regional` — spread across zones within the clients' region.
- `global` — a deliberate same-zone + same-region + far-region mix; widest RTT.
- custom — `--placement us-central1-a:2,us-central1-b:2,us-east1-b:2`.

GCP mechanics: one **global VPC**, one **regional subnet per region** used; the
single memcache firewall rule (client tag → server tag, internal) is
network-global; cross-region internal IPs route over the VPC so servers stay
private everywhere — no public IPs. **Cross-region/zone egress is billed per
GB** (surfaced by the per-tier byte counters; `local` avoids it).

The orchestrator records each address's zone/region in the run manifest;
loadgen tags per-op latency by server address; the report groups latency
histograms by placement tier (same-zone / cross-zone / cross-region).

Synthetic alternative (future): tc/netem for deterministic delay in one zone
without egress cost. Real placement is the primary mechanism.

## 5. Client fleet & load intensity (simulating longer runs)

Two intensity models, chosen by goal:

- **Saturation (stress)** — closed-loop: ramp concurrency until throughput
  plateaus, then hold at the ceiling, driving the client + pool to full
  potential. Total ops ≈ throughput × duration, so a saturated 1h run compresses
  the *operation-count wear* of a far longer modest-load run into the time
  budget — maximizing the odds of surfacing rare bugs (desync, pool leaks,
  breaker edges) and contention.
- **Fixed-rate (perf)** — open-loop at a target rate below the ceiling, with
  coordinated-omission correction, to measure service-latency percentiles
  cleanly (saturation latency is just queueing, not a meaningful SLO).

**Two distinct levers for "longer":**
- *Operation-count effects* (races, desync, leaks, contention) scale with
  throughput → compress via saturation.
- *Wall-clock effects* (`MaxConnLifetime`/`MaxConnIdleTime` eviction,
  `HealthCheckInterval`, breaker open/half-open timeouts, TTL expiry) are
  time-driven and do **not** compress with throughput. To exercise their churn
  in 1h, **shorten the configured time constants** (as `ConnectionChurn` does
  with 100ms lifetimes). The profile exposes these knobs.

A `calibrate` step ramps concurrency on one client VM to find the per-VM
throughput ceiling, so a run is sized (workers × clients) to actually reach
saturation, or to hold a chosen fraction of it for fixed-rate.

## 6. Observability — verify & tune the setup (host metrics)

We must be able to tell whether the harness is doing what we expect and tune it.
A tiny **`hoststat`** binary runs on **every** VM (client and server), sampling
`/proc` every `--interval` (default 5s) and emitting JSONL, compressed and
uploaded to GCS. No external agent. Linux-only collection (build-tagged);
parsers are unit-tested against captured `/proc` fixtures so they run anywhere.

### Signals (utilization *and* saturation)
| Concern | Utilization | Saturation |
|---|---|---|
| CPU | busy fraction from `/proc/stat` (user+sys+...+steal), per-core + aggregate | `/proc/pressure/cpu` PSI (`some avg10`), run-queue (`procs_running`), `loadavg`/cores |
| Memory | used vs total from `/proc/meminfo` | `/proc/pressure/memory` PSI, swap activity |
| Network | rx/tx bytes & packets per NIC from `/proc/net/dev`; rate vs the VM's egress cap (derived from machine type → vCPU count) | drops/errors (`/proc/net/dev`), TCP retransmits/resets (`/proc/net/snmp` `Tcp:`) |
| Disk (op-log on) | r/w bytes from `/proc/diskstats` | `/proc/pressure/io` PSI |

PSI (Linux ≥4.20, present on Debian 12) is the headline saturation signal —
it directly answers "is this resource the bottleneck?" Missing `/proc/pressure`
(older kernels) is tolerated and omitted.

### How it's used
- **Network saturation %**: the sampler reports bytes/s; the report (which knows
  each VM's machine type) computes % of the egress cap. GCE egress scales with
  vCPU count (~2 Gbps/vCPU up to a per-machine ceiling) — the cap table lives in
  the `profile` package.
- **Bottleneck attribution**: per-run summary flags, e.g. "clients CPU-saturated
  (PSI cpu some avg10 > 0.2) while servers idle → load generators are the limit,
  add client VMs or use the top-perf machine type"; or "server NIC at 95% of cap
  → network-bound". This is the tuning loop.
- **Live `status`**: the orchestrator pulls the latest sample per VM during a run
  and prints a compact CPU/net/PSI table so a long run can be watched and aborted
  early if mis-sized.
- **Correlation**: host samples and loadgen metric snapshots share a wall-clock
  timestamp and run-id, so throughput dips can be lined up against CPU/net spikes
  and breaker trips in the report.

## 7. Data-plane logging (configurable)

Numeric keyspace (`stress:lt:<n>`) so per-op records store a `uint32` key-id,
not the string.

- **Always — metrics**: latency histogram (per op type) + atomic counters (ops,
  hits/misses, errors by class, desyncs, bytes). Periodic snapshot (default 10s)
  + final summary. This is the perf output.
- **Always — flight recorder**: a fixed-size ring of recent op records per
  worker; on any desync or transport error, dump the ring (the ops leading to
  the anomaly). Cheap, captures crash context.
- **Opt-in — full op-log** (`--oplog`): every op as a compact ~20-byte binary
  record (delta-ts, worker, op, key-id, status, latency-µs), streamed through
  zstd, sharded per worker. Estimate: ~3–4 GB raw/VM-hour → ~0.5 GB zstd. Off
  for perf, on for stress forensics. Offline decoder in `oplog`.

## 8. Control-plane logging

- Orchestrator: structured `slog` to `./runs/<run-id>/orchestrator.log` + stdout
  — every GCP call, resource id, label, timing.
- `run-manifest.json`: full config, profile, placement, machine types, image,
  VM list + private IPs + zone/region, address→placement-tier map, loadgen git
  commit, start/end, per-VM exit status.
- Per VM: loadgen + hoststat stdout/stderr via `systemd-run`/journald → file,
  plus serial-console output, uploaded to GCS alongside data-plane + host logs.

## 9. Resource profiles

A profile bundles `{machine_type, cpu_constraint, GOMAXPROCS, spot, memcache.Config knobs, intensity}`:

- **`top-perf`** — CPU unconstrained. Larger machine (e.g. `c3-highcpu-8`),
  `GOMAXPROCS=all`, **on-demand** (stable numbers). Perf measurement.
- **`efficiency`** — CPU constrained. **Same base machine type** as top-perf,
  but loadgen wrapped by `systemd-run --property=CPUQuota=100%` (1 vCPU) with
  `GOMAXPROCS` pinned, so CPU allowance is the only variable. Spot OK.

Stress vs perf mode layers on top: stress = longer duration, server kill/restart
injection, `--oplog` on, spot allowed, saturation intensity, optionally shortened
time constants; perf = op-log off, on-demand, no injection, fixed-rate intensity.

## 10. Networking & security

- One global VPC; a regional subnet per region in the placement. Dedicated
  network tags (`client`, `server`). One firewall rule: memcache port from the
  client tag to the server tag, internal IPs only — covers all regions.
- memcached bound to the private IP, never public (it has no auth).
- v1: ephemeral external IPs for SSH/egress (incl. GCS); memcache port never
  opened externally. Hardening (future): IAP SSH + Cloud NAT + Private Google
  Access to drop external IPs entirely.

## 11. Labels, cleanup, cost

Every resource (instances, disks, firewall, GCS prefix) labelled:
`app=memcache-loadtest`, `run-id=<id>`, `role=client|server`, `profile=<p>`,
`owner=<user>`, `created=<unix>`, `ttl-hours=<n>`.

- Teardown deletes everything matching `app` + `run-id`.
- `reap` deletes resources where `app=memcache-loadtest` and (`run-id` matches OR
  `created` older than `ttl-hours`) — catches orphans from crashed runs.
- Cost guardrails: spot by default for stress (preemption is itself a
  failure-injection signal), on-demand forced for perf; hard `--max-vms` cap;
  small defaults; deferred teardown on Ctrl-C/panic unless `--keep`;
  cross-region egress flagged by per-tier byte counters.

## 12. Module & package layout

```
loadtest/                              module github.com/pior/memcache/loadtest
├── go.mod / go.sum                    replace github.com/pior/memcache => ..
├── SPEC.md / README.md
├── cmd/
│   ├── loadgen/main.go                VM workload binary — NO cloud deps
│   ├── hoststat/main.go               VM host-metrics sampler — NO cloud deps
│   └── orchestrator/main.go           laptop binary — the only GCP-SDK importer
└── internal/
    ├── workload/                      key-embedding invariant + op mix (self-contained)
    ├── metrics/                       latency histogram + counters + snapshot/merge
    ├── recorder/                      flight-recorder ring buffer
    ├── oplog/                         opt-in zstd per-op log: writer/record/reader
    ├── hoststat/                      /proc parsers + Linux sampler + portable stub
    ├── profile/                       config + named profiles + intensity + cap table
    ├── report/                        aggregate per-VM + per-server + host metrics
    └── cloud/                         GCP compute/storage/firewall wrappers, labels,
                                       run-id, placement resolution, startup-scripts
```

**Isolation guarantee.** A module's `go.sum` is a superset; the Go linker only
includes packages reachable from a given `main`. `loadgen`/`hoststat` never
import `cloud.google.com/go/*`, so those binaries never link the SDK even though
all three share one module. The main module imports nothing from here.

**Invariant home.** The key-embedding invariant lives in
`loadtest/internal/workload`, not the main module's public API: each binary
reads the values it itself wrote, so the contract is self-consistent within
`loadgen` — there is no cross-process contract with `stress_test.go` to keep in
sync. `stress_test.go` stays untouched.

## 13. Orchestrator subcommands

`build` (cross-compile loadgen+hoststat, `GOOS=linux GOARCH=amd64
CGO_ENABLED=0`), `dry-run` (print full plan + scripts, no GCP calls), `up`,
`start`, `status` (live host+throughput table), `collect`, `down`, `reap`,
`calibrate` (per-VM ceiling), and `run` =
build→up→start→wait(duration)→collect→down in one shot. Deferred teardown on
Ctrl-C/panic unless `--keep`.

## 14. Implementation phases

1. **Module skeleton + `workload`** — `go.mod` + replace; invariant/op-mix + its
   drift-guard test (`ParseKeyID(Value(id)) == id`, `CheckValue` rejects a
   foreign key). *(verifiable locally)*
2. **`metrics` + `profile`** — histogram/counters/snapshot; config + presets +
   intensity + egress-cap table. *(unit tests)*
3. **`hoststat`** — `/proc` parsers (fixture tests) + Linux sampler + stub +
   `cmd/hoststat`. *(parser tests run anywhere; sampler smoke on Linux)*
4. **`generator` + `cmd/loadgen`** — worker pool, intensities, invariant checks,
   pool-stats sampling. *(smoke vs docker-compose; zero desyncs)*
5. **`oplog` + `recorder`** — zstd records round-trip; ring dump on anomaly.
6. **`cloud` + `report` + `cmd/orchestrator`** — VPC/subnets/firewall/VMs/labels,
   placement resolution, startup-scripts, run/collect/down/reap/status/calibrate;
   report aggregation incl. host-metric bottleneck attribution. Pure logic
   unit-tested; SDK calls behind a small interface; `dry-run` exercises the
   whole plan with no cloud calls.
7. **Docs + `dev.yml` + multi-address docker-compose** for the local pooling
   smoke; full build/vet/test.

## 15. Verification

- **loadgen smoke (local):** `cd loadtest && go run ./cmd/loadgen -servers
  127.0.0.1:11211 -duration 30s -workers 16` against docker-compose memcached →
  metrics emitted, **zero desyncs**.
- **pooling smoke (local, multi-address):** memcached on `:11211..11214`,
  loadgen pointed at all four → report shows keys distributed across all four
  pools (jump hash) and zero desyncs.
- **hoststat (local):** `go run ./cmd/hoststat -interval 1s -duration 5s` →
  JSONL with CPU/mem/net (PSI present on Linux, omitted on macOS dev).
- **Unit tests (no GCP):** workload round-trip; metrics merge math; profile
  resolution + cap table; oplog zstd round-trip; hoststat `/proc` parsers
  against fixtures; cloud label/run-id/placement/startup-script generation;
  report aggregation + bottleneck attribution.
- **orchestrator dry-run:** `orchestrator dry-run --placement global --clients 3
  --servers 1 --duration 1h` prints subnets, per-VM locations, address→region
  map, labels, and startup-scripts with **no** GCP calls.
- **minimal real run (user-triggered, costs money):** `orchestrator run
  --placement us-central1-a:1,us-east1-b:1 --clients 1 --duration 5m`; afterward
  `./runs/<id>/` holds metrics + manifest + host logs, the report groups latency
  by placement tier (cross-region tier shows higher percentiles), bottleneck
  attribution is sane, and a label query returns no surviving resources.

## 16. Defaults (overridable)

Duration 1h; 3 client VMs; 3 server VMs × 2 instances = 6 addresses; placement
`local`; clients' zone `us-central1-a`; Debian 12; profile `top-perf`; op-log
off; hoststat interval 5s; spot for stress / on-demand for perf; project & owner
from `gcloud config` / `$USER`; GCS bucket `gs://<project>-memcache-loadtest`
(created if absent, labelled).
