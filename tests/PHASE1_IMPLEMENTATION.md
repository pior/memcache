# Phase 1: Foundation Implementation Guide

## Overview

Phase 1 establishes the Prometheus/Grafana infrastructure with a working load generator that exports client metrics. This is the critical foundation for all subsequent work.

## Architecture

```
┌─────────────────────────────────────┐
│         Load Generator              │
│                                     │
│  ┌───────────────────────────────┐  │
│  │   Workload Runner             │  │
│  │   (existing code reuse)       │  │
│  └───────────┬───────────────────┘  │
│              │                      │
│  ┌───────────▼───────────────────┐  │
│  │   Metrics Collector           │  │
│  │   - Circuit breaker states    │  │
│  │   - Pool statistics           │  │
│  │   - Operation counts          │  │
│  └───────────┬───────────────────┘  │
│              │                      │
│  ┌───────────▼───────────────────┐  │
│  │   Prometheus Exporter         │  │
│  │   HTTP server on :9090        │  │
│  │   /metrics endpoint           │  │
│  └───────────────────────────────┘  │
│                                     │
└─────────────────┬───────────────────┘
                  │
                  │ scrape
                  │ every 5s
                  ▼
         ┌─────────────────┐
         │   Prometheus    │
         │   :9091         │
         └────────┬────────┘
                  │
                  │ query
                  ▼
         ┌─────────────────┐
         │    Grafana      │
         │    :3000        │
         └─────────────────┘
```

## File Structure

```
tests/
├── cmd/
│   └── loadgen/
│       └── main.go                    # New: Load generator binary
├── internal/
│   └── promexporter/
│       ├── exporter.go                # New: Prometheus metrics registry
│       └── client_metrics.go          # New: Client-specific metrics
├── prometheus/
│   └── prometheus.yml                 # New: Prometheus config
├── grafana/
│   ├── provisioning/
│   │   ├── datasources/
│   │   │   └── prometheus.yml         # New: Grafana datasource config
│   │   └── dashboards/
│   │       └── dashboards.yml         # New: Dashboard provisioning config
│   └── dashboards/
│       └── client-performance.json    # New: Main dashboard
├── docker-compose.yml                 # Modified: Add Prometheus & Grafana
├── workload/                          # Existing: Reuse as-is
├── testutils/                         # Existing: Reuse as-is
└── scenarios/                         # Existing: Not used in Phase 1
```

## Implementation Details

### 1. Docker Compose Updates

**File:** `tests/docker-compose.yml`

Add two new services:

```yaml
  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    ports:
      - "9091:9090"  # Expose on 9091 to avoid conflicts
    volumes:
      - ./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
      - '--web.console.libraries=/etc/prometheus/console_libraries'
      - '--web.console.templates=/etc/prometheus/consoles'
      - '--web.enable-lifecycle'
    networks:
      - memcache-test

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning
      - ./grafana/dashboards:/var/lib/grafana/dashboards
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    networks:
      - memcache-test
    depends_on:
      - prometheus
```

**Key decisions:**
- Prometheus exposed on 9091 (not 9090) to avoid conflicts with loadgen
- Grafana uses default port 3000
- File-based provisioning for both datasource and dashboards
- Admin password set to "admin" for simplicity (docs will note this)

---

### 2. Prometheus Configuration

**File:** `tests/prometheus/prometheus.yml`

```yaml
global:
  scrape_interval: 5s      # Frequent scraping for real-time visibility
  evaluation_interval: 5s  # How often to evaluate rules

scrape_configs:
  # Load generator metrics
  - job_name: 'loadgen'
    static_configs:
      - targets: ['host.docker.internal:9090']  # macOS/Windows
        labels:
          component: 'load-generator'

    # For Linux, use the following instead:
    # - targets: ['172.17.0.1:9090']
```

**Notes:**
- `host.docker.internal` works on macOS/Windows Docker Desktop
- For Linux, need to use bridge network IP (document this)
- 5s scrape interval provides good real-time visibility
- Labels help identify metrics source

---

### 3. Grafana Datasource Provisioning

**File:** `tests/grafana/provisioning/datasources/prometheus.yml`

```yaml
apiVersion: 1

datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

---

### 4. Grafana Dashboard Provisioning

**File:** `tests/grafana/provisioning/dashboards/dashboards.yml`

```yaml
apiVersion: 1

providers:
  - name: 'default'
    orgId: 1
    folder: ''
    type: file
    disableDeletion: false
    updateIntervalSeconds: 10
    allowUiUpdates: true
    options:
      path: /var/lib/grafana/dashboards
      foldersFromFilesStructure: true
```

---

### 5. Prometheus Metrics Exporter

**File:** `tests/internal/promexporter/exporter.go`

```go
package promexporter

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Exporter manages Prometheus metrics export
type Exporter struct {
	registry *prometheus.Registry
	client   *ClientMetrics
}

// NewExporter creates a new Prometheus exporter
func NewExporter() *Exporter {
	registry := prometheus.NewRegistry()

	return &Exporter{
		registry: registry,
		client:   NewClientMetrics(registry),
	}
}

// ClientMetrics returns the client metrics collector
func (e *Exporter) ClientMetrics() *ClientMetrics {
	return e.client
}

// Handler returns an HTTP handler for the /metrics endpoint
func (e *Exporter) Handler() http.Handler {
	return promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{})
}

// ServeHTTP starts the metrics HTTP server
func (e *Exporter) ServeHTTP(addr string) error {
	http.Handle("/metrics", e.Handler())
	return http.ListenAndServe(addr, nil)
}
```

---

### 6. Client Metrics Definitions

**File:** `tests/internal/promexporter/client_metrics.go`

```go
package promexporter

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ClientMetrics holds all client-related Prometheus metrics
type ClientMetrics struct {
	// Operations
	opsTotal      *prometheus.CounterVec
	opsRate       prometheus.Gauge
	errorRate     prometheus.Gauge

	// Circuit Breaker
	circuitState       *prometheus.GaugeVec
	circuitTransitions *prometheus.CounterVec
	circuitRequests    *prometheus.GaugeVec
	circuitFailures    *prometheus.GaugeVec

	// Pool
	poolConnections *prometheus.GaugeVec
	poolCreated     *prometheus.CounterVec
	poolErrors      *prometheus.CounterVec
}

// NewClientMetrics creates and registers all client metrics
func NewClientMetrics(registry *prometheus.Registry) *ClientMetrics {
	m := &ClientMetrics{
		opsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "memcache_operations_total",
				Help: "Total number of memcache operations",
			},
			[]string{"status"}, // success, failed
		),
		opsRate: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "memcache_operations_per_second",
				Help: "Current operations per second",
			},
		),
		errorRate: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "memcache_error_rate",
				Help: "Current error rate (0.0 to 1.0)",
			},
		),
		circuitState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "memcache_circuit_breaker_state",
				Help: "Circuit breaker state (0=closed, 1=half-open, 2=open)",
			},
			[]string{"server"},
		),
		circuitTransitions: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "memcache_circuit_breaker_transitions_total",
				Help: "Total circuit breaker state transitions",
			},
			[]string{"server", "from", "to"},
		),
		circuitRequests: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "memcache_circuit_breaker_requests",
				Help: "Number of requests tracked by circuit breaker",
			},
			[]string{"server"},
		),
		circuitFailures: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "memcache_circuit_breaker_failures",
				Help: "Circuit breaker failure counts",
			},
			[]string{"server", "type"}, // total, consecutive
		),
		poolConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "memcache_pool_connections",
				Help: "Connection pool statistics",
			},
			[]string{"server", "state"}, // total, active, idle
		),
		poolCreated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "memcache_pool_connections_created_total",
				Help: "Total connections created",
			},
			[]string{"server"},
		),
		poolErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "memcache_pool_acquire_errors_total",
				Help: "Total connection acquire errors",
			},
			[]string{"server"},
		),
	}

	// Register all metrics
	registry.MustRegister(
		m.opsTotal,
		m.opsRate,
		m.errorRate,
		m.circuitState,
		m.circuitTransitions,
		m.circuitRequests,
		m.circuitFailures,
		m.poolConnections,
		m.poolCreated,
		m.poolErrors,
	)

	return m
}

// Update methods (simplified - actual implementation will have more)

// RecordOperation records an operation result
func (m *ClientMetrics) RecordOperation(success bool) {
	status := "success"
	if !success {
		status = "failed"
	}
	m.opsTotal.WithLabelValues(status).Inc()
}

// SetOperationRate sets the current ops/sec
func (m *ClientMetrics) SetOperationRate(rate float64) {
	m.opsRate.Set(rate)
}

// SetErrorRate sets the current error rate
func (m *ClientMetrics) SetErrorRate(rate float64) {
	m.errorRate.Set(rate)
}

// RecordCircuitBreakerTransition records a state change
func (m *ClientMetrics) RecordCircuitBreakerTransition(server, from, to string) {
	m.circuitTransitions.WithLabelValues(server, from, to).Inc()
}

// SetCircuitBreakerState sets the current state (0, 1, or 2)
func (m *ClientMetrics) SetCircuitBreakerState(server string, state int) {
	m.circuitState.WithLabelValues(server).Set(float64(state))
}

// UpdatePoolStats updates pool statistics
func (m *ClientMetrics) UpdatePoolStats(server string, total, active, idle, created, errors int) {
	m.poolConnections.WithLabelValues(server, "total").Set(float64(total))
	m.poolConnections.WithLabelValues(server, "active").Set(float64(active))
	m.poolConnections.WithLabelValues(server, "idle").Set(float64(idle))

	// For counters, we need to set them to the current value
	// This is a simplification - in production, we'd track increments
	m.poolCreated.WithLabelValues(server).Add(float64(created))
	m.poolErrors.WithLabelValues(server).Add(float64(errors))
}
```

---

### 7. Load Generator Main

**File:** `tests/cmd/loadgen/main.go`

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pior/memcache/tests/internal/promexporter"
	"github.com/pior/memcache/tests/testutils"
	"github.com/pior/memcache/tests/workload"
	"github.com/sony/gobreaker/v2"
)

func main() {
	// Flags
	concurrency := flag.Int("concurrency", 100, "Number of concurrent workers")
	workloadName := flag.String("workload", "mixed", "Workload pattern to use")
	metricsPort := flag.String("metrics-port", ":9090", "Port for Prometheus metrics")

	flag.Parse()

	log.Printf("Starting memcache load generator")
	log.Printf("  Concurrency: %d", *concurrency)
	log.Printf("  Workload: %s", *workloadName)
	log.Printf("  Metrics: http://localhost%s/metrics", *metricsPort)

	// Setup Prometheus exporter
	exporter := promexporter.NewExporter()
	go func() {
		log.Printf("Starting metrics server on %s", *metricsPort)
		if err := exporter.ServeHTTP(*metricsPort); err != nil {
			log.Fatalf("Metrics server error: %v", err)
		}
	}()

	// Setup memcache client
	clientConfig := testutils.DefaultMemcacheClientConfig()

	// Wire up circuit breaker callbacks to Prometheus
	clientConfig.CircuitBreakerSettings.OnStateChange = func(name string, from, to gobreaker.State) {
		log.Printf("Circuit breaker %s: %s -> %s", name, from.String(), to.String())
		exporter.ClientMetrics().RecordCircuitBreakerTransition(name, from.String(), to.String())

		// Update state gauge
		stateValue := circuitStateToInt(to)
		exporter.ClientMetrics().SetCircuitBreakerState(name, stateValue)
	}

	client, err := testutils.SetupMemcacheClient(clientConfig)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Wait for client to be healthy
	ctx := context.Background()
	log.Printf("Waiting for memcache servers to be healthy...")
	if err := testutils.WaitForHealthy(ctx, client); err != nil {
		log.Fatalf("Health check failed: %v", err)
	}
	log.Printf("All servers healthy")

	// Get workload
	wl, err := workload.Get(*workloadName)
	if err != nil {
		log.Fatalf("Failed to load workload: %v", err)
	}
	log.Printf("Workload: %s - %s", wl.Name(), wl.Description())

	// Create workload runner
	runner := workload.NewRunner(client, wl, *concurrency)

	// Start metrics collection goroutine
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go metricsCollectionLoop(ctx, runner, client, exporter.ClientMetrics())

	// Start workload
	log.Printf("Starting workload with %d workers", *concurrency)
	go func() {
		if err := runner.Run(ctx); err != nil {
			log.Printf("Workload error: %v", err)
		}
	}()

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Printf("Shutting down...")
	cancel()
	time.Sleep(500 * time.Millisecond)
	log.Printf("Shutdown complete")
}

func metricsCollectionLoop(ctx context.Context, runner *workload.Runner, client interface{}, metrics *promexporter.ClientMetrics) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastTotal int64
	var lastTime time.Time = time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Collect workload stats
			stats := runner.Stats()

			// Calculate rate
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			if elapsed > 0 {
				opsThisPeriod := stats.TotalOps - lastTotal
				rate := float64(opsThisPeriod) / elapsed
				metrics.SetOperationRate(rate)
			}

			metrics.SetErrorRate(stats.ErrorRate)
			lastTotal = stats.TotalOps
			lastTime = now

			// Collect pool stats (using type assertion - actual code will be cleaner)
			// For Phase 1, we'll implement basic stats collection
			// TODO: Add proper pool stats collection
		}
	}
}

func circuitStateToInt(state gobreaker.State) int {
	switch state.String() {
	case "closed":
		return 0
	case "half-open":
		return 1
	case "open":
		return 2
	default:
		return -1
	}
}
```

---

### 8. Basic Grafana Dashboard

**File:** `tests/grafana/dashboards/client-performance.json`

This will be a JSON file created manually or exported from Grafana. Key panels:

1. **Operations per Second** - Line graph showing `memcache_operations_per_second`
2. **Error Rate** - Line graph showing `memcache_error_rate * 100` (as percentage)
3. **Circuit Breaker States** - State timeline using `memcache_circuit_breaker_state`
4. **Pool Connections** - Stacked area chart of `memcache_pool_connections` by state
5. **Total Operations** - Single stat showing `rate(memcache_operations_total[1m])`

(Initial version can be simple - we'll refine iteratively)

---

## Implementation Order

1. ✅ **Docker Compose** - Add Prometheus & Grafana services
2. ✅ **Prometheus Config** - Create prometheus.yml
3. ✅ **Grafana Provisioning** - Setup datasource and dashboard configs
4. ✅ **Prometheus Exporter** - Create internal/promexporter package
5. ✅ **Load Generator** - Create cmd/loadgen/main.go
6. ✅ **Test & Validate** - Run everything and verify metrics flow
7. ✅ **Grafana Dashboard** - Create basic dashboard JSON
8. ✅ **Documentation** - Update README with Phase 1 usage

---

## Testing Plan

### Manual Testing Steps

1. **Start infrastructure:**
   ```bash
   cd tests
   docker compose up -d
   ```

2. **Verify services:**
   - Memcache nodes: `docker ps` should show 3 healthy nodes
   - Toxiproxy: `curl http://localhost:8474/version`
   - Prometheus: Open `http://localhost:9091` in browser
   - Grafana: Open `http://localhost:3000` (admin/admin)

3. **Start load generator:**
   ```bash
   cd cmd/loadgen
   go run . -concurrency 100 -workload mixed
   ```

4. **Verify metrics:**
   - Direct: `curl http://localhost:9090/metrics`
   - Prometheus: Check Targets page shows loadgen as UP
   - Grafana: Import/view the dashboard

5. **Validate metrics accuracy:**
   - Compare ops/sec with previous TUI version
   - Trigger circuit breaker manually (stop a memcache node)
   - Verify state changes appear in metrics

### Success Criteria

- ✅ All docker services start successfully
- ✅ Load generator exports metrics on :9090
- ✅ Prometheus scrapes metrics every 5s
- ✅ Grafana datasource connects to Prometheus
- ✅ Dashboard displays real-time data
- ✅ Circuit breaker state transitions visible
- ✅ Metrics match expected behavior (cross-check with old system)

---

## Known Limitations (Phase 1)

1. **No latency metrics** - Will add histograms in Phase 2
2. **No scenario controller** - Phase 1 is load generator only
3. **Basic dashboard** - Will enhance with advanced panels later
4. **Manual testing required** - No automated tests yet
5. **Linux compatibility** - Prometheus scraping may need adjustment

---

## Migration Notes

- **Old main.go preserved** - Don't delete until Phase 1 validated
- **TUI code untouched** - Keep as reference during migration
- **Metrics logic reused** - Adapter pattern to bridge existing collector

---

## Next Steps After Phase 1

Once Phase 1 is complete and validated:
1. Remove TUI dependencies
2. Start Phase 2: Scenario controller
3. Enhance Grafana dashboards
4. Add latency histogram metrics
5. Implement structured logging
