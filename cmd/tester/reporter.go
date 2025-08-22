package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type Reporter struct {
	name      string
	startedAt time.Time
	workers   int

	counter int64
	info    atomic.Value
}

func NewReporter(ctx context.Context, name string, workers int) *Reporter {
	r := &Reporter{
		name:      name,
		startedAt: time.Now(),
		workers:   workers,
	}

	go func() {
		ticker := time.Tick(2 * time.Second)
		lastTick := time.Now()
		lastCount := int64(0)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker:
				elapsed := time.Since(lastTick)
				lastTick = time.Now()
				currentCount := atomic.LoadInt64(&r.counter)
				processed := currentCount - lastCount
				lastCount = currentCount
				rate := float64(processed) / elapsed.Seconds()
				fmt.Printf("[%s] Current rate: %.2f op/s (info: %v)\n", r.name, rate, r.info.Load())
			}
		}
	}()

	return r
}

func (r *Reporter) Tick(count int64) {
	atomic.AddInt64(&r.counter, count)
}

func (r *Reporter) TickInfo(count int64, info any) {
	atomic.AddInt64(&r.counter, count)
	r.info.Store(info)
}

func (r *Reporter) Stop() {
	elapsed := time.Since(r.startedAt)
	currentCount := atomic.LoadInt64(&r.counter)
	rate := float64(currentCount) / elapsed.Seconds()

	ratePerWorker := rate / float64(r.workers)

	operationTime := time.Duration(0)
	if ratePerWorker > 0 {
		operationTime = time.Second / time.Duration(rate/float64(r.workers))
	}

	fmt.Printf("[%s] Final rate: %.2f op/s - %.2f op/s per worker - %s per operation - total operations: %d (last info: %v)\n", r.name, rate, ratePerWorker, operationTime, currentCount, r.info.Load())
}
