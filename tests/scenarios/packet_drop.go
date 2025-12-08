package scenarios

import (
	"context"
	"fmt"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
)

// BriefPacketDropScenario simulates a brief network glitch (50ms-1s)
type BriefPacketDropScenario struct{}

func (s *BriefPacketDropScenario) Name() string {
	return "brief-packet-drop"
}

func (s *BriefPacketDropScenario) Description() string {
	return "Brief packet drop (100ms) on one node - simulates transient network glitch"
}

func (s *BriefPacketDropScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	if len(proxies) == 0 {
		return fmt.Errorf("no proxies available")
	}

	proxy := proxies[0] // Target first node
	fmt.Printf("[Scenario] Injecting brief packet drop (100ms timeout) on %s\n", proxy.Name)

	// Add timeout toxic
	toxic, err := proxy.AddToxic("brief_timeout", "timeout", "downstream", 1.0,
		toxiproxy.Attributes{"timeout": 100})
	if err != nil {
		return fmt.Errorf("failed to add toxic: %w", err)
	}

	// Let it run for 5 seconds
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}

	// Remove toxic
	fmt.Printf("[Scenario] Removing toxic from %s\n", proxy.Name)
	if err := proxy.RemoveToxic(toxic.Name); err != nil {
		return fmt.Errorf("failed to remove toxic: %w", err)
	}

	// Allow recovery time
	fmt.Printf("[Scenario] Allowing 5s recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}

	return nil
}

// TotalPacketDropScenario simulates complete network partition
type TotalPacketDropScenario struct{}

func (s *TotalPacketDropScenario) Name() string {
	return "total-packet-drop"
}

func (s *TotalPacketDropScenario) Description() string {
	return "Total packet drop (10s) on one node - simulates complete network partition"
}

func (s *TotalPacketDropScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	if len(proxies) == 0 {
		return fmt.Errorf("no proxies available")
	}

	proxy := proxies[0]
	fmt.Printf("[Scenario] Disabling proxy %s (total network partition)\n", proxy.Name)

	// Disable proxy entirely
	if err := proxy.Disable(); err != nil {
		return fmt.Errorf("failed to disable proxy: %w", err)
	}

	// Let it run for 10 seconds
	fmt.Printf("[Scenario] Node unavailable for 10s\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
	}

	// Re-enable proxy
	fmt.Printf("[Scenario] Re-enabling proxy %s\n", proxy.Name)
	if err := proxy.Enable(); err != nil {
		return fmt.Errorf("failed to enable proxy: %w", err)
	}

	// Allow recovery time
	fmt.Printf("[Scenario] Allowing 10s recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
	}

	return nil
}
