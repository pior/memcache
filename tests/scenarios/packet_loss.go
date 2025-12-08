package scenarios

import (
	"context"
	"fmt"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
)

// PacketLossScenario simulates degraded network with packet loss
type PacketLossScenario struct{}

func (s *PacketLossScenario) Name() string {
	return "packet-loss"
}

func (s *PacketLossScenario) Description() string {
	return "5% packet loss on all nodes - simulates degraded network quality"
}

func (s *PacketLossScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	fmt.Printf("[Scenario] Injecting 5%% packet loss on all nodes\n")

	toxics := make([]*toxiproxy.Toxic, 0, len(proxies))

	// Add packet loss to all proxies
	for _, proxy := range proxies {
		// Use bandwidth toxic with low rate to simulate packet loss
		// toxicity=0.05 means 5% of packets affected
		toxic, err := proxy.AddToxic("packet_loss", "bandwidth", "downstream", 0.05,
			toxiproxy.Attributes{"rate": 0})
		if err != nil {
			return fmt.Errorf("failed to add toxic to %s: %w", proxy.Name, err)
		}
		toxics = append(toxics, toxic)
	}

	// Run for 20 seconds
	fmt.Printf("[Scenario] Running with packet loss for 20s\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Second):
	}

	// Remove all toxics
	fmt.Printf("[Scenario] Removing packet loss toxics\n")
	for i, proxy := range proxies {
		if err := proxy.RemoveToxic(toxics[i].Name); err != nil {
			return fmt.Errorf("failed to remove toxic from %s: %w", proxy.Name, err)
		}
	}

	// Allow recovery
	fmt.Printf("[Scenario] Allowing 5s recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}

	return nil
}
