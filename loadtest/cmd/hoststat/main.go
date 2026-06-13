// Command hoststat samples host CPU/memory/network/pressure from /proc at a
// fixed interval and writes one JSON object per line. It runs on every load-test
// VM (client and server) so a run can be verified and tuned. No external agent.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pior/memcache/loadtest/internal/hoststat"
)

func main() {
	interval := flag.Duration("interval", 5*time.Second, "sampling interval")
	duration := flag.Duration("duration", 0, "total run time (0 = until signalled)")
	out := flag.String("out", "", "output file for JSONL (default stdout)")
	runID := flag.String("run-id", "", "run id, echoed into each sample line")
	vm := flag.String("vm", "", "vm name, echoed into each sample line")
	flag.Parse()

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hoststat:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	sampler := hoststat.NewSampler()
	enc := json.NewEncoder(w)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	emit := func() {
		s := sampler.Sample()
		line := struct {
			RunID string `json:"run_id,omitempty"`
			VM    string `json:"vm,omitempty"`
			hoststat.Sample
		}{RunID: *runID, VM: *vm, Sample: s}
		_ = enc.Encode(line)
	}

	emit() // prime the sampler (warmup sample, establishes the baseline)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}
