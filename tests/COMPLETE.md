# Prometheus/Grafana Reliability Testing - COMPLETE ✅

All phases of the Prometheus/Grafana migration are now complete!

## What Was Built

A production-ready reliability testing framework with:
- **2 independent processes** (load generator + scenario controller)
- **14 failure scenarios** covering realistic production failures
- **Prometheus metrics collection** with 1-second granularity
- **Grafana dashboards** for real-time visualization
- **3-phase scenario structure** for consistent testing
- **Docker Compose orchestration** for easy deployment

## Migration Summary

### Phase 1: Foundation ✅
- Added Prometheus and Grafana to docker-compose
- Created load generator binary (`cmd/loadgen`)
- Implemented Prometheus metrics exporter
- Created basic Grafana dashboard
- Achieved sub-second observability (1s refresh throughout)

### Phase 2: Scenario Controller ✅
- Created scenario controller binary (`cmd/scenariod`)
- Implemented 3-phase scenario pattern
- Created 12 initial scenarios:
  - 6 packet loss scenarios (2%, 5%, 10%, 20%, 50%, 100%)
  - 6 latency scenarios (+200ms for varying durations)
- Added scenario state metrics
- Added scenario phase visualization to Grafana

### Phase 3: Multi-Failure Scenarios ✅
- Implemented 2 multi-failure scenarios:
  - 2 servers with 100% packet loss
  - 2 servers with +500ms latency
- Tests extreme degradation (only 1 out of 3 servers healthy)
- Enhanced toxiproxy cleanup for scenario phases
- Added CPU core limits (loadgen: 4 cores, scenariod: 2 cores)

### Phase 4: Documentation ✅
- Comprehensive README with quick start guide
- Troubleshooting section
- Metrics reference
- Architecture decision documentation
- Phase completion documents (PHASE1, PHASE2, PHASE3)

## Removed

Old TUI implementation files:
- `tui/dashboard.go` - TUI dashboard code
- `main.go` - Old monolithic main.go

The framework is now fully migrated to Prometheus/Grafana with no TUI dependencies.

## Quick Start

```bash
# 1. Start infrastructure
cd tests && docker compose up -d

# 2. Start load generator
go run ./cmd/loadgen -concurrency 100

# 3. Open Grafana
open http://localhost:3000/d/memcache-client-perf
# Login: admin/admin

# 4. Run scenarios
go run ./cmd/scenariod -scenario all
```

## Available Scenarios

**Total: 14 scenarios**

### Single-Server Failures (12 scenarios)
- Packet loss: 2%, 5%, 10%, 20%, 50%, 100%
- Latency: +200ms for 100ms, 1s, 5s, 10s, 40s, 2m

### Multi-Server Failures (2 scenarios)
- 100% packet loss on 2 out of 3 servers
- +500ms latency on 2 out of 3 servers

## Architecture

```
┌─────────────────┐         ┌──────────────────┐
│ Load Generator  │         │ Scenario         │
│   (loadgen)     │         │ Controller       │
│                 │         │ (scenariod)      │
│ - Client metrics│         │ - Scenario state │
│ - :9090/metrics │         │ - :9092/metrics  │
└────────┬────────┘         └────────┬─────────┘
         │                           │
         └─────────┬─────────────────┘
                   │
        ┌──────────▼────────────┐
        │     Prometheus        │
        │   Scrapes every 1s    │
        └──────────┬────────────┘
                   │
        ┌──────────▼────────────┐
        │       Grafana         │
        │   Real-time dashboard │
        └───────────────────────┘
```

## Key Features

### Observability
- **Sub-second visibility**: All metrics scraped and refreshed every 1 second
- **Real-time dashboard**: Grafana updates continuously
- **Scenario timeline**: Visual representation of test phases
- **Comprehensive metrics**: Client behavior + scenario state + toxic configuration

### Reliability
- **Process isolation**: Load generation independent from scenario execution
- **Clean state management**: Toxiproxy reset before each scenario phase
- **Resource limits**: CPU core caps prevent resource exhaustion
- **Graceful shutdown**: Proper cleanup on interruption

### Extensibility
- **Simple scenario creation**: Use `PhasedScenarioConfig` pattern
- **Flexible metrics**: Easy to add new metrics via promexporter
- **Modular suites**: Each scenario type in its own suite
- **Standard tools**: Use `strings.HasPrefix` and stdlib patterns

## Metrics Exported

### Client Metrics (from loadgen)
- Operations per second
- Error rate
- Circuit breaker states (per server)
- Pool connections (total, active, idle per server)
- Circuit breaker transitions and failures

### Scenario Metrics (from scenariod)
- Current scenario phase (idle/stabilization/testing/recovery)
- Phase durations
- Toxic active state (per server, per type)
- Toxic configuration values (packet loss rate, latency ms)

## Documentation

- **[README.md](README.md)** - Main documentation and quick start
- **[PHASE1_IMPLEMENTATION.md](PHASE1_IMPLEMENTATION.md)** - Phase 1 technical details
- **[PHASE2_COMPLETE.md](PHASE2_COMPLETE.md)** - Scenario controller
- **[PHASE3_COMPLETE.md](PHASE3_COMPLETE.md)** - Multi-failure scenarios
- **[PROMETHEUS_MIGRATION_PLAN.md](PROMETHEUS_MIGRATION_PLAN.md)** - Original migration plan

## Success Metrics - All Achieved ✅

- ✅ 2 independent Go binaries (loadgen, scenariod)
- ✅ 14 well-tested scenarios
- ✅ Comprehensive Grafana dashboards
- ✅ Complete docker-compose setup
- ✅ Documentation for usage and extension
- ✅ No TUI code remaining
- ✅ Sub-second observability
- ✅ CPU resource management
- ✅ Clean scenario state management

## What's Next

The framework is production-ready and can be used for:

1. **Continuous testing**: Run loadgen + scenarios in CI
2. **Baseline establishment**: Collect metrics to establish normal behavior
3. **Regression detection**: Compare metrics across client versions
4. **Scenario expansion**: Add new scenarios as needed
5. **Alert configuration**: Add Prometheus alerts for anomalies

## Credits

Built with:
- [Prometheus](https://prometheus.io/) - Metrics collection
- [Grafana](https://grafana.com/) - Visualization
- [Toxiproxy](https://github.com/Shopify/toxiproxy) - Failure injection
- [gobreaker](https://github.com/sony/gobreaker) - Circuit breaker
- [puddle](https://github.com/jackc/puddle) - Connection pooling
