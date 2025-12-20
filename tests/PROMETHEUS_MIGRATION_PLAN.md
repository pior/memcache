# Prometheus/Grafana Migration Plan

## Overview

Redesign the reliability testing framework to use Prometheus for metrics collection and Grafana for visualization, replacing the current TUI approach. The system will consist of two independent processes that emit metrics to Prometheus.

## Current State Analysis

### Existing Infrastructure (Reusable)
- ✅ Docker Compose with 3 memcache nodes + toxiproxy
- ✅ Scenario system with registry pattern
- ✅ Workload generation with runner
- ✅ Metrics collection logic
- ✅ Circuit breaker state change tracking

### To Be Replaced
- ❌ TUI dashboard (termui-based)
- ❌ In-memory metrics storage
- ❌ Monolithic main.go that does everything
- ❌ Simple scenario timing (need structured 3-phase approach)

## Architecture Design

### Two Process Architecture

```
┌─────────────────────┐         ┌──────────────────┐
│ Scenario Controller │         │ Load Generator   │
│                     │         │                  │
│ - Controls toxiproxy│         │ - Runs workload  │
│ - Orchestrates      │         │ - Emits client   │
│   scenarios         │         │   metrics        │
│ - Emits scenario    │         │                  │
│   state metrics     │         │                  │
└─────────┬───────────┘         └────────┬─────────┘
          │                              │
          │    ┌─────────────────┐       │
          └───►│   Prometheus    │◄──────┘
               │                 │
               └────────┬────────┘
                        │
               ┌────────▼────────┐
               │    Grafana      │
               │   (Dashboards)  │
               └─────────────────┘
```

### Process 1: Load Generator (`loadgen`)

**Responsibilities:**
- Run continuous memcache workload
- Export Prometheus metrics at `/metrics` endpoint
- Independent of scenario execution
- Configurable workload patterns

**Metrics to Export:**
- `memcache_operations_total{status="success|failed"}` - Counter
- `memcache_operations_rate` - Gauge (ops/sec)
- `memcache_error_rate` - Gauge (percentage)
- `memcache_circuit_breaker_state{server}` - Gauge (0=closed, 1=half-open, 2=open)
- `memcache_circuit_breaker_transitions_total{server, from, to}` - Counter
- `memcache_pool_connections{server, state="total|active|idle"}` - Gauge
- `memcache_pool_created_total{server}` - Counter
- `memcache_pool_acquire_errors_total{server}` - Counter
- `memcache_circuit_breaker_requests{server}` - Gauge
- `memcache_circuit_breaker_failures{server, type="total|consecutive"}` - Gauge

**Configuration:**
- Listen port for metrics (default: 9090)
- Workload type (mixed, get-heavy, set-heavy)
- Concurrency level (workers)
- Memcache server addresses (via toxiproxy)

### Process 2: Scenario Controller (`scenariod`)

**Responsibilities:**
- Execute scenarios with 3-phase structure
- Control toxiproxy to inject failures
- Export scenario state metrics
- Can run scenarios sequentially or individually

**Metrics to Export:**
- `scenario_phase{scenario}` - Gauge (0=idle, 1=stabilization, 2=testing, 3=recovery)
- `scenario_phase_duration_seconds{scenario, phase}` - Gauge
- `scenario_runs_total{scenario, status="success|failed"}` - Counter
- `toxiproxy_toxic_active{server, type}` - Gauge (0=inactive, 1=active)
- `toxiproxy_toxic_config{server, type, param}` - Gauge (e.g., packet_loss_rate, latency_ms)

**3-Phase Scenario Structure:**
```
1. Stabilization (30s)
   - No toxics applied
   - Allow client to reach steady state
   - Emit phase=1 metric

2. Testing (variable duration)
   - Apply toxic to one server
   - Emit phase=2 + toxic metrics
   - Duration depends on scenario type

3. Recovery (60s)
   - Remove toxic
   - Emit phase=3 metric
   - Observe client recovery
```

**Configuration:**
- Listen port for metrics (default: 9091)
- Toxiproxy API endpoint
- Scenario to run (or "all" for sequential)
- Loop mode (run continuously or N times)

## Implementation Phases

### Phase 1: Foundation (First Deliverable) 🎯

**Goal:** Establish Prometheus/Grafana infrastructure with basic load generator

**Deliverables:**
1. Update `docker-compose.yml` to add Prometheus and Grafana services
2. Create Prometheus configuration (`prometheus.yml`)
3. Create basic load generator process (`cmd/loadgen/main.go`)
   - Reuse existing workload runner
   - Implement Prometheus metrics exporter
   - HTTP server for `/metrics` endpoint
4. Verify metrics flow: loadgen → Prometheus → Grafana
5. Create basic Grafana dashboard (manual JSON)

**Files to Create:**
- `tests/docker-compose.yml` (modify existing)
- `tests/prometheus/prometheus.yml`
- `tests/grafana/dashboards/client-performance.json`
- `tests/grafana/provisioning/datasources/prometheus.yml`
- `tests/grafana/provisioning/dashboards/dashboard.yml`
- `tests/cmd/loadgen/main.go`
- `tests/internal/promexporter/client_metrics.go`

**Files to Reuse:**
- `tests/workload/workload.go` - ✅ Ready to use
- `tests/workload/mixed.go` - ✅ Ready to use
- `tests/testutils/setup.go` - ✅ Ready to use

**Success Criteria:**
- ✅ Load generator runs and emits metrics
- ✅ Prometheus scrapes metrics successfully
- ✅ Grafana displays operations/sec and error rate
- ✅ Circuit breaker states visible in Grafana

**Estimated Complexity:** Medium
- Prometheus client library is straightforward
- Docker compose setup is simple
- Grafana dashboard JSON can be basic initially

---

### Phase 2: Scenario Controller

**Goal:** Build scenario orchestration process with 3-phase structure

**Deliverables:**
1. Create scenario controller process (`cmd/scenariod/main.go`)
2. Refactor scenarios to use 3-phase pattern
3. Implement scenario state metrics exporter
4. Create new scenarios per requirements:
   - Packet loss scenarios (2%, 5%, 10%, 20%, 50%, 100%)
   - Network latency scenarios (100ms, 1s, 5s, 10s, 40s, 2min)
5. Update Grafana dashboard to show scenario phases

**Files to Create:**
- `tests/cmd/scenariod/main.go`
- `tests/internal/promexporter/scenario_metrics.go`
- `tests/scenarios/phased_scenario.go` (base implementation)
- `tests/scenarios/packet_loss_suite.go`
- `tests/scenarios/latency_suite.go`

**Files to Modify:**
- `tests/scenarios/scenario.go` - Add phase callbacks
- `tests/grafana/dashboards/client-performance.json` - Add scenario annotations

**Success Criteria:**
- ✅ Scenario controller runs independently
- ✅ Can see scenario phases in Grafana
- ✅ Toxic configurations visible in metrics
- ✅ Phases align with observed client behavior

**Estimated Complexity:** Medium-High
- Scenario refactoring requires careful design
- Ensuring clean toxic lifecycle is critical
- Metric synchronization needs testing

---

### Phase 3: Advanced Scenarios & Real-World Patterns

**Goal:** Implement comprehensive scenario suite

**Deliverables:**
1. Additional real-world scenarios:
   - Bandwidth throttling
   - Connection limits
   - Intermittent failures (flaky network)
   - Split-brain/partition scenarios
   - Multiple simultaneous failures
2. Advanced Grafana dashboards:
   - Per-server breakdown
   - Circuit breaker state timeline
   - Latency percentiles (if we add histogram metrics)
3. Scenario composition (run multiple scenarios in sequence)

**Files to Create:**
- `tests/scenarios/bandwidth_throttle.go`
- `tests/scenarios/connection_limit.go`
- `tests/scenarios/intermittent.go`
- `tests/scenarios/partition.go`
- `tests/scenarios/multi_failure.go`
- `tests/grafana/dashboards/advanced-analysis.json`

**Success Criteria:**
- ✅ 10+ scenarios covering diverse failure modes
- ✅ Grafana dashboards provide deep insights
- ✅ Can correlate client behavior with scenario phases
- ✅ Documentation for interpreting results

**Estimated Complexity:** Medium
- Building on established patterns
- More scenarios = more testing required

---

### Phase 4: Polish & Documentation

**Goal:** Production-ready reliability testing framework

**Deliverables:**
1. Comprehensive README for new architecture
2. Dashboard export/import documentation
3. Example analysis workflows
4. Performance baseline documentation
5. CI integration (optional)

**Files to Create/Update:**
- `tests/README.md` - Complete rewrite
- `tests/docs/GETTING_STARTED.md`
- `tests/docs/SCENARIO_GUIDE.md`
- `tests/docs/INTERPRETING_RESULTS.md`
- `tests/docs/ADDING_SCENARIOS.md`

**Success Criteria:**
- ✅ Anyone can run the test suite
- ✅ Clear documentation for adding scenarios
- ✅ Dashboard templates ready to use
- ✅ Baseline performance metrics documented

**Estimated Complexity:** Low
- Mostly documentation work
- Cleanup and refinement

## Technical Decisions

### Prometheus Client Library
Use `github.com/prometheus/client_golang` - official Go client, well-documented

### Metric Naming Convention
Follow Prometheus best practices:
- `memcache_*` prefix for client metrics
- `scenario_*` prefix for scenario state
- `toxiproxy_*` prefix for toxic state
- Units in suffix: `_total`, `_seconds`, `_bytes`
- Labels for dimensions: server, status, phase, etc.

### Grafana Dashboard Provisioning
Use file-based provisioning for version control:
- Datasource in `grafana/provisioning/datasources/`
- Dashboards in `grafana/provisioning/dashboards/`
- JSON definitions in `grafana/dashboards/`

### Process Communication
- No direct communication between processes
- All coordination via Prometheus metrics
- Load generator runs independently
- Scenario controller is optional (can run loadgen alone)

### Configuration Management
- Use flags for process configuration
- Environment variables for docker-compose
- YAML config files for complex scenarios (future)

## Migration Strategy

### Backward Compatibility
- Keep existing code temporarily
- Create new `cmd/` directory for new binaries
- Old `main.go` can coexist during transition
- Remove TUI code after Phase 1 is validated

### Testing Approach
1. Validate Phase 1 thoroughly before continuing
2. Compare metrics accuracy with old TUI system
3. Ensure circuit breaker behavior is identical
4. Performance testing: loadgen should not add overhead

### Rollback Plan
- Git branch for migration
- Keep old code until full validation
- Document any behavioral differences
- User acceptance before removing old code

## Open Questions for User

1. **Grafana Dashboard Preferences:**
   - Auto-refresh rate (5s, 10s, 30s)?
   - Time range default (5min, 15min, 1hour)?
   - Panel layout preferences?

2. **Scenario Execution:**
   - Should scenariod support running scenarios in parallel?
   - Should there be a "scenario playlist" feature?

3. **Metric Granularity:**
   - Do we need latency histograms (p50, p95, p99)?
   - Per-operation metrics (Get/Set/Delete separately)?

4. **Deployment:**
   - Should we create a Makefile for common tasks?
   - Docker images for loadgen/scenariod?

## Success Metrics

After completion, we should have:
- 🎯 2 independent Go binaries (loadgen, scenariod)
- 🎯 10+ well-tested scenarios
- 🎯 Comprehensive Grafana dashboards
- 🎯 Complete docker-compose setup
- 🎯 Documentation for usage and extension
- 🎯 No TUI code remaining
- 🎯 Zero performance regression vs. old system

## First Deliverable Focus

**Phase 1 is the critical path:**
1. Get Prometheus/Grafana running in docker-compose
2. Build basic load generator with metrics export
3. Validate metrics in Grafana
4. Ensure circuit breaker states are correct

**This establishes the foundation for all subsequent work.**

Once Phase 1 is approved and working, Phases 2-4 are straightforward iterations.
