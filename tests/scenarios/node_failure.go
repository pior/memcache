package scenarios

import (
	"context"
	"fmt"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
)

// SingleNodeFailureScenario simulates one node going down
type SingleNodeFailureScenario struct{}

func (s *SingleNodeFailureScenario) Name() string {
	return "single-node-failure"
}

func (s *SingleNodeFailureScenario) Description() string {
	return "Single node failure (1 of 3) for 15s - simulates partial availability"
}

func (s *SingleNodeFailureScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	if len(proxies) < 3 {
		return fmt.Errorf("need at least 3 proxies, got %d", len(proxies))
	}

	proxy := proxies[0]
	fmt.Printf("[Scenario] Disabling node %s (1/3 nodes down)\n", proxy.Name)

	if err := proxy.Disable(); err != nil {
		return fmt.Errorf("failed to disable proxy: %w", err)
	}

	// Run for 15 seconds
	fmt.Printf("[Scenario] Node down for 15s\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
	}

	// Re-enable
	fmt.Printf("[Scenario] Re-enabling node %s\n", proxy.Name)
	if err := proxy.Enable(); err != nil {
		return fmt.Errorf("failed to enable proxy: %w", err)
	}

	// Allow recovery
	fmt.Printf("[Scenario] Allowing 10s recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
	}

	return nil
}

// MajorityNodeFailureScenario simulates majority of nodes failing
type MajorityNodeFailureScenario struct{}

func (s *MajorityNodeFailureScenario) Name() string {
	return "majority-node-failure"
}

func (s *MajorityNodeFailureScenario) Description() string {
	return "Majority node failure (2 of 3) for 10s - simulates quorum loss"
}

func (s *MajorityNodeFailureScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	if len(proxies) < 3 {
		return fmt.Errorf("need at least 3 proxies, got %d", len(proxies))
	}

	// Disable 2 nodes
	failedProxies := proxies[:2]
	fmt.Printf("[Scenario] Disabling 2 nodes (2/3 down - quorum loss)\n")

	for _, proxy := range failedProxies {
		if err := proxy.Disable(); err != nil {
			return fmt.Errorf("failed to disable %s: %w", proxy.Name, err)
		}
	}

	// Run for 10 seconds
	fmt.Printf("[Scenario] 2/3 nodes down for 10s\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
	}

	// Re-enable all
	fmt.Printf("[Scenario] Re-enabling all nodes\n")
	for _, proxy := range failedProxies {
		if err := proxy.Enable(); err != nil {
			return fmt.Errorf("failed to enable %s: %w", proxy.Name, err)
		}
	}

	// Allow recovery
	fmt.Printf("[Scenario] Allowing 15s recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
	}

	return nil
}

// FlappingNodeScenario simulates a node going up and down repeatedly
type FlappingNodeScenario struct{}

func (s *FlappingNodeScenario) Name() string {
	return "flapping-node"
}

func (s *FlappingNodeScenario) Description() string {
	return "Node flapping (up/down every 10s) - simulates unstable node"
}

func (s *FlappingNodeScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	if len(proxies) == 0 {
		return fmt.Errorf("no proxies available")
	}

	proxy := proxies[0]
	fmt.Printf("[Scenario] Node %s flapping (5 cycles of 10s down, 10s up)\n", proxy.Name)

	for range 5 {
		// Disable
		fmt.Printf("[Scenario] Disabling %s\n", proxy.Name)
		if err := proxy.Disable(); err != nil {
			return fmt.Errorf("failed to disable: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}

		// Enable
		fmt.Printf("[Scenario] Enabling %s\n", proxy.Name)
		if err := proxy.Enable(); err != nil {
			return fmt.Errorf("failed to enable: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}

	// Final recovery time
	fmt.Printf("[Scenario] Allowing 10s final recovery time\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
	}

	return nil
}
