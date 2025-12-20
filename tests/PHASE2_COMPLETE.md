# Phase 2: Scenario Controller - COMPLETE ✅

## What's New

Phase 2 adds a **standalone scenario controller** that orchestrates failure scenarios with structured 3-phase execution and scenario state metrics.

## Architecture

```
┌──────────────────┐         ┌─────────────────────┐
│  Load Generator  │         │ Scenario Controller │
│    (loadgen)     │         │    (scenariod)      │
│                  │         │                     │
│ - Client metrics │         │ - Scenario phases   │
│ - :9090/metrics  │         │ - Toxic state       │
└────────┬─────────┘         │ - :9092/metrics     │
         │                   └──────────┬──────────┘
         │                              │
         └──────────┬───────────────────┘
                    │
         ┌──────────▼────────────┐
         │     Prometheus        │
         │  Scrapes both every 1s│
         └──────────┬────────────┘
                    │
         ┌──────────▼────────────┐
         │      Grafana          │
         │   Unified dashboard   │
         └───────────────────────┘
```

## Files Created

### Core Implementation
- `internal/promexporter/scenario_metrics.go` - Scenario state metrics
- `scenarios/phased_scenario.go` - 3-phase scenario base
- `scenarios/packet_loss_suite.go` - Packet loss scenarios
- `scenarios/latency_suite.go` - Latency scenarios
- `cmd/scenariod/main.go` - Scenario controller binary

### Configuration
- `prometheus/prometheus.yml` - Updated with scenariod scrape target

## Available Scenarios

### Packet Loss Scenarios (6 scenarios)
All apply packet loss to **one server for 1 minute** with 3-phase structure:

- `packet-loss-2-pct` - 2% packet loss
- `packet-loss-5-pct` - 5% packet loss
- `packet-loss-10-pct` - 10% packet loss
- `packet-loss-20-pct` - 20% packet loss
- `packet-loss-50-pct` - 50% packet loss
- `packet-loss-100-pct` - 100% packet loss (complete network partition)

### Latency Scenarios (6 scenarios)
All add **+200ms latency** to one server for varying durations:

- `latency-200ms-100ms` - 100ms duration (very brief)
- `latency-200ms-1s` - 1 second duration
- `latency-200ms-5s` - 5 seconds duration
- `latency-200ms-10s` - 10 seconds duration
- `latency-200ms-40s` - 40 seconds duration
- `latency-200ms-2m` - 2 minutes duration

**Total: 12 scenarios** available

## 3-Phase Scenario Structure

Every scenario follows this pattern:

```
┌─────────────────┐
│ 1. Stabilization│ (30 seconds)
│    - No toxics  │
│    - Baseline   │
└────────┬────────┘
         │
┌────────▼────────┐
│ 2. Testing      │ (varies by scenario)
│    - Apply toxic│
│    - Observe    │
└────────┬────────┘
         │
┌────────▼────────┐
│ 3. Recovery     │ (60 seconds)
│    - Remove toxic│
│    - Observe heal│
└─────────────────┘
```

## Scenario Metrics Exported

### Phase Tracking
- `scenario_phase{scenario}` - Current phase (0=idle, 1=stab, 2=test, 3=recovery)
- `scenario_phase_duration_seconds{scenario,phase}` - Phase durations
- `scenario_runs_total{scenario,status}` - Success/failure counter
- `scenario_active` - Whether any scenario is running (0/1)

### Toxic State
- `toxiproxy_toxic_active{server,type}` - Toxic active/inactive (0/1)
- `toxiproxy_toxic_value{server,type,param}` - Toxic configuration values
  - For packet loss: `param="rate"`, value 0.0-1.0
  - For latency: `param="latency_ms"`, value in milliseconds

## Usage

### List Available Scenarios
```bash
cd tests
go run ./cmd/scenariod -list
```

### Run Single Scenario
```bash
# Run one packet loss scenario
go run ./cmd/scenariod -scenario packet-loss-10-pct

# Run one latency scenario
go run ./cmd/scenariod -scenario latency-200ms-5s
```

### Run All Scenarios
```bash
# Run all 12 scenarios sequentially
go run ./cmd/scenariod -scenario all

# Run all scenarios in a loop
go run ./cmd/scenariod -scenario all -loop
```

### Run with Load Generator
```bash
# Terminal 1: Start load generator
go run ./cmd/loadgen -concurrency 100

# Terminal 2: Run scenarios
go run ./cmd/scenariod -scenario all
```

## Metrics Ports

- **Load Generator**: :9090/metrics
- **Scenario Controller**: :9092/metrics
- **Prometheus**: :9091 (web UI + API)
- **Grafana**: :3000

## Example Workflow

1. **Start infrastructure:**
   ```bash
   docker compose up -d
   ```

2. **Start load generator** (Terminal 1):
   ```bash
   cd tests
   go run ./cmd/loadgen -concurrency 100 -workload mixed
   ```

3. **Start scenario controller** (Terminal 2):
   ```bash
   cd tests
   go run ./cmd/scenariod -scenario packet-loss-10-pct
   ```

4. **Watch Grafana:**
   - Open http://localhost:3000/d/memcache-client-perf
   - Observe the 3 phases:
     - **0-30s**: Stabilization (metrics stable)
     - **30s-90s**: Testing with 10% packet loss
     - **90s-150s**: Recovery phase

## Observing Scenario Phases in Grafana

You can add panels to visualize scenario state:

### Scenario Phase Panel
```promql
scenario_phase{scenario!=""}
```
Shows current phase for active scenario (0/1/2/3)

### Toxic Active Panel
```promql
toxiproxy_toxic_active{server!=""}
```
Shows which servers have active toxics (0 or 1)

### Packet Loss Rate Panel
```promql
toxiproxy_toxic_value{param="rate"}
```
Shows current packet loss rate (0.0 to 1.0)

### Latency Panel
```promql
toxiproxy_toxic_value{param="latency_ms"}
```
Shows added latency in milliseconds

## What Each Scenario Tests

### Packet Loss Scenarios
Test circuit breaker behavior under packet loss:
- **2-5%**: Should handle gracefully, minor error rate increase
- **10-20%**: Circuit breaker may trip, significant impact
- **50-100%**: Circuit breaker should trip immediately, full failover

### Latency Scenarios
Test timeout and circuit breaker behavior:
- **100ms**: Very brief, tests rapid recovery
- **1-5s**: Short duration, tests timeout handling
- **10-40s**: Medium duration, tests sustained degradation
- **2min**: Long duration, tests extended recovery

## Next Steps

Want to add the scenario phase visualization to Grafana dashboard? Let me know and I can:
1. Add a "Scenario Phase" panel showing current phase
2. Add "Toxic State" panels showing active toxics
3. Add annotations for phase transitions

Or you can test the scenarios now and see the metrics in Prometheus Explore!
