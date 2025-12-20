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
	scenario *ScenarioMetrics
}

// NewExporter creates a new Prometheus exporter
func NewExporter() *Exporter {
	registry := prometheus.NewRegistry()

	return &Exporter{
		registry: registry,
		client:   NewClientMetrics(registry),
		scenario: NewScenarioMetrics(registry),
	}
}

// ClientMetrics returns the client metrics collector
func (e *Exporter) ClientMetrics() *ClientMetrics {
	return e.client
}

// ScenarioMetrics returns the scenario metrics collector
func (e *Exporter) ScenarioMetrics() *ScenarioMetrics {
	return e.scenario
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
