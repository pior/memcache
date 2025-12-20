package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/pior/memcache/tests/internal/promexporter"
	"github.com/pior/memcache/tests/testutils"
)

// PhasedScenario provides a base implementation for 3-phase scenarios
type PhasedScenario struct {
	name               string
	description        string
	stabilizationTime  time.Duration
	testingTime        time.Duration
	recoveryTime       time.Duration
	applyPerturbation  func(ctx context.Context, proxies []*toxiproxy.Proxy) error
	removePerturbation func(proxies []*toxiproxy.Proxy) error
	metrics            *promexporter.ScenarioMetrics
}

// PhasedScenarioConfig configures a 3-phase scenario
type PhasedScenarioConfig struct {
	Name               string
	Description        string
	StabilizationTime  time.Duration // Default: 30s
	TestingTime        time.Duration // Required
	RecoveryTime       time.Duration // Default: 60s
	ApplyPerturbation  func(ctx context.Context, proxies []*toxiproxy.Proxy) error
	RemovePerturbation func(proxies []*toxiproxy.Proxy) error
}

// NewPhasedScenario creates a new 3-phase scenario
func NewPhasedScenario(config PhasedScenarioConfig, metrics *promexporter.ScenarioMetrics) *PhasedScenario {
	// Set defaults
	if config.StabilizationTime == 0 {
		config.StabilizationTime = 30 * time.Second
	}
	if config.RecoveryTime == 0 {
		config.RecoveryTime = 60 * time.Second
	}

	return &PhasedScenario{
		name:               config.Name,
		description:        config.Description,
		stabilizationTime:  config.StabilizationTime,
		testingTime:        config.TestingTime,
		recoveryTime:       config.RecoveryTime,
		applyPerturbation:  config.ApplyPerturbation,
		removePerturbation: config.RemovePerturbation,
		metrics:            metrics,
	}
}

func (s *PhasedScenario) Name() string {
	return s.name
}

func (s *PhasedScenario) Description() string {
	return s.description
}

// Run executes the 3-phase scenario
func (s *PhasedScenario) Run(ctx context.Context, proxies []*toxiproxy.Proxy) error {
	log.Printf("[Scenario %s] Starting 3-phase execution", s.name)

	// Ensure toxiproxy is clean before starting
	log.Printf("[Scenario %s] Ensuring clean toxiproxy state", s.name)
	if err := testutils.CleanupToxiproxy(proxies); err != nil {
		return fmt.Errorf("failed to clean toxiproxy before scenario: %w", err)
	}

	// Reset toxic metrics
	for _, proxy := range proxies {
		s.metrics.SetToxicActive(proxy.Name, "packet_loss", false)
		s.metrics.SetToxicActive(proxy.Name, "latency", false)
		s.metrics.SetToxicValue(proxy.Name, "packet_loss", "rate", 0)
		s.metrics.SetToxicValue(proxy.Name, "latency", "latency_ms", 0)
	}

	// Update metrics
	s.metrics.SetScenarioActive(true)
	s.metrics.SetPhaseDuration(s.name, "stabilization", s.stabilizationTime.Seconds())
	s.metrics.SetPhaseDuration(s.name, "testing", s.testingTime.Seconds())
	s.metrics.SetPhaseDuration(s.name, "recovery", s.recoveryTime.Seconds())

	defer func() {
		s.metrics.SetScenarioActive(false)
		s.metrics.SetPhase(s.name, 0) // idle
	}()

	// Phase 1: Stabilization
	log.Printf("[Scenario %s] Phase 1: Stabilization (%s)", s.name, s.stabilizationTime)
	s.metrics.SetPhase(s.name, 1)
	if err := s.waitOrCancel(ctx, s.stabilizationTime); err != nil {
		return err
	}

	// Phase 2: Testing (apply perturbation)
	log.Printf("[Scenario %s] Phase 2: Testing (%s) - Applying perturbation", s.name, s.testingTime)
	s.metrics.SetPhase(s.name, 2)

	if err := s.applyPerturbation(ctx, proxies); err != nil {
		s.metrics.RecordRun(s.name, false)
		return fmt.Errorf("failed to apply perturbation: %w", err)
	}

	if err := s.waitOrCancel(ctx, s.testingTime); err != nil {
		// Clean up before returning
		_ = s.removePerturbation(proxies)
		return err
	}

	// Remove perturbation before recovery phase
	log.Printf("[Scenario %s] Removing perturbation", s.name)
	if err := s.removePerturbation(proxies); err != nil {
		s.metrics.RecordRun(s.name, false)
		return fmt.Errorf("failed to remove perturbation: %w", err)
	}

	// Ensure toxiproxy is fully clean for recovery phase
	if err := testutils.CleanupToxiproxy(proxies); err != nil {
		log.Printf("[Scenario %s] Warning: failed to cleanup toxiproxy for recovery: %v", s.name, err)
	}

	// Phase 3: Recovery
	log.Printf("[Scenario %s] Phase 3: Recovery (%s)", s.name, s.recoveryTime)
	s.metrics.SetPhase(s.name, 3)
	if err := s.waitOrCancel(ctx, s.recoveryTime); err != nil {
		return err
	}

	log.Printf("[Scenario %s] Complete", s.name)
	s.metrics.RecordRun(s.name, true)
	return nil
}

func (s *PhasedScenario) waitOrCancel(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}
