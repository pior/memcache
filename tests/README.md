# Memcache Client Reliability Testing

Production-ready reliability testing framework for the memcache client using Prometheus, Grafana, and Toxiproxy.

## Architecture

Two independent processes emit metrics to Prometheus, visualized in Grafana:

```
┌─────────────────┐         ┌──────────────────┐
│ Load Generator  │         │ Scenario         │
│   (loadgen)     │         │ Controller       │
│                 │         │ (scenariod)      │
│ - Continuous    │         │                  │
│   workload      │         │ - Orchestrates   │
│ - Client metrics│         │   failures       │
│ - :9090/metrics │         │ - Scenario state │
└────────┬────────┘         │ - :9092/metrics  │
         │                  └────────┬─────────┘
         │                           │
         └─────────┬─────────────────┘
                   │
        ┌──────────▼────────────┐
        │     Prometheus        │
        │   Scrapes every 1s    │
        │      :9091 (UI)       │
        └──────────┬────────────┘
                   │
        ┌──────────▼────────────┐
        │       Grafana         │
        │   Dashboard + alerts  │
        │        :3000          │
        └───────────────────────┘
```

## Quick Start

### 1. Start Infrastructure

```bash
cd tests
docker compose up -d
```

This starts:
- 3 memcache nodes (via toxiproxy on ports 21211-21213)
- Toxiproxy for failure injection
- Prometheus for metrics collection
- Grafana for visualization

### 2. Start Load Generator

```bash
go run ./cmd/loadgen -concurrency 100 -workload mixed
```

Generates continuous traffic and exports client metrics.

### 3. Open Grafana Dashboard

Open http://localhost:3000/d/memcache-client-perf

Login: `admin` / `admin`

### 4. Run Scenarios

```bash
# List available scenarios
go run ./cmd/scenariod -list

# Run single scenario
go run ./cmd/scenariod -scenario packet-loss-10-pct

# Run all scenarios sequentially
go run ./cmd/scenariod -scenario all

# Run scenarios in a loop
go run ./cmd/scenariod -scenario all -loop
```

## Available Scenarios

### Single-Server Failures

**Packet Loss** (6 scenarios) - Test circuit breaker behavior under packet loss:
- `packet-loss-2-pct` - 2% loss (should handle gracefully)
- `packet-loss-5-pct` - 5% loss (minor impact)
- `packet-loss-10-pct` - 10% loss (circuit breaker may trip)
- `packet-loss-20-pct` - 20% loss (significant impact)
- `packet-loss-50-pct` - 50% loss (circuit breaker should trip)
- `packet-loss-100-pct` - Complete partition (immediate failover)

**Latency** (6 scenarios) - Test timeout and circuit breaker with +200ms latency:
- `latency-200ms-100ms` - Very brief (100ms duration)
- `latency-200ms-1s` - Short (1 second)
- `latency-200ms-5s` - Medium (5 seconds)
- `latency-200ms-10s` - Extended (10 seconds)
- `latency-200ms-40s` - Long (40 seconds)
- `latency-200ms-2m` - Very long (2 minutes)

### Multiple Simultaneous Failures

**Multi-Failure** (2 scenarios) - Test extreme degradation (2 out of 3 servers fail):
- `multi-failure-2-servers-packet-loss` - 100% packet loss on 2 servers
- `multi-failure-2-servers-latency` - +500ms latency on 2 servers

**Total: 14 scenarios**

## Scenario Structure

Every scenario follows a 3-phase pattern for consistent analysis:

```
Phase 1: Stabilization (30s)
  ├─ No toxics applied
  ├─ Client reaches steady state
  └─ Baseline metrics established

Phase 2: Testing (varies)
  ├─ Toxic applied to server(s)
  ├─ Observe client behavior
  └─ Circuit breakers may trip

Phase 3: Recovery (60s)
  ├─ Toxics removed
  ├─ Circuit breakers recover
  └─ System returns to baseline
```

## Grafana Dashboard

The dashboard includes:

### Scenario Phase Timeline
- Full-width panel at the top
- Shows current scenario and phase
- Color-coded: Blue (stabilization) → Orange (testing) → Green (recovery)

### Client Metrics
- **Operations/sec** - Throughput over time
- **Error Rate** - Percentage of failed operations
- **Circuit Breaker States** - Per-server state (closed/half-open/open)
- **Circuit Breaker Failures** - Total and consecutive failure counts
- **Pool Connections** - Total, active, and idle connections per server

All panels refresh every 1 second for sub-second observability.

## Configuration

### Load Generator Flags

```bash
go run ./cmd/loadgen [flags]

Flags:
  -concurrency int      Number of concurrent workers (default 100)
  -workload string      Workload pattern: mixed, get-heavy, set-heavy (default "mixed")
  -hot-keys int         Number of hot keys for workload (default 10)
  -metrics-port string  Prometheus metrics port (default ":9090")
  -max-procs int        CPU cores to use (default 4)
```

**Hot Keys:** The `mixed` workload generates 30% of requests to a small set of "hot keys" to simulate real-world cache patterns where certain keys are accessed more frequently. Increase `-hot-keys` for more even distribution across servers.

### Scenario Controller Flags

```bash
go run ./cmd/scenariod [flags]

Flags:
  -scenario string      Scenario name or "all" (required)
  -list                 List available scenarios
  -loop                 Run scenarios continuously
  -metrics-port string  Prometheus metrics port (default ":9092")
  -max-procs int        CPU cores to use (default 2)
```

## Interpreting Results

### Healthy Client Behavior

During single-server failures:
- ✅ Circuit breaker trips within 1-2 seconds
- ✅ Error rate spikes briefly (<5s), then recovers
- ✅ Traffic redistributes to healthy servers
- ✅ Operations/sec maintains ~66% throughput (2/3 servers)
- ✅ Recovery completes within 10-15 seconds

During multi-server failures (2 out of 3):
- ✅ Two circuit breakers trip
- ✅ All traffic concentrates on 1 healthy server
- ✅ Operations/sec maintains ~33% throughput (1/3 servers)
- ✅ Error rate returns to baseline once routing stabilizes

### Warning Signs

- ❌ Circuit breaker doesn't trip within 5 seconds
- ❌ Error rate remains elevated (>1%) for >30 seconds
- ❌ Operations/sec drops to near zero during single-server failure
- ❌ Recovery takes >60 seconds after toxic removal
- ❌ Connection pool errors continue after circuit opens

## Troubleshooting

### Grafana shows "No Data"

1. Check Prometheus is scraping:
   ```bash
   # Open Prometheus UI
   open http://localhost:9091/targets

   # Both targets should show "UP":
   # - loadgen (host.docker.internal:9090)
   # - scenariod (host.docker.internal:9092)
   ```

2. Verify processes are running:
   ```bash
   # Should see metrics
   curl http://localhost:9090/metrics | grep memcache
   curl http://localhost:9092/metrics | grep scenario
   ```

### Scenarios don't start

1. Ensure Docker Compose is running:
   ```bash
   docker compose ps
   # Should see: toxiproxy, memcache1, memcache2, memcache3, prometheus, grafana
   ```

2. Check toxiproxy is accessible:
   ```bash
   curl http://localhost:8474/proxies
   ```

## Metrics Reference

### Client Metrics (from loadgen)

```promql
# Throughput
memcache_operations_per_second

# Error rate (0.0 to 1.0)
memcache_error_rate

# Circuit breaker state (0=closed, 1=half-open, 2=open)
memcache_circuit_breaker_state{server}

# Pool connections by state
memcache_pool_connections{server, state="total|active|idle"}
```

### Scenario Metrics (from scenariod)

```promql
# Current phase (0=idle, 1=stabilization, 2=testing, 3=recovery)
scenario_phase{scenario}

# Toxic state (0=inactive, 1=active)
toxiproxy_toxic_active{server, type="packet_loss|latency"}

# Toxic configuration values
toxiproxy_toxic_value{server, type, param}
```

See full metrics reference in the [complete documentation](#metrics-reference) below.

## Development

### Adding New Scenarios

1. Create a new suite file in `scenarios/`:
   ```go
   type MyScenarioSuite struct {
       metrics *promexporter.ScenarioMetrics
   }

   func (s *MyScenarioSuite) CreateScenarios() []Scenario {
       // Return list of scenarios using PhasedScenarioConfig
   }
   ```

2. Register in `cmd/scenariod/main.go`:
   ```go
   mySuite := scenarios.NewMyScenarioSuite(exporter.ScenarioMetrics())
   for _, s := range mySuite.CreateScenarios() {
       allScenarios[s.Name()] = s
   }
   ```

## Remote Memcache Setup

You can run memcache nodes on a remote server (useful for testing with realistic network conditions or using Podman):

1. **Start memcache on remote server:**
   ```bash
   # Copy and run the memcache-only compose file
   scp docker-compose-memcache.yml user@remote-host:~/
   ssh user@remote-host
   podman-compose -f docker-compose-memcache.yml up -d
   ```

2. **Configure local environment:**
   ```bash
   # Point to your remote server
   export MEMCACHE_HOST=remote-host.example.com

   # Start local infrastructure (toxiproxy, prometheus, grafana)
   docker compose up -d toxiproxy prometheus grafana

   # Run loadgen/scenariod normally - they'll connect to remote memcache
   go run ./cmd/loadgen -concurrency 100
   ```

See [REMOTE_MEMCACHE.md](REMOTE_MEMCACHE.md) for detailed setup instructions.

## Ports Reference

| Service | Port | Purpose |
|---------|------|---------|
| Load Generator | 9090 | Prometheus metrics |
| Scenario Controller | 9092 | Prometheus metrics |
| Prometheus | 9091 | Web UI and API |
| Grafana | 3000 | Dashboards |
| Toxiproxy API | 8474 | Failure injection control |
| Memcache (proxied) | 21211-21213 | Via toxiproxy |
| Memcache (direct) | 11211-11213 | When running remotely |

## Further Reading

- [PHASE1_IMPLEMENTATION.md](PHASE1_IMPLEMENTATION.md) - Phase 1 technical details
- [PHASE2_COMPLETE.md](PHASE2_COMPLETE.md) - Scenario controller implementation
- [PHASE3_COMPLETE.md](PHASE3_COMPLETE.md) - Multi-failure scenarios
- [PROMETHEUS_MIGRATION_PLAN.md](PROMETHEUS_MIGRATION_PLAN.md) - Full migration plan

## Architecture Decisions

### Why Two Processes?

- **Independence**: Load generator runs continuously, scenarios run on-demand
- **Isolation**: Scenario failures don't affect load generation
- **Simplicity**: Each binary has a single responsibility

### Why Prometheus/Grafana?

- **Industry Standard**: Familiar to most developers
- **Time-Series Data**: Perfect for observing behavior over time
- **Query Language**: PromQL enables complex analysis
- **Persistence**: Historical data for comparison

### Why 3-Phase Scenarios?

- **Consistency**: Same structure makes results comparable
- **Baseline**: Stabilization phase establishes normal behavior
- **Recovery**: Tests client's ability to heal after failure

## Credits

Built with:
- [Prometheus](https://prometheus.io/) - Metrics collection
- [Grafana](https://grafana.com/) - Visualization
- [Toxiproxy](https://github.com/Shopify/toxiproxy) - Failure injection
- [gobreaker](https://github.com/sony/gobreaker) - Circuit breaker pattern
