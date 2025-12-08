package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/pior/memcache/tests/metrics"
	"github.com/pior/memcache/tests/scenarios"
	"github.com/pior/memcache/tests/testutils"
	"github.com/pior/memcache/tests/tui"
	"github.com/pior/memcache/tests/workload"
	"github.com/sony/gobreaker/v2"
)

func main() {
	// Command-line flags
	scenarioName := flag.String("scenario", "", "Specific scenario to run (default: continuous workload)")
	runs := flag.Int("runs", 0, "Number of scenario runs (0 = continuous)")
	concurrency := flag.Int("concurrency", 100, "Number of concurrent workers")
	metricsInterval := flag.Duration("metrics-interval", 2*time.Second, "How often to print metrics (ignored with TUI)")
	listScenarios := flag.Bool("list", false, "List available scenarios and exit")
	workloadName := flag.String("workload", "mixed", "Workload pattern to use")
	noTUI := flag.Bool("no-tui", false, "Disable TUI dashboard (use plain text output)")

	flag.Parse()

	// List scenarios if requested
	if *listScenarios {
		printScenarios()
		return
	}

	fmt.Println("========================================")
	fmt.Println("  Memcache Reliability Test Runner")
	fmt.Println("========================================")
	fmt.Printf("Concurrency: %d workers\n", *concurrency)
	fmt.Printf("Workload: %s\n", *workloadName)
	if *scenarioName != "" {
		fmt.Printf("Scenario: %s\n", *scenarioName)
		if *runs > 0 {
			fmt.Printf("Runs: %d\n", *runs)
		} else {
			fmt.Println("Runs: Continuous (Ctrl+C to stop)")
		}
	} else {
		fmt.Println("Scenario: None (workload only)")
	}
	fmt.Println("========================================")
	fmt.Println()

	// Setup toxiproxy
	fmt.Println("[Setup] Initializing toxiproxy...")
	toxiConfig := testutils.DefaultToxiproxyConfig()
	_, proxies, err := testutils.SetupToxiproxy(toxiConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up toxiproxy: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure to run: docker compose up -d\n")
		os.Exit(1)
	}
	defer testutils.CleanupToxiproxy(proxies)

	// Setup memcache client
	fmt.Println("[Setup] Creating memcache client...")
	clientConfig := testutils.DefaultMemcacheClientConfig()

	// Set up OnStateChange callback to capture circuit breaker state transitions
	// We'll wire this to the collector after it's created
	var collector *metrics.Collector
	clientConfig.CircuitBreakerSettings.OnStateChange = func(name string, from, to gobreaker.State) {
		if collector != nil {
			collector.RecordCircuitBreakerChange(name, from.String(), to.String())
		}
	}

	client, err := testutils.SetupMemcacheClient(clientConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Wait for client to be healthy
	ctx := context.Background()
	if err := testutils.WaitForHealthy(ctx, client); err != nil {
		fmt.Fprintf(os.Stderr, "Error waiting for client health: %v\n", err)
		os.Exit(1)
	}

	// Get workload
	wl, err := workload.Get(*workloadName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading workload: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[Setup] Workload: %s - %s\n", wl.Name(), wl.Description())

	// Create workload runner
	runner := workload.NewRunner(client, wl, *concurrency)

	// Create metrics collector with appropriate interval
	// Use 500ms for TUI mode to keep data fresh, otherwise use user-specified interval
	collectorInterval := *metricsInterval
	if !*noTUI {
		collectorInterval = 500 * time.Millisecond
	}
	collector = metrics.NewCollector(client, runner, collectorInterval)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[Main] Received interrupt, shutting down...")
		cancel()
	}()

	// Start workload runner
	fmt.Printf("\n[Main] Starting workload with %d workers\n", *concurrency)
	go func() {
		if err := runner.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Workload error: %v\n", err)
		}
	}()

	// Start metrics collector
	go collector.Start(ctx)

	// Use TUI or plain text output
	if !*noTUI {
		// TUI mode - works with or without scenarios
		fmt.Println("\n[Main] Starting TUI dashboard...")
		time.Sleep(1 * time.Second) // Let some initial data collect

		dashboard := tui.NewDashboard(collector)

		// If scenario is specified, run it in background
		if *scenarioName != "" {
			// Get all scenarios for keyboard navigation
			allScenarios := scenarios.All()
			scenarioNames := make([]string, 0, len(allScenarios))
			for name := range allScenarios {
				scenarioNames = append(scenarioNames, name)
			}
			sort.Strings(scenarioNames)
			dashboard.SetAvailableScenarios(scenarioNames)

			dashboard.AddLog("Letting workload stabilize...")
			time.Sleep(5 * time.Second) // Let workload stabilize

			currentScenarioName := *scenarioName
			scenarioSwitchCh := dashboard.GetScenarioSwitchChannel()

			// Run scenario in background with switching support
			go func() {
				runCount := 1
				var scenarioCancel context.CancelFunc
				scenarioCtx := ctx

				for {
					// Load current scenario
					scenario, err := scenarios.Get(currentScenarioName)
					if err != nil {
						dashboard.AddLog(fmt.Sprintf("Error loading scenario %s: %v", currentScenarioName, err))
						select {
						case <-ctx.Done():
							return
						case <-time.After(5 * time.Second):
							continue
						}
					}

					dashboard.SetScenario(scenario.Name(), scenario.Description())

					// Create cancellable context for this scenario run
					scenarioCtx, scenarioCancel = context.WithCancel(ctx)

					// Update run info
					if *runs > 0 {
						dashboard.AddLog(fmt.Sprintf("Starting scenario run %d/%d: %s", runCount, *runs, scenario.Description()))
					} else {
						dashboard.AddLog(fmt.Sprintf("Starting scenario run %d: %s", runCount, scenario.Description()))
					}

					// Run scenario in separate goroutine
					scenarioDone := make(chan error, 1)
					go func() {
						// Suppress scenario stdout to avoid interfering with TUI
						oldStdout := os.Stdout
						r, w, _ := os.Pipe()
						os.Stdout = w
						go io.Copy(io.Discard, r) // Drain the pipe

						err := scenario.Run(scenarioCtx, proxies)
						w.Close()
						os.Stdout = oldStdout

						scenarioDone <- err
					}()

					// Wait for scenario completion or switch signal
					select {
					case <-ctx.Done():
						if scenarioCancel != nil {
							scenarioCancel()
						}
						return
					case newScenario := <-scenarioSwitchCh:
						// Cancel current scenario and switch
						if scenarioCancel != nil {
							scenarioCancel()
						}
						<-scenarioDone // Wait for scenario to actually stop

						// Clean up toxiproxy state from previous scenario
						dashboard.AddLog("Cleaning up toxiproxy state...")
						if err := testutils.CleanupToxiproxy(proxies); err != nil {
							dashboard.AddLog(fmt.Sprintf("Warning: Failed to cleanup toxiproxy: %v", err))
						}

						currentScenarioName = newScenario
						runCount = 1
						time.Sleep(500 * time.Millisecond) // Brief pause before starting new scenario
						continue
					case err := <-scenarioDone:
						if scenarioCancel != nil {
							scenarioCancel()
						}

						if err != nil && err != context.Canceled {
							dashboard.AddLog(fmt.Sprintf("Scenario run %d error: %v", runCount, err))
						} else if err == context.Canceled {
							dashboard.AddLog("Scenario canceled")
							// If canceled, check if it was a switch or main context cancellation
							select {
							case <-ctx.Done():
								return
							default:
								// Switched scenario, continue with new one
								continue
							}
						} else {
							dashboard.AddLog(fmt.Sprintf("Scenario run %d complete", runCount))
						}

						// Check if we should continue
						if *runs > 0 && runCount >= *runs {
							dashboard.SetScenario("", fmt.Sprintf("All %d runs complete", *runs))
							dashboard.AddLog(fmt.Sprintf("All %d scenario runs complete", *runs))
							cancel()
							return
						}

						runCount++

						// Brief pause between runs
						select {
						case <-ctx.Done():
							return
						case <-time.After(2 * time.Second):
						}
					}
				}
			}()
		}

		// Run TUI in main goroutine - it will block until user presses 'q' or Ctrl+C
		if err := dashboard.Run(ctx.Done()); err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		}

		// TUI exited (user pressed 'q' or Ctrl+C), cancel context to stop workers
		cancel()
		time.Sleep(200 * time.Millisecond) // Let workers shut down

		// Print summary after TUI exits
		collector.PrintSummary()
		fmt.Println("\n[Main] Test complete")
	} else {
		// Plain text mode - for scenarios or when TUI is disabled
		metricsTicker := time.NewTicker(*metricsInterval)
		defer metricsTicker.Stop()

		// Run scenario if specified
		if *scenarioName != "" {
			// Wait a bit for workload to stabilize
			fmt.Println("[Main] Letting workload stabilize for 5s...")
			time.Sleep(5 * time.Second)

			scenario, err := scenarios.Get(*scenarioName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading scenario: %v\n", err)
				os.Exit(1)
			}

			// Run scenario loop
			runCount := 1
			for {
				if *runs > 0 {
					fmt.Printf("\n[Main] Starting scenario run %d/%d: %s\n", runCount, *runs, scenario.Description())
				} else {
					fmt.Printf("\n[Main] Starting scenario run %d: %s\n", runCount, scenario.Description())
				}
				fmt.Println("========================================")

				err := scenario.Run(ctx, proxies)

				fmt.Println("========================================")
				if err != nil && err != context.Canceled {
					fmt.Fprintf(os.Stderr, "Scenario run %d error: %v\n", runCount, err)
				} else if err == context.Canceled {
					fmt.Println("[Main] Scenario canceled")
					break
				} else {
					fmt.Printf("[Main] Scenario run %d complete\n", runCount)
				}

				// Check if we should continue
				if *runs > 0 && runCount >= *runs {
					fmt.Printf("[Main] All %d scenario runs complete\n", *runs)
					cancel()
					break
				}

				runCount++

				// Brief pause between runs
				fmt.Println("[Main] Pausing 2s before next run...")
				select {
				case <-ctx.Done():
					goto done
				case <-time.After(2 * time.Second):
				}
			}

			// Continue printing metrics after scenarios complete
			if *runs == 0 {
				fmt.Println("[Main] Continuing workload (Ctrl+C to stop)...")
			}
			for {
				select {
				case <-ctx.Done():
					goto done
				case <-metricsTicker.C:
					collector.PrintLatest()
				}
			}
		} else {
			// No scenario - just run workload and print metrics
			for {
				select {
				case <-ctx.Done():
					goto done
				case <-metricsTicker.C:
					collector.PrintLatest()
				}
			}
		}

	done:
		// Print final summary
		time.Sleep(500 * time.Millisecond) // Let final metrics be collected
		collector.PrintSummary()

		fmt.Println("\n[Main] Test complete")
	}
}

func printScenarios() {
	fmt.Println("Available Scenarios:")
	fmt.Println("====================")

	allScenarios := scenarios.All()
	names := make([]string, 0, len(allScenarios))
	for name := range allScenarios {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		s := allScenarios[name]
		fmt.Printf("  %-25s %s\n", name, s.Description())
	}

	fmt.Println("\nAvailable Workloads:")
	fmt.Println("====================")

	allWorkloads := workload.All()
	workloadNames := make([]string, 0, len(allWorkloads))
	for name := range allWorkloads {
		workloadNames = append(workloadNames, name)
	}
	sort.Strings(workloadNames)

	for _, name := range workloadNames {
		w := allWorkloads[name]
		fmt.Printf("  %-25s %s\n", name, w.Description())
	}
}
