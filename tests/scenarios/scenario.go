package scenarios

import (
	"context"
	"fmt"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
)

// Scenario represents a failure scenario that can be executed during testing
type Scenario interface {
	// Name returns the unique identifier for this scenario
	Name() string

	// Description returns a human-readable description
	Description() string

	// Run executes the scenario, applying toxics to the proxies
	// It should block for the duration of the scenario
	Run(ctx context.Context, proxies []*toxiproxy.Proxy) error
}

// Registry holds all available scenarios
var registry = make(map[string]Scenario)

// Register adds a scenario to the registry
func Register(s Scenario) {
	registry[s.Name()] = s
}

// Get retrieves a scenario by name
func Get(name string) (Scenario, error) {
	s, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", name)
	}
	return s, nil
}

// All returns all registered scenarios
func All() map[string]Scenario {
	return registry
}

// List returns all scenario names
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

func init() {
	// Register all scenarios
	Register(&BriefPacketDropScenario{})
	Register(&TotalPacketDropScenario{})
	Register(&PacketLossScenario{})
	Register(&LatencyScenario{})
	Register(&SingleNodeFailureScenario{})
	Register(&MajorityNodeFailureScenario{})
	Register(&FlappingNodeScenario{})
}
