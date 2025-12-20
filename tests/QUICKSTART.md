# Quick Start Guide - Phase 1

## What's New

The reliability testing framework now uses **Prometheus** for metrics collection and **Grafana** for visualization, replacing the previous TUI approach.

## Architecture

```
Load Generator (Go)  →  Prometheus  →  Grafana Dashboard
  :9090/metrics         :9091           :3000
```

## Quick Start

### 1. Start Infrastructure

Start all docker services (memcache, toxiproxy, prometheus, grafana):

```bash
cd tests
docker compose up -d
```

Verify services are running:

```bash
docker compose ps
```

You should see:
- 3 memcache nodes (healthy)
- toxiproxy
- **prometheus** (new!)
- **grafana** (new!)

### 2. Start Load Generator

Run the load generator to generate traffic and export metrics:

```bash
cd tests
go run ./cmd/loadgen -concurrency 100 -workload mixed
```

**Flags:**
- `-concurrency N` - Number of concurrent workers (default: 100)
- `-workload NAME` - Workload pattern: `mixed`, `get-heavy`, or `set-heavy` (default: mixed)
- `-metrics-port PORT` - Metrics endpoint port (default: :9090)

### 3. Access Dashboards

**Prometheus:**
- URL: http://localhost:9091
- Check targets: http://localhost:9091/targets
  - Should show `loadgen` target as UP
- Run queries: e.g., `memcache_operations_per_second`

**Grafana:**
- URL: http://localhost:3000
- Login: `admin` / `admin`
- Dashboard: [Memcache Client Performance](http://localhost:3000/d/memcache-client-perf/memcache-client-performance)

**Direct Metrics:**
- Load generator metrics: http://localhost:9090/metrics

### 4. View Real-Time Data

Open the Grafana dashboard to see:
- **Operations per Second** - Real-time throughput
- **Error Rate** - Percentage of failed operations
- **Circuit Breaker States** - State of each server's circuit breaker (0=closed, 1=half-open, 2=open)
- **Pool Connections** - Active, idle, and total connections per server
- **Circuit Breaker Failures** - Failure counts per server

## Available Metrics

The load generator exports these Prometheus metrics:

### Operation Metrics
- `memcache_operations_per_second` - Current ops/sec
- `memcache_error_rate` - Error rate (0.0 to 1.0)
- `memcache_operations_total{status="success|failed"}` - Counter

### Circuit Breaker Metrics
- `memcache_circuit_breaker_state{server}` - State (0/1/2)
- `memcache_circuit_breaker_transitions_total{server,from,to}` - Transition counter
- `memcache_circuit_breaker_requests{server}` - Request count
- `memcache_circuit_breaker_failures{server,type}` - Failure counts

### Pool Metrics
- `memcache_pool_connections{server,state}` - Connections by state
- `memcache_pool_connections_created{server}` - Created count
- `memcache_pool_acquire_errors{server}` - Acquire error count

## Testing Circuit Breaker Behavior

To see the circuit breaker in action:

1. **Start the load generator**
2. **Open Grafana dashboard**
3. **Stop one memcache node:**
   ```bash
   docker stop memcache_node1
   ```
4. **Watch the dashboard:**
   - Error rate should increase
   - Circuit breaker for that server should open (state → 2)
   - Connections should drop for that server
5. **Restart the node:**
   ```bash
   docker start memcache_node1
   ```
6. **Watch recovery:**
   - Circuit breaker should go: open → half-open → closed
   - Connections should rebuild
   - Error rate should drop to zero

## Workload Patterns

### Mixed (default)
- 60% GET
- 30% SET
- 5% DELETE
- 5% INCREMENT
- Includes hot keys (30% chance)

### Get-Heavy
- 95% GET
- 5% SET

### Set-Heavy
- 20% GET
- 80% SET

## Troubleshooting

### Load generator can't connect to memcache

Check toxiproxy is healthy:
```bash
curl http://localhost:8474/proxies
```

If empty, proxies weren't created. The load generator's testutils package should auto-create them.

### Prometheus not scraping metrics

1. Check load generator is running and metrics are available:
   ```bash
   curl http://localhost:9090/metrics
   ```

2. Check Prometheus targets:
   http://localhost:9091/targets

3. **macOS/Windows users:** The config uses `host.docker.internal`. This should work automatically with Docker Desktop.

4. **Linux users:** Edit `prometheus/prometheus.yml` and change:
   ```yaml
   - targets: ['host.docker.internal:9090']
   ```
   to:
   ```yaml
   - targets: ['172.17.0.1:9090']
   ```
   Then restart prometheus: `docker compose restart prometheus`

### Grafana dashboard is blank

1. Check datasource is configured:
   http://localhost:3000/datasources

2. Check dashboard queries work in Prometheus first:
   http://localhost:9091

3. Ensure load generator has been running for at least 10 seconds to collect initial data

## Stopping Everything

```bash
# Stop load generator: Ctrl+C

# Stop docker services
docker compose down
```

## What's Next

**Phase 1** ✅ Complete - Basic infrastructure working

**Phase 2** (upcoming):
- Scenario controller process
- 3-phase scenario structure (stabilization → testing → recovery)
- Enhanced scenarios (packet loss rates, latency injection)
- Scenario state metrics
- Grafana annotations for scenario phases

**Phase 3** (upcoming):
- Advanced scenarios (bandwidth throttling, split-brain, etc.)
- Enhanced dashboards with detailed breakdowns
- Latency histogram metrics

---

For detailed implementation information, see:
- [PROMETHEUS_MIGRATION_PLAN.md](PROMETHEUS_MIGRATION_PLAN.md)
- [PHASE1_IMPLEMENTATION.md](PHASE1_IMPLEMENTATION.md)
