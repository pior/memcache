package scenarios

import (
	"context"
	"fmt"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
)

// LatencyScenario simulates slow network with high latency
type LatencyScenario struct{}

func (s *LatencyScenario) Name() string {
	return "latency"
}

func (s *LatencyScenario) Description() string {
	return "500ms latency (+/- 50ms jitter) on all nodes - simulates slow network"
}

func (s *LatencyScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	fmt.Printf("[Scenario] Injecting 500ms latency with 50ms jitter on all nodes\n")

	toxics := make([]*toxiproxy.Toxic, 0, len(proxies))

	// Add latency to all proxies
	for _, proxy := range proxies {
		toxic, err := proxy.AddToxic("high_latency", "latency", "downstream", 1.0,
			toxiproxy.Attributes{
				"latency": 500,
				"jitter":  50,
			})
		if err != nil {
			return fmt.Errorf("failed to add toxic to %s: %w", proxy.Name, err)
		}
		toxics = append(toxics, toxic)
	}

	// Run for 30 seconds
	fmt.Printf("[Scenario] Running with high latency for 30s\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
	}

	// Remove all toxics
	fmt.Printf("[Scenario] Removing latency toxics\n")
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
