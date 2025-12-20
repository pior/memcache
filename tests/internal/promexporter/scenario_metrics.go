package promexporter

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ScenarioMetrics holds all scenario-related Prometheus metrics
type ScenarioMetrics struct {
	// Scenario state
	phase          *prometheus.GaugeVec
	phaseDuration  *prometheus.GaugeVec
	runsTotal      *prometheus.CounterVec
	currentScenario prometheus.Gauge

	// Toxiproxy state
	toxicActive *prometheus.GaugeVec
	toxicConfig *prometheus.GaugeVec
}

// NewScenarioMetrics creates and registers all scenario metrics
func NewScenarioMetrics(registry *prometheus.Registry) *ScenarioMetrics {
	m := &ScenarioMetrics{
		phase: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "scenario_phase",
				Help: "Current scenario phase (0=idle, 1=stabilization, 2=testing, 3=recovery)",
			},
			[]string{"scenario"},
		),
		phaseDuration: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "scenario_phase_duration_seconds",
				Help: "Duration of current phase in seconds",
			},
			[]string{"scenario", "phase"},
		),
		runsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "scenario_runs_total",
				Help: "Total number of scenario runs",
			},
			[]string{"scenario", "status"}, // success, failed
		),
		currentScenario: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "scenario_active",
				Help: "Whether a scenario is currently running (0=no, 1=yes)",
			},
		),
		toxicActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "toxiproxy_toxic_active",
				Help: "Whether a toxic is currently active (0=inactive, 1=active)",
			},
			[]string{"server", "type"}, // server address, toxic type (latency, bandwidth, etc)
		),
		toxicConfig: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "toxiproxy_toxic_value",
				Help: "Toxic configuration value (e.g., latency in ms, packet loss rate 0.0-1.0)",
			},
			[]string{"server", "type", "param"}, // param: latency_ms, loss_rate, etc
		),
	}

	// Register all metrics
	registry.MustRegister(
		m.phase,
		m.phaseDuration,
		m.runsTotal,
		m.currentScenario,
		m.toxicActive,
		m.toxicConfig,
	)

	return m
}

// SetPhase sets the current scenario phase (0=idle, 1=stabilization, 2=testing, 3=recovery)
func (m *ScenarioMetrics) SetPhase(scenario string, phase int) {
	m.phase.WithLabelValues(scenario).Set(float64(phase))
}

// SetPhaseDuration sets the duration for a specific phase
func (m *ScenarioMetrics) SetPhaseDuration(scenario, phase string, seconds float64) {
	m.phaseDuration.WithLabelValues(scenario, phase).Set(seconds)
}

// RecordRun records a scenario run completion
func (m *ScenarioMetrics) RecordRun(scenario string, success bool) {
	status := "success"
	if !success {
		status = "failed"
	}
	m.runsTotal.WithLabelValues(scenario, status).Inc()
}

// SetScenarioActive sets whether a scenario is currently running
func (m *ScenarioMetrics) SetScenarioActive(active bool) {
	if active {
		m.currentScenario.Set(1)
	} else {
		m.currentScenario.Set(0)
	}
}

// SetToxicActive sets whether a toxic is active
func (m *ScenarioMetrics) SetToxicActive(server, toxicType string, active bool) {
	if active {
		m.toxicActive.WithLabelValues(server, toxicType).Set(1)
	} else {
		m.toxicActive.WithLabelValues(server, toxicType).Set(0)
	}
}

// SetToxicValue sets a toxic configuration value
func (m *ScenarioMetrics) SetToxicValue(server, toxicType, param string, value float64) {
	m.toxicConfig.WithLabelValues(server, toxicType, param).Set(value)
}

// ClearToxics clears all toxic metrics (sets all to inactive/0)
func (m *ScenarioMetrics) ClearToxics() {
	// This doesn't actually clear the metrics, but we could reset known ones
	// For now, individual scenarios will set toxics to 0 when they deactivate them
}
