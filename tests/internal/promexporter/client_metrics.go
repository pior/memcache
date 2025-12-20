package promexporter

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ClientMetrics holds all client-related Prometheus metrics
type ClientMetrics struct {
	// Operations
	opsTotal  *prometheus.CounterVec
	opsRate   prometheus.Gauge
	errorRate prometheus.Gauge

	// Circuit Breaker
	circuitState       *prometheus.GaugeVec
	circuitTransitions *prometheus.CounterVec
	circuitRequests    *prometheus.GaugeVec
	circuitFailures    *prometheus.GaugeVec

	// Pool
	poolConnections *prometheus.GaugeVec
	poolCreated     *prometheus.CounterVec
	poolErrors      *prometheus.GaugeVec
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
				Name: "memcache_pool_connections_created",
				Help: "Total connections created (cumulative)",
			},
			[]string{"server"},
		),
		poolErrors: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "memcache_pool_acquire_errors",
				Help: "Total connection acquire errors (cumulative)",
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

// SetCircuitBreakerRequests sets the number of requests tracked
func (m *ClientMetrics) SetCircuitBreakerRequests(server string, requests int) {
	m.circuitRequests.WithLabelValues(server).Set(float64(requests))
}

// SetCircuitBreakerFailures sets failure counts
func (m *ClientMetrics) SetCircuitBreakerFailures(server string, total, consecutive int) {
	m.circuitFailures.WithLabelValues(server, "total").Set(float64(total))
	m.circuitFailures.WithLabelValues(server, "consecutive").Set(float64(consecutive))
}

// SetPoolConnections sets pool connection counts
func (m *ClientMetrics) SetPoolConnections(server string, total, active, idle int) {
	m.poolConnections.WithLabelValues(server, "total").Set(float64(total))
	m.poolConnections.WithLabelValues(server, "active").Set(float64(active))
	m.poolConnections.WithLabelValues(server, "idle").Set(float64(idle))
}

// SetPoolCreated sets cumulative connections created
func (m *ClientMetrics) SetPoolCreated(server string, created uint64) {
	m.poolCreated.WithLabelValues(server).Add(float64(created))
}

// SetPoolErrors sets cumulative acquire errors
func (m *ClientMetrics) SetPoolErrors(server string, errors uint64) {
	m.poolErrors.WithLabelValues(server).Set(float64(errors))
}
