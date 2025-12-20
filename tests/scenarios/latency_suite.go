package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache/tests/internal/promexporter"
)

// LatencyScenarioSuite creates scenarios for different latency durations
type LatencyScenarioSuite struct {
	metrics       *promexporter.ScenarioMetrics
	latencyAmount int // Latency to add in milliseconds
}

// NewLatencyScenarioSuite creates a new latency scenario suite
// latencyAmount is the latency to add in milliseconds (e.g., 200 for +200ms)
func NewLatencyScenarioSuite(metrics *promexporter.ScenarioMetrics, latencyAmount int) *LatencyScenarioSuite {
	return &LatencyScenarioSuite{
		metrics:       metrics,
		latencyAmount: latencyAmount,
	}
}

// CreateScenarios returns all latency scenarios
func (s *LatencyScenarioSuite) CreateScenarios() []Scenario {
	durations := []time.Duration{
		100 * time.Millisecond,
		1 * time.Second,
		5 * time.Second,
		10 * time.Second,
		40 * time.Second,
		2 * time.Minute,
	}

	scenarios := make([]Scenario, 0, len(durations))

	for _, duration := range durations {
		scenario := s.createLatencyScenario(duration)
		scenarios = append(scenarios, scenario)
	}

	return scenarios
}

func (s *LatencyScenarioSuite) createLatencyScenario(testDuration time.Duration) Scenario {
	name := fmt.Sprintf("latency-%dms-%s", s.latencyAmount, formatDuration(testDuration))
	description := fmt.Sprintf("+%dms latency on single server for %s", s.latencyAmount, testDuration)

	var activeToxic *toxiproxy.Toxic
	var affectedServer string

	config := PhasedScenarioConfig{
		Name:              name,
		Description:       description,
		StabilizationTime: 30 * time.Second,
		TestingTime:       testDuration,
		RecoveryTime:      60 * time.Second,
		ApplyPerturbation: func(ctx context.Context, proxies []*toxiproxy.Proxy) error {
			if len(proxies) == 0 {
				return fmt.Errorf("no proxies available")
			}

			// Apply to first server
			proxy := proxies[0]
			affectedServer = proxy.Name

			log.Printf("[%s] Adding +%dms latency to %s", name, s.latencyAmount, affectedServer)

			// Use latency toxic
			toxic, err := proxy.AddToxic("", "latency", "downstream", 1.0, toxiproxy.Attributes{
				"latency": s.latencyAmount,
				"jitter":  0,
			})
			if err != nil {
				return fmt.Errorf("failed to add toxic: %w", err)
			}

			activeToxic = toxic

			// Update metrics
			s.metrics.SetToxicActive(affectedServer, "latency", true)
			s.metrics.SetToxicValue(affectedServer, "latency", "latency_ms", float64(s.latencyAmount))

			return nil
		},
		RemovePerturbation: func(proxies []*toxiproxy.Proxy) error {
			if activeToxic == nil {
				return nil
			}

			if len(proxies) == 0 {
				return fmt.Errorf("no proxies available")
			}

			proxy := proxies[0]
			log.Printf("[%s] Removing latency from %s", name, affectedServer)

			if err := proxy.RemoveToxic(activeToxic.Name); err != nil {
				return fmt.Errorf("failed to remove toxic: %w", err)
			}

			// Update metrics
			s.metrics.SetToxicActive(affectedServer, "latency", false)
			s.metrics.SetToxicValue(affectedServer, "latency", "latency_ms", 0)

			activeToxic = nil
			return nil
		},
	}

	return NewPhasedScenario(config, s.metrics)
}

// formatDuration formats a duration for use in scenario names
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
