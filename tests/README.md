# Memcache Client Reliability Testing

## Objective

Validate the memcache client's resilience and recovery capabilities under realistic network failure conditions. The client should **never enter a bad state** and **always recover** once the platform recovers.

## What We Test

### Core Components Under Test

1. **Circuit Breaker** ([gobreaker](https://github.com/sony/gobreaker))
   - Proper state transitions (closed → open → half-open → closed)
   - Failure detection and recovery timing
   - Correct request handling in each state
   - Metrics accuracy during transitions

2. **Connection Pool** ([puddle](https://github.com/jackc/puddle) & channel-based)
   - Connection lifecycle management under failures
   - Idle/active connection handling during network issues
   - Pool exhaustion and recovery
   - Health check effectiveness during degradation
   - Connection deadline and timeout behavior

3. **Multi-Server Support**
   - Server selection consistency during partial failures
   - Failover behavior when nodes become unavailable
   - Load distribution during degraded states
   - Recovery when failed nodes return

4. **Statistics & Observability**
   - Metric accuracy during failures
   - Circuit breaker counts correctness
   - Pool stats reliability under stress
   - Real-time observability during chaos

## Failure Scenarios

### Network-Level Failures
- **Brief packet drop** (50ms-1s) - transient network glitch
- **Total packet drop** (10s+) - complete network partition
- **Packet loss** (5%, 10%, 25%) - degraded network quality
- **Latency injection** (100ms, 500ms, 1s) - slow network
- **Bandwidth throttling** - constrained network capacity
- **Connection timeout** - stuck connections
- **Slow close** - delayed connection teardown

### Platform-Level Failures
- **Single node failure** (1 of 3 nodes) - partial availability
- **Majority failure** (2 of 3 nodes) - quorum loss
- **Complete failure** (all nodes) - total outage
- **Rolling failures** - cascading node failures
- **Flapping nodes** - nodes going up/down repeatedly
- **Split brain** - network partitions between nodes

### Combined Scenarios
- High concurrency + network latency
- Connection pool exhaustion + node failure
- Circuit breaker tripping + partial outage
- Recovery under continuous load

## Success Criteria

### Resilience Requirements

1. **No Permanent Failures**
   - Client recovers automatically when platform recovers
   - No manual intervention required
   - No stuck connections or goroutine leaks

2. **Graceful Degradation**
   - Requests fail fast when nodes are down
   - Circuit breaker prevents cascading failures
   - Connection pool doesn't exhaust during failures

3. **Correct Behavior**
   - Operations return appropriate errors (not timeouts when circuit is open)
   - Pool stats accurately reflect state
   - Circuit breaker metrics match actual behavior
   - No panics or crashes under any scenario

4. **Performance Under Load**
   - Sustains 100+ concurrent operations
   - Acceptable latency percentiles (p50, p95, p99)
   - Minimal performance degradation during partial failures

5. **Observable State**
   - Stats remain accessible during failures
   - Circuit breaker state accurately reflects reality
   - Pool metrics update in real-time

## Workload Patterns

### Standard Patterns
- **Constant load** - steady request rate
- **Burst traffic** - sudden spikes
- **Ramp-up** - gradually increasing load
- **Mixed operations** - get/set/delete/increment distribution

### Realistic Patterns
- **Cache stampede** - many requests for same missing key
- **Hot keys** - skewed access pattern
- **Batch operations** - multi-get scenarios
- **Long-tail latency** - occasional slow requests

## Test Infrastructure

- **Docker Compose** - orchestrate 3 memcache nodes + toxiproxy
- **Toxiproxy** - inject network failures via proxy layer
- **TUI Dashboard** - real-time visualization with sparklines (200ms refresh, 5 updates/sec)
- **High concurrency** - 100-1000 concurrent operations
- **Continuous mode** - run indefinitely for soak testing
- **Scenario mode** - run specific failure scenarios

## Expected Outcomes

After running reliability tests, we should observe:

1. **Circuit breaker opens** when failure threshold is reached
2. **Pool connections are cleaned up** after network failures
3. **Client recovers within seconds** when nodes return
4. **Stats remain consistent** throughout failures
5. **No goroutine or connection leaks** after any scenario
6. **Predictable error types** for each failure mode

## Usage

### TUI Mode (Default)

The test runner features a real-time TUI dashboard with:
- **Sparklines** showing ops/sec and error rate trends (smooth, continuous updates)
- **Throughput gauge** visualizing current performance
- **Server pool table** with connection states and circuit breaker status
- **Event log** tracking circuit breaker state transitions
- **200ms refresh rate** (5 updates/sec) synchronized with metrics collection

```bash
# Run with TUI dashboard (default for continuous workload)
cd tests && go run .

# Disable TUI (use plain text output)
cd tests && go run . -no-tui

# Run with specific workload
cd tests && go run . -workload get-heavy

# High concurrency with TUI
cd tests && go run . -concurrency 500
```

### Scenario Mode (Plain Text)

When running scenarios, plain text output is used to show scenario progress:

```bash
# Run specific scenario (auto-disables TUI)
cd tests && go run . -scenario packet-loss

# Run scenario for specific duration
cd tests && go run . -scenario latency -duration 2m

# Scenario with custom workload
cd tests && go run . -scenario single-node-failure -workload set-heavy

# List all scenarios and workloads
cd tests && go run . -list
```

## Metrics Collection

The test runner collects:
- Request latency (p50, p95, p99, max)
- Success/failure rates
- Circuit breaker state changes
- Pool statistics over time
- Error distribution
- Recovery time after failures

## Related Documentation

- [SETUP.md](SETUP.md) - Technical implementation details
- [../README.md](../README.md) - Main memcache client documentation
