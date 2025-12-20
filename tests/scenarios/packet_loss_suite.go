package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache/tests/internal/promexporter"
)

// PacketLossScenarioSuite creates scenarios for different packet loss rates
type PacketLossScenarioSuite struct {
	metrics *promexporter.ScenarioMetrics
}

// NewPacketLossScenarioSuite creates a new packet loss scenario suite
func NewPacketLossScenarioSuite(metrics *promexporter.ScenarioMetrics) *PacketLossScenarioSuite {
	return &PacketLossScenarioSuite{metrics: metrics}
}

// CreateScenarios returns all packet loss scenarios
func (s *PacketLossScenarioSuite) CreateScenarios() []Scenario {
	rates := []float64{0.02, 0.05, 0.10, 0.20, 0.50, 1.0}
	scenarios := make([]Scenario, 0, len(rates))

	for _, rate := range rates {
		scenario := s.createPacketLossScenario(rate)
		scenarios = append(scenarios, scenario)
	}

	return scenarios
}

func (s *PacketLossScenarioSuite) createPacketLossScenario(lossRate float64) Scenario {
	name := fmt.Sprintf("packet-loss-%.0f-pct", lossRate*100)
	description := fmt.Sprintf("%.0f%% packet loss on single server for 1 minute", lossRate*100)

	var activeToxic *toxiproxy.Toxic
	var affectedServer string

	config := PhasedScenarioConfig{
		Name:              name,
		Description:       description,
		StabilizationTime: 30 * time.Second,
		TestingTime:       1 * time.Minute,
		RecoveryTime:      60 * time.Second,
		ApplyPerturbation: func(ctx context.Context, proxies []*toxiproxy.Proxy) error {
			if len(proxies) == 0 {
				return fmt.Errorf("no proxies available")
			}

			// Apply to first server
			proxy := proxies[0]
			affectedServer = proxy.Name

			log.Printf("[%s] Applying %.0f%% packet loss to %s", name, lossRate*100, affectedServer)

			// Use bandwidth toxic with toxicity parameter for packet loss
			toxic, err := proxy.AddToxic("", "bandwidth", "downstream", float32(lossRate), toxiproxy.Attributes{
				"rate": 0, // 0 rate = complete packet drop
			})
			if err != nil {
				return fmt.Errorf("failed to add toxic: %w", err)
			}

			activeToxic = toxic

			// Update metrics
			s.metrics.SetToxicActive(affectedServer, "packet_loss", true)
			s.metrics.SetToxicValue(affectedServer, "packet_loss", "rate", lossRate)

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
			log.Printf("[%s] Removing packet loss from %s", name, affectedServer)

			if err := proxy.RemoveToxic(activeToxic.Name); err != nil {
				return fmt.Errorf("failed to remove toxic: %w", err)
			}

			// Update metrics
			s.metrics.SetToxicActive(affectedServer, "packet_loss", false)
			s.metrics.SetToxicValue(affectedServer, "packet_loss", "rate", 0)

			activeToxic = nil
			return nil
		},
	}

	return NewPhasedScenario(config, s.metrics)
}
