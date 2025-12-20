# Phase 3: Multiple Simultaneous Failures - COMPLETE ✅

## What's New

Phase 3 adds **multiple simultaneous failure scenarios** that test how the client handles when 2 out of 3 servers fail at the same time.

## New Scenarios

### Multiple Simultaneous Failures (2 scenarios)

Both scenarios affect **2 out of 3 servers simultaneously** to test client behavior under severe degradation:

- `multi-failure-2-servers-packet-loss` - 100% packet loss on 2 servers for 1 minute
- `multi-failure-2-servers-latency` - +500ms latency on 2 servers for 1 minute

**Total scenarios: 14** (6 packet loss + 6 latency + 2 multi-failure)

## Why These Scenarios Matter

**Single-server failures** (from Phase 2) test basic resilience - the client has 2 healthy servers to fall back on.

**Multi-server failures** test extreme conditions:
- Only 1 out of 3 servers remains healthy
- All traffic must concentrate on the single healthy server
- Tests if circuit breakers trip correctly for multiple servers
- Tests connection pool behavior under severe load concentration
- More realistic than single-server failures in production scenarios

## 3-Phase Structure

Same structured pattern as Phase 2:

```
┌─────────────────┐
│ 1. Stabilization│ (30 seconds)
│    - No toxics  │
│    - 3 healthy  │
└────────┬────────┘
         │
┌────────▼────────┐
│ 2. Testing      │ (1 minute)
│    - 2 servers  │
│      with toxic │
│    - 1 healthy  │
└────────┬────────┘
         │
┌────────▼────────┐
│ 3. Recovery     │ (60 seconds)
│    - All toxics │
│      removed    │
│    - Observe    │
│      healing    │
└─────────────────┘
```

## Usage

### List All Scenarios

```bash
cd tests
go run ./cmd/scenariod -list
```

You'll now see:
```
Multiple Simultaneous Failures:
  multi-failure-2-servers-latency     +500ms latency on 2 out of 3 servers simultaneously for 1 minute
  multi-failure-2-servers-packet-loss 100% packet loss on 2 out of 3 servers simultaneously for 1 minute
```

### Run Multi-Failure Scenarios

```bash
# Run packet loss multi-failure
go run ./cmd/scenariod -scenario multi-failure-2-servers-packet-loss

# Run latency multi-failure
go run ./cmd/scenariod -scenario multi-failure-2-servers-latency

# Run ALL scenarios including multi-failure (all 14 scenarios)
go run ./cmd/scenariod -scenario all
```

### Run with Load Generator

```bash
# Terminal 1: Start load generator
go run ./cmd/loadgen -concurrency 100

# Terminal 2: Run multi-failure scenario
go run ./cmd/scenariod -scenario multi-failure-2-servers-packet-loss
```

## What to Observe in Grafana

### Expected Behavior - Packet Loss Scenario

**Phase 1 (Stabilization):**
- All 3 circuit breakers in "closed" state
- Traffic distributed evenly across 3 servers
- Low error rate

**Phase 2 (Testing - 2 servers with 100% packet loss):**
- 2 circuit breakers should transition: closed → open
- 1 circuit breaker remains closed (healthy server)
- Error rate spikes initially, then recovers
- All traffic concentrates on the 1 healthy server
- Pool connections: 2 servers drop to ~0 active, 1 server increases

**Phase 3 (Recovery):**
- 2 circuit breakers transition: open → half-open → closed
- Traffic gradually redistributes across all 3 servers
- Error rate returns to baseline
- Pool connections rebalance

### Expected Behavior - Latency Scenario

**Phase 2 (Testing - 2 servers with +500ms latency):**
- 2 servers experience high latency
- Circuit breakers may trip depending on timeout settings
- Client should route most traffic to the fast server
- Overall throughput may decrease due to reduced server capacity

## Scenario Phase Visualization

The Grafana dashboard now includes a **Scenario Phase** panel at the top showing:
- Current scenario name in the legend
- Phase transitions (blue → orange → green)
- Toxic state for each server

## Files Created

- `scenarios/multi_failure_suite.go` - Multi-failure scenario implementations

## Files Modified

- `cmd/scenariod/main.go` - Added multi-failure suite registration

## Files Removed

Old TUI implementation (no longer needed):
- `tui/dashboard.go` - Old TUI dashboard
- `main.go` - Old monolithic main.go

## Architecture Improvements

### CPU Core Limits

Both binaries now limit CPU usage:
- **loadgen**: Default 4 cores (configurable via `-max-procs`)
- **scenariod**: Default 2 cores (configurable via `-max-procs`)

This prevents the test infrastructure from consuming all CPU resources.

### Toxiproxy State Management

Enhanced cleanup ensures clean scenario execution:
- Toxiproxy state is reset **before stabilization phase**
- Toxiproxy state is reset **before recovery phase**
- Toxic metrics are zeroed at scenario start
- No leftover state between scenarios

## Next Steps

Phase 4 (Documentation) will add:
- Comprehensive README for the new architecture
- Usage guide and examples
- How to interpret results
- How to add new scenarios

## Current State

✅ **14 total scenarios** covering:
- Single-server packet loss (6 scenarios)
- Single-server latency (6 scenarios)
- Multi-server failures (2 scenarios)

✅ **Comprehensive observability:**
- Client metrics (ops/sec, errors, circuit breakers, pools)
- Scenario metrics (phases, toxic state)
- Grafana dashboard with scenario timeline

✅ **Production-ready infrastructure:**
- Two independent binaries (loadgen, scenariod)
- Prometheus metrics export
- Grafana visualization
- Docker Compose orchestration
- CPU resource limits
