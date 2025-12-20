package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pior/memcache/tests/internal/promexporter"
	"github.com/pior/memcache/tests/scenarios"
	"github.com/pior/memcache/tests/testutils"
)

func main() {
	// Flags
	scenarioName := flag.String("scenario", "", "Specific scenario to run (or 'all' for all)")
	listScenarios := flag.Bool("list", false, "List available scenarios")
	loop := flag.Bool("loop", false, "Loop scenarios continuously")
	metricsPort := flag.String("metrics-port", ":9092", "Port for Prometheus metrics")
	maxProcs := flag.Int("max-procs", 2, "Maximum number of CPU cores to use (GOMAXPROCS)")

	flag.Parse()

	// Limit CPU cores
	runtime.GOMAXPROCS(*maxProcs)

	log.Printf("Starting memcache scenario controller")
	log.Printf("  Max CPU cores: %d", *maxProcs)
	log.Printf("  Metrics: http://localhost%s/metrics", *metricsPort)

	// Setup Prometheus exporter
	exporter := promexporter.NewExporter()
	go func() {
		log.Printf("Starting metrics server on %s", *metricsPort)
		if err := exporter.ServeHTTP(*metricsPort); err != nil {
			log.Fatalf("Metrics server error: %v", err)
		}
	}()

	// Setup toxiproxy
	log.Println("Initializing toxiproxy...")
	toxiConfig := testutils.DefaultToxiproxyConfig()
	_, proxies, err := testutils.SetupToxiproxy(toxiConfig)
	if err != nil {
		log.Fatalf("Error setting up toxiproxy: %v\nMake sure to run: docker compose up -d", err)
	}
	defer testutils.CleanupToxiproxy(proxies)

	// Create scenario suites
	packetLossSuite := scenarios.NewPacketLossScenarioSuite(exporter.ScenarioMetrics())
	latencySuite := scenarios.NewLatencyScenarioSuite(exporter.ScenarioMetrics(), 200) // +200ms latency
	multiFailureSuite := scenarios.NewMultiFailureScenarioSuite(exporter.ScenarioMetrics())

	// Collect all scenarios
	allScenarios := make(map[string]scenarios.Scenario)

	// Add packet loss scenarios
	for _, s := range packetLossSuite.CreateScenarios() {
		allScenarios[s.Name()] = s
	}

	// Add latency scenarios
	for _, s := range latencySuite.CreateScenarios() {
		allScenarios[s.Name()] = s
	}

	// Add multi-failure scenarios
	for _, s := range multiFailureSuite.CreateScenarios() {
		allScenarios[s.Name()] = s
	}

	// List scenarios if requested
	if *listScenarios {
		printScenarios(allScenarios)
		return
	}

	if *scenarioName == "" {
		log.Fatalf("No scenario specified. Use -scenario=<name> or -scenario=all or -list")
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("\nReceived interrupt, shutting down...")
		cancel()
	}()

	// Determine which scenarios to run
	var scenariosToRun []scenarios.Scenario
	if *scenarioName == "all" {
		// Run all scenarios in alphabetical order
		names := make([]string, 0, len(allScenarios))
		for name := range allScenarios {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			scenariosToRun = append(scenariosToRun, allScenarios[name])
		}
	} else {
		// Run specific scenario
		scenario, ok := allScenarios[*scenarioName]
		if !ok {
			log.Fatalf("Scenario not found: %s", *scenarioName)
		}
		scenariosToRun = []scenarios.Scenario{scenario}
	}

	log.Printf("Will run %d scenario(s)", len(scenariosToRun))

	// Run scenarios
	runCount := 1
	for {
		for i, scenario := range scenariosToRun {
			log.Printf("\n=== Scenario %d/%d: %s ===", i+1, len(scenariosToRun), scenario.Name())
			log.Printf("Description: %s", scenario.Description())
			log.Printf("Run: %d", runCount)

			// Run scenario
			startTime := time.Now()
			err := scenario.Run(ctx, proxies)
			duration := time.Since(startTime)

			if err != nil {
				if err == context.Canceled {
					log.Printf("Scenario %s canceled", scenario.Name())
					goto done
				}
				log.Printf("ERROR: Scenario %s failed: %v", scenario.Name(), err)
			} else {
				log.Printf("SUCCESS: Scenario %s completed in %s", scenario.Name(), duration.Round(time.Second))
			}

			// Clean up toxiproxy state between scenarios
			log.Printf("Cleaning up toxiproxy state...")
			if err := testutils.CleanupToxiproxy(proxies); err != nil {
				log.Printf("Warning: Failed to cleanup toxiproxy: %v", err)
			}

			// Brief pause between scenarios
			if i < len(scenariosToRun)-1 {
				select {
				case <-ctx.Done():
					goto done
				case <-time.After(5 * time.Second):
				}
			}
		}

		runCount++

		if !*loop {
			log.Printf("\nAll scenarios complete (1 run)")
			break
		}

		log.Printf("\nCompleted run %d, starting next iteration...\n", runCount-1)

		// Pause between loops
		select {
		case <-ctx.Done():
			goto done
		case <-time.After(10 * time.Second):
		}
	}

done:
	log.Println("Scenario controller shutting down")
}

func printScenarios(scenarios map[string]scenarios.Scenario) {
	fmt.Println("\n=== Available Scenarios ===")

	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)

	// Group by type
	fmt.Println("Packet Loss Scenarios:")
	for _, name := range names {
		if strings.HasPrefix(name, "packet-loss") {
			s := scenarios[name]
			fmt.Printf("  %-35s %s\n", name, s.Description())
		}
	}

	fmt.Println("\nLatency Scenarios:")
	for _, name := range names {
		if strings.HasPrefix(name, "latency") {
			s := scenarios[name]
			fmt.Printf("  %-35s %s\n", name, s.Description())
		}
	}

	fmt.Println("\nMultiple Simultaneous Failures:")
	for _, name := range names {
		if strings.HasPrefix(name, "multi-failure") {
			s := scenarios[name]
			fmt.Printf("  %-35s %s\n", name, s.Description())
		}
	}

	fmt.Println("\nUsage:")
	fmt.Println("  -scenario=<name>     Run specific scenario")
	fmt.Println("  -scenario=all        Run all scenarios sequentially")
	fmt.Println("  -loop                Loop scenarios continuously")
	fmt.Println("  -list                List available scenarios")
}
