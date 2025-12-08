# Technical Setup Documentation

## Architecture

```
┌─────────────┐
│   Test CLI  │
│  (Go binary)│
└──────┬──────┘
       │
       │ Toxiproxy Go Client API (HTTP :8474)
       ↓
┌──────────────────────────────────────────┐
│            Toxiproxy Container            │
│  ┌────────┐  ┌────────┐  ┌────────┐     │
│  │ Proxy1 │  │ Proxy2 │  │ Proxy3 │     │
│  │ :21211 │  │ :21212 │  │ :21213 │     │
│  └───┬────┘  └───┬────┘  └───┬────┘     │
└──────┼──────────┼──────────┼────────────┘
       │          │          │
       │          │          │
    ┌──▼───┐  ┌──▼───┐  ┌──▼───┐
    │ MC 1 │  │ MC 2 │  │ MC 3 │
    │:11211│  │:11211│  │:11211│
    └──────┘  └──────┘  └──────┘
    Memcache Containers
```

## Components

### 1. Memcache Nodes (3 instances)

- **Image**: `memcached:1.6-alpine`
- **Instances**: 3 independent nodes
- **Config**: Meta protocol enabled, reasonable memory limit (64MB)
- **Network**: Internal docker network
- **Exposed ports**: Only via toxiproxy

### 2. Toxiproxy

- **Image**: `ghcr.io/shopify/toxiproxy:latest`
- **Purpose**: Network failure injection proxy
- **API Port**: 8474 (exposed to host)
- **Proxy Ports**: 21211, 21212, 21213 (exposed to host)
- **Configuration**: Proxies created dynamically via API

#### Toxiproxy Features

**Available Toxics**:
- `latency` - Add delay to connections (upstream/downstream)
- `bandwidth` - Limit bandwidth in KB/s
- `slow_close` - Delay connection closing
- `timeout` - Stop all data and close connection after timeout
- `slicer` - Slice TCP packets into small bits
- `limit_data` - Close connection after transmitting N bytes

**Toxic Attributes**:
- `toxicity` - Probability of toxic being applied (0.0 - 1.0)
- `attributes` - Toxic-specific parameters (latency, jitter, rate, etc.)

### 3. Test CLI (Go)

- **Language**: Go 1.24+
- **Dependencies**:
  - `github.com/pior/memcache` (the client we're testing)
  - `github.com/Shopify/toxiproxy/v2/client` (toxiproxy control)
- **Functionality**:
  - Initialize toxiproxy clients
  - Configure memcache client with 3 servers
  - Execute workload patterns
  - Inject failure scenarios
  - Collect and report metrics

## Docker Compose Configuration

### Network Setup

Single bridge network for all containers. Toxiproxy acts as the only entry point to memcache nodes.

### Service Dependencies

```
test-cli → toxiproxy → memcache-{1,2,3}
```

The test CLI waits for toxiproxy to be healthy before starting.

### Volume Mounts

None required - all configuration is done via environment variables and API calls.

## Toxiproxy Integration

### Initialization Sequence

1. **Docker Compose starts** memcache nodes + toxiproxy
2. **Test CLI connects** to toxiproxy API (`:8474`)
3. **Create proxies** for each memcache node:
   ```go
   proxy1, _ := toxiClient.CreateProxy("memcache1", "0.0.0.0:21211", "memcache1:11211")
   proxy2, _ := toxiClient.CreateProxy("memcache2", "0.0.0.0:21212", "memcache2:11211")
   proxy3, _ := toxiClient.CreateProxy("memcache3", "0.0.0.0:21213", "memcache3:11211")
   ```
4. **Memcache client connects** to `localhost:21211,21212,21213`

### Failure Injection Examples

```go
// Brief packet drop (100ms timeout)
toxic, _ := proxy1.AddToxic("timeout_brief", "timeout", "downstream", 1.0,
    toxiproxy.Attributes{"timeout": 100})
time.Sleep(5 * time.Second)
proxy1.RemoveToxic(toxic.Name)

// 5% packet loss
proxy1.AddToxic("packet_loss", "bandwidth", "downstream", 0.05,
    toxiproxy.Attributes{"rate": 0})

// Latency injection
proxy1.AddToxic("latency_500ms", "latency", "downstream", 1.0,
    toxiproxy.Attributes{
        "latency": 500,
        "jitter": 50,
    })

// Complete node failure (disable proxy)
proxy1.Disable()
time.Sleep(10 * time.Second)
proxy1.Enable()
```

## Test Implementation Structure

```
tests/
├── README.md              # Objectives and goals
├── SETUP.md              # This file
├── docker-compose.yml    # Infrastructure definition
├── go.mod               # Go module dependencies
├── main.go              # CLI entry point
├── scenarios/
│   ├── scenarios.go     # Scenario interface
│   ├── packet_drop.go   # Brief packet drop scenario
│   ├── packet_loss.go   # Percentage packet loss
│   ├── node_failure.go  # Single/multiple node failures
│   ├── latency.go       # Latency injection
│   └── combined.go      # Combined failure scenarios
├── workload/
│   ├── workload.go      # Workload interface
│   ├── constant.go      # Constant request rate
│   ├── burst.go         # Burst traffic
│   └── mixed.go         # Mixed operation types
├── metrics/
│   ├── collector.go     # Metrics collection
│   └── reporter.go      # Results reporting
└── testutils/
    ├── setup.go         # Test environment setup
    └── assertions.go    # Custom assertions
```

## Memcache Client Configuration

```go
servers := memcache.NewStaticServers(
    "localhost:21211",  // via toxiproxy
    "localhost:21212",
    "localhost:21213",
)

client, _ := memcache.NewClient(servers, memcache.Config{
    MaxSize:             10,
    MaxConnLifetime:     5 * time.Minute,
    MaxConnIdleTime:     1 * time.Minute,
    HealthCheckInterval: 10 * time.Second,
    CircuitBreakerSettings: &gobreaker.Settings{
        MaxRequests: 3,
        Interval:    30 * time.Second,
        Timeout:     5 * time.Second,
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
            return counts.Requests >= 5 && failureRatio >= 0.6
        },
    },
})
```

### Key Configuration Choices

- **Short health check interval** (10s) - detect failures faster
- **Aggressive circuit breaker** - trip at 60% failure rate with 5+ requests
- **Small pool size** (10) - easier to observe connection behavior
- **Moderate timeouts** - balance between responsiveness and stability

## Metrics Collection

### Real-time Metrics

Collected every 1-2 seconds during test:
- Pool stats per server (active, idle, total connections)
- Circuit breaker state and counts
- Request latency distribution
- Success/error rates
- Operation throughput

### Aggregated Metrics

Computed at test completion:
- P50, P95, P99 latency
- Total requests and error rate
- Time to recovery after failures
- Circuit breaker state transitions
- Connection pool high-water marks

### Storage

Metrics stored in-memory during test, printed to stdout, optionally exported to JSON.

## Running the Tests

### Prerequisites

```bash
# Install Docker and Docker Compose
docker --version
docker compose version

# Ensure ports are available
# Required: 21211, 21212, 21213, 8474
```

### Startup Sequence

```bash
cd tests

# Start infrastructure
docker compose up -d

# Wait for services to be ready (automatic in test CLI)
# Or manually check:
docker compose ps

# Run tests
go run . [flags]

# Cleanup
docker compose down
```

### Test Modes

1. **Continuous mode** (default): Run until interrupted
   ```bash
   go run .
   ```

2. **Scenario mode**: Run specific failure scenario
   ```bash
   go run . -scenario packet-loss -duration 1m
   ```

3. **Stress mode**: High concurrency load
   ```bash
   go run . -concurrency 500 -duration 5m
   ```

4. **Development mode**: Single scenario, quick iteration
   ```bash
   go run . -scenario brief-packet-drop -duration 10s
   ```

### CLI Flags

```
-scenario string      Specific scenario to run (default: all)
-duration duration    How long to run (default: continuous)
-concurrency int      Concurrent workers (default: 100)
-list                 List available scenarios
-metrics-interval     How often to print metrics (default: 2s)
-json                 Output metrics as JSON
```

## Extending the Test Suite

### Adding New Scenarios

1. Create file in `scenarios/` package
2. Implement `Scenario` interface:
   ```go
   type Scenario interface {
       Name() string
       Description() string
       Run(ctx context.Context, proxies []*toxiproxy.Proxy) error
   }
   ```
3. Register in `scenarios/registry.go`

### Adding New Workloads

1. Create file in `workload/` package
2. Implement `Workload` interface:
   ```go
   type Workload interface {
       Name() string
       Execute(ctx context.Context, client *memcache.Client) error
   }
   ```
3. Register in `workload/registry.go`

## Debugging

### View Toxiproxy State

```bash
# List all proxies
curl http://localhost:8474/proxies

# View specific proxy
curl http://localhost:8474/proxies/memcache1

# List active toxics
curl http://localhost:8474/proxies/memcache1/toxics
```

### View Memcache Stats

```bash
# Connect directly to memcache (bypassing toxiproxy)
docker compose exec memcache1 memcached-tool localhost:11211 stats
```

### View Test Logs

```bash
# Follow docker logs
docker compose logs -f

# View specific service
docker compose logs -f toxiproxy
```

## Performance Considerations

### Expected Baseline Performance

- **Latency**: <1ms p99 under normal conditions
- **Throughput**: 10k+ ops/sec with 100 concurrent workers
- **Circuit breaker**: Opens within 1-2 seconds of failures
- **Recovery**: <5 seconds after node returns

### Load Testing Limits

- Max concurrency tested: 1000 workers
- Max sustained load: Limited by memcache capacity (3 nodes × ~50k ops/sec)
- Docker networking overhead: Minimal (<10% vs native)

## Alternatives Considered

### Pumba
**Pros**: Docker-native chaos, easy container manipulation
**Cons**: Less precise network-level control, container-focused
**Decision**: Toxiproxy chosen for finer-grained network simulation

### Chaos Mesh
**Pros**: Rich feature set, great for Kubernetes
**Cons**: Heavyweight, Kubernetes-focused, overkill for single-machine testing
**Decision**: Toxiproxy better fit for local development testing

### tc (Linux traffic control)
**Pros**: Native Linux tool, no dependencies
**Cons**: Requires elevated privileges, platform-specific, harder to script
**Decision**: Toxiproxy provides better cross-platform experience

## References

- [Toxiproxy Documentation](https://github.com/Shopify/toxiproxy)
- [Toxiproxy Go Client](https://github.com/Shopify/toxiproxy/tree/main/client)
- [Docker Compose Docs](https://docs.docker.com/compose/)
- [Memcached Meta Protocol](https://github.com/memcached/memcached/wiki/MetaCommands)
- [Gobreaker Documentation](https://github.com/sony/gobreaker)
- [Puddle Pool Documentation](https://github.com/jackc/puddle)
