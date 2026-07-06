package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pior/memcache/loadtest/internal/chaos"
)

// ServerVMName returns the deterministic name of server VM idx in a run,
// shared by the VM planner and the chaos schedule builder.
func ServerVMName(runID string, idx int) string {
	return fmt.Sprintf("mclt-%s-srv-%d", runID, idx)
}

// chaosObject is the GCS object path (relative to the bucket) holding a
// server VM's chaos schedule.
func chaosObject(runID, vmName string) string {
	return fmt.Sprintf("%s/chaos/%s.json", runID, vmName)
}

// BuildChaosSchedules renders the per-server-VM chaos schedules for a run:
// one schedule per server VM (empty when the VM is not targeted, so chaosd
// still runs and heals on teardown). cfg.Chaos is a preset name or a path to
// a JSON file mapping server index ("0", "1", …) to an event list. Returns
// nil when chaos is disabled.
func BuildChaosSchedules(cfg RunConfig, runID string) (map[string][]byte, error) {
	if cfg.Chaos == "" {
		return nil, nil
	}

	events, err := loadChaosEvents(cfg)
	if err != nil {
		return nil, err
	}

	schedules := make(map[string][]byte, cfg.ServerVMs)
	for i := range cfg.ServerVMs {
		s := chaos.Schedule{
			Ports:    chaos.PortSpec(MemcachePort, cfg.InstancesPerVM),
			Relaunch: memcachedStartLoop(cfg.InstancesPerVM, cfg.MemoryMB),
			Events:   events[i],
		}
		schedules[ServerVMName(runID, i)] = s.JSON()
	}
	return schedules, nil
}

// loadChaosEvents resolves cfg.Chaos into per-server-index event lists:
// a *.json path is a custom timeline, anything else a preset name.
func loadChaosEvents(cfg RunConfig) (map[int][]chaos.Event, error) {
	if !strings.HasSuffix(cfg.Chaos, ".json") {
		return chaos.Preset(cfg.Chaos, cfg.ServerVMs, cfg.Duration)
	}

	data, err := os.ReadFile(cfg.Chaos)
	if err != nil {
		return nil, fmt.Errorf("chaos file: %w", err)
	}
	byIndex := map[string][]chaos.Event{}
	if err := json.Unmarshal(data, &byIndex); err != nil {
		return nil, fmt.Errorf("chaos file %s: %w", cfg.Chaos, err)
	}
	events := make(map[int][]chaos.Event, len(byIndex))
	for key, evs := range byIndex {
		idx, err := strconv.Atoi(key)
		if err != nil || idx < 0 || idx >= cfg.ServerVMs {
			return nil, fmt.Errorf("chaos file %s: key %q is not a server index in [0,%d)", cfg.Chaos, key, cfg.ServerVMs)
		}
		events[idx] = evs
	}
	return events, nil
}
