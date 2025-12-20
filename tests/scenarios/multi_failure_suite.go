package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache/tests/internal/promexporter"
)

// MultiFailureScenarioSuite creates scenarios for multiple simultaneous server failures
type MultiFailureScenarioSuite struct {
	metrics *promexporter.ScenarioMetrics
}

// NewMultiFailureScenarioSuite creates a new multi-failure scenario suite
func NewMultiFailureScenarioSuite(metrics *promexporter.ScenarioMetrics) *MultiFailureScenarioSuite {
	return &MultiFailureScenarioSuite{metrics: metrics}
}

// CreateScenarios returns all multi-failure scenarios
func (s *MultiFailureScenarioSuite) CreateScenarios() []Scenario {
	return []Scenario{
		s.createTwoServerPacketLossScenario(),
		s.createTwoServerLatencyScenario(),
	}
}

func (s *MultiFailureScenarioSuite) createTwoServerPacketLossScenario() Scenario {
	name := "multi-failure-2-servers-packet-loss"
	description := "100% packet loss on 2 out of 3 servers simultaneously for 1 minute"

	var activeToxics []*toxiproxy.Toxic
	var affectedServers []string

	config := PhasedScenarioConfig{
		Name:              name,
		Description:       description,
		StabilizationTime: 30 * time.Second,
		TestingTime:       1 * time.Minute,
		RecoveryTime:      60 * time.Second,
		ApplyPerturbation: func(ctx context.Context, proxies []*toxiproxy.Proxy) error {
			if len(proxies) < 3 {
				return fmt.Errorf("need at least 3 proxies, got %d", len(proxies))
			}

			// Apply to first 2 servers (leaving 1 healthy)
			affectedServers = []string{proxies[0].Name, proxies[1].Name}
			log.Printf("[%s] Applying 100%% packet loss to %v", name, affectedServers)

			for _, proxy := range proxies[:2] {
				toxic, err := proxy.AddToxic("", "bandwidth", "downstream", 1.0, toxiproxy.Attributes{
					"rate": 0, // 0 rate = complete packet drop
				})
				if err != nil {
					return fmt.Errorf("failed to add toxic to %s: %w", proxy.Name, err)
				}
				activeToxics = append(activeToxics, toxic)

				// Update metrics
				s.metrics.SetToxicActive(proxy.Name, "packet_loss", true)
				s.metrics.SetToxicValue(proxy.Name, "packet_loss", "rate", 1.0)
			}

			return nil
		},
		RemovePerturbation: func(proxies []*toxiproxy.Proxy) error {
			if len(activeToxics) == 0 {
				return nil
			}

			log.Printf("[%s] Removing packet loss from %v", name, affectedServers)

			for i, proxy := range proxies[:2] {
				if i < len(activeToxics) {
					if err := proxy.RemoveToxic(activeToxics[i].Name); err != nil {
						return fmt.Errorf("failed to remove toxic from %s: %w", proxy.Name, err)
					}
				}

				// Update metrics
				s.metrics.SetToxicActive(proxy.Name, "packet_loss", false)
				s.metrics.SetToxicValue(proxy.Name, "packet_loss", "rate", 0)
			}

			activeToxics = nil
			return nil
		},
	}

	return NewPhasedScenario(config, s.metrics)
}

func (s *MultiFailureScenarioSuite) createTwoServerLatencyScenario() Scenario {
	name := "multi-failure-2-servers-latency"
	description := "+500ms latency on 2 out of 3 servers simultaneously for 1 minute"

	var activeToxics []*toxiproxy.Toxic
	var affectedServers []string

	config := PhasedScenarioConfig{
		Name:              name,
		Description:       description,
		StabilizationTime: 30 * time.Second,
		TestingTime:       1 * time.Minute,
		RecoveryTime:      60 * time.Second,
		ApplyPerturbation: func(ctx context.Context, proxies []*toxiproxy.Proxy) error {
			if len(proxies) < 3 {
				return fmt.Errorf("need at least 3 proxies, got %d", len(proxies))
			}

			// Apply to first 2 servers (leaving 1 healthy)
			affectedServers = []string{proxies[0].Name, proxies[1].Name}
			log.Printf("[%s] Adding +500ms latency to %v", name, affectedServers)

			for _, proxy := range proxies[:2] {
				toxic, err := proxy.AddToxic("", "latency", "downstream", 1.0, toxiproxy.Attributes{
					"latency": 500,
					"jitter":  0,
				})
				if err != nil {
					return fmt.Errorf("failed to add toxic to %s: %w", proxy.Name, err)
				}
				activeToxics = append(activeToxics, toxic)

				// Update metrics
				s.metrics.SetToxicActive(proxy.Name, "latency", true)
				s.metrics.SetToxicValue(proxy.Name, "latency", "latency_ms", 500)
			}

			return nil
		},
		RemovePerturbation: func(proxies []*toxiproxy.Proxy) error {
			if len(activeToxics) == 0 {
				return nil
			}

			log.Printf("[%s] Removing latency from %v", name, affectedServers)

			for i, proxy := range proxies[:2] {
				if i < len(activeToxics) {
					if err := proxy.RemoveToxic(activeToxics[i].Name); err != nil {
						return fmt.Errorf("failed to remove toxic from %s: %w", proxy.Name, err)
					}
				}

				// Update metrics
				s.metrics.SetToxicActive(proxy.Name, "latency", false)
				s.metrics.SetToxicValue(proxy.Name, "latency", "latency_ms", 0)
			}

			activeToxics = nil
			return nil
		},
	}

	return NewPhasedScenario(config, s.metrics)
}
