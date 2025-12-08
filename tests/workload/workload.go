package workload

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pior/memcache"
)

// Workload represents a pattern of operations to execute against the memcache client
type Workload interface {
	// Name returns the workload identifier
	Name() string

	// Description returns a human-readable description
	Description() string

	// Execute runs a single operation and returns any error
	// This is called concurrently by multiple workers
	Execute(ctx context.Context, client *memcache.Client, workerID int) error
}

// Runner executes a workload with specified concurrency
type Runner struct {
	client      *memcache.Client
	workload    Workload
	concurrency int

	// Metrics
	opsSuccess atomic.Int64
	opsFailed  atomic.Int64
}

// NewRunner creates a workload runner
func NewRunner(client *memcache.Client, workload Workload, concurrency int) *Runner {
	return &Runner{
		client:      client,
		workload:    workload,
		concurrency: concurrency,
	}
}

// Run starts the workload and runs until context is cancelled
func (r *Runner) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	// Start workers
	for i := range r.concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r.worker(ctx, workerID)
		}(i)
	}

	// Wait for all workers to finish
	wg.Wait()
	return nil
}

func (r *Runner) worker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			err := r.workload.Execute(ctx, r.client, workerID)
			if err != nil {
				r.opsFailed.Add(1)
			} else {
				r.opsSuccess.Add(1)
			}
		}
	}
}

// Stats returns current operation statistics
func (r *Runner) Stats() WorkloadStats {
	success := r.opsSuccess.Load()
	failed := r.opsFailed.Load()
	total := success + failed

	var errorRate float64
	if total > 0 {
		errorRate = float64(failed) / float64(total)
	}

	return WorkloadStats{
		TotalOps:   total,
		SuccessOps: success,
		FailedOps:  failed,
		ErrorRate:  errorRate,
	}
}

// WorkloadStats holds workload execution statistics
type WorkloadStats struct {
	TotalOps   int64
	SuccessOps int64
	FailedOps  int64
	ErrorRate  float64
}

func (s WorkloadStats) String() string {
	return fmt.Sprintf("Total: %d, Success: %d, Failed: %d, Error Rate: %.2f%%",
		s.TotalOps, s.SuccessOps, s.FailedOps, s.ErrorRate*100)
}

// Registry of available workloads
var registry = make(map[string]Workload)

// Register adds a workload to the registry
func Register(w Workload) {
	registry[w.Name()] = w
}

// Get retrieves a workload by name
func Get(name string) (Workload, error) {
	w, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("workload not found: %s", name)
	}
	return w, nil
}

// All returns all registered workloads
func All() map[string]Workload {
	return registry
}

func init() {
	// Register standard workloads
	Register(&MixedWorkload{})
	Register(&GetHeavyWorkload{})
	Register(&SetHeavyWorkload{})
}
