// Degraded-mode scenarios: the client's behavior when servers are hung,
// slow, or partially down. These are the stress-level regression tests for
// the robustness work on timeouts (the operation timeout cannot be disabled),
// circuit breaker error attribution (a caller's budget miss is not a server
// failure), and partial-outage isolation (one dead server must not take down
// traffic to the others).
package stress

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pior/memcache"
)

// trackSlowest records the slowest observed duration across workers.
type trackSlowest struct {
	nanos atomic.Int64
}

func (s *trackSlowest) observe(d time.Duration) {
	for {
		cur := s.nanos.Load()
		if int64(d) <= cur || s.nanos.CompareAndSwap(cur, int64(d)) {
			return
		}
	}
}

func (s *trackSlowest) get() time.Duration { return time.Duration(s.nanos.Load()) }

// TestStress_HungServerDefaultTimeout verifies that the operation timeout
// cannot be disabled. Timeout < 0 used to mean "no timeout"; it now selects
// the default (1s). Against a hung server with a far-future context deadline
// — the exact configuration that would previously leave every read unbounded
// — all operations, including pipelined batches, must fail within the default
// timeout instead of hanging forever.
func TestStress_HungServerDefaultTimeout(t *testing.T) {
	proxy := newToxiproxy(t, stressMemcacheAddr)
	setLatency(t, proxy, time.Hour, 0)

	// One connection per worker: acquire-wait is bounded by the caller's
	// context (by design), so pool queueing must not pollute the per-op
	// elapsed times this test asserts on.
	client := memcache.NewClient(memcache.StaticServers(proxy.Listen), memcache.Config{
		MaxSize: int32(stressWorkers()),
		Timeout: -1, // an attempt to disable the operation timeout
	})
	t.Cleanup(client.Close)
	bc := memcache.NewBatchCommands(client)

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	const keySpace = 50
	var stats stressStats
	var slowest trackSlowest

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:hungdefault:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		start := time.Now()
		var err error
		switch rng.IntN(3) {
		case 0:
			err = client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
		case 1:
			_, err = client.Get(ctx, key)
		case 2:
			keys := make([]string, 1+rng.IntN(10))
			for i := range keys {
				keys[i] = fmt.Sprintf("stress:hungdefault:%d", rng.IntN(keySpace))
			}
			_, err = bc.MultiGet(ctx, keys)
		}
		slowest.observe(time.Since(start))

		if err != nil {
			stats.errors.Add(1)
		} else {
			t.Errorf("operation against a hung server unexpectedly succeeded (key %q)", key)
		}
	})

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(10), "the workload must actually run against the hung server")
	assert.Equal(t, stats.ops.Load(), stats.errors.Load(), "every op against a hung server must fail")

	// The headline guard: with the timeout "disabled", ops must still be
	// bounded by the 1s default, not by the 1h context deadline.
	assert.Less(t, slowest.get(), 3*time.Second,
		"slowest op %s must be bounded by the default operation timeout — Timeout<0 must not disable it", slowest.get())

	// The client must fully recover once the server responds normally again.
	setLatency(t, proxy, time.Millisecond, 0)
	assert.Eventually(t, func() bool {
		key := "stress:hungdefault:recovery"
		if err := client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|done")}); err != nil {
			return false
		}
		item, err := client.Get(ctx, key)
		return err == nil && item.Found
	}, 5*time.Second, 100*time.Millisecond, "client must recover once the server responds again")
}

// TestStress_BreakerCallerBudget is the non-regression test for circuit
// breaker error attribution: a latency-sensitive caller whose context
// deadline is shorter than the server's response time fails its own budget —
// that says nothing about server health and must not trip the breaker for
// everyone else.
//
// The server is healthy but slow (~30ms). Impatient workers issue ops with a
// 10ms deadline and fail constantly; patient workers run alongside with no
// per-op deadline. Without correct attribution the impatient failures open
// the breaker within milliseconds and the patient workers start failing with
// ErrOpenState; with it, the breaker never counts a single failure.
func TestStress_BreakerCallerBudget(t *testing.T) {
	proxy := newToxiproxy(t, stressMemcacheAddr)
	setLatency(t, proxy, 30*time.Millisecond, 5*time.Millisecond)

	client := memcache.NewClient(memcache.StaticServers(proxy.Listen), memcache.Config{
		MaxSize: 8,
		Timeout: time.Second,
		Breaker: memcache.BreakerConfig{
			// Aggressive on purpose: any mis-attributed failure trips it
			// quickly (a low ratio over a small sample), and a long open
			// interval keeps the tripped state visible.
			Enabled:          true,
			TripMinRequests:  3,
			TripFailureRatio: 0.1,
			OpenDuration:     time.Minute,
		},
	})
	t.Cleanup(client.Close)

	const callerBudget = 10 * time.Millisecond
	const keySpace = 100
	var impatientOps, impatientErrors, misattributed atomic.Int64
	var patientOps, patientErrors atomic.Int64

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:budget:%d", rng.IntN(keySpace))

		if workerID%2 == 0 {
			// Impatient caller: its 10ms budget can never be met at ~30ms RTT.
			opCtx, cancel := context.WithTimeout(context.Background(), callerBudget)
			defer cancel()

			impatientOps.Add(1)
			var err error
			if rng.IntN(2) == 0 {
				err = client.Set(opCtx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
			} else {
				_, err = client.Get(opCtx, key)
			}
			if err == nil {
				t.Errorf("impatient op unexpectedly met its %s budget", callerBudget)
				return
			}
			impatientErrors.Add(1)
			// The error must carry the caller's context error: that is what
			// the breaker exclusion keys on.
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				misattributed.Add(1)
			}
		} else {
			// Patient caller: no per-op deadline; must never be affected by
			// the impatient callers' failures.
			ctx := context.Background()

			patientOps.Add(1)
			var err error
			if rng.IntN(2) == 0 {
				err = client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
			} else {
				item, gerr := client.Get(ctx, key)
				err = gerr
				if gerr == nil && item.Found {
					checkValue(t, key, item.Value)
				}
			}
			if err != nil {
				patientErrors.Add(1)
			}
		}
	})

	t.Logf("impatient: ops=%d errors=%d, patient: ops=%d errors=%d",
		impatientOps.Load(), impatientErrors.Load(), patientOps.Load(), patientErrors.Load())
	require.Greater(t, impatientOps.Load(), int64(50), "the impatient workload must actually run")
	require.Greater(t, patientOps.Load(), int64(50), "the patient workload must actually run")

	assert.Zero(t, misattributed.Load(),
		"caller-budget failures must carry the caller's context error")
	assert.Zero(t, patientErrors.Load(),
		"patient callers must be unaffected: caller-budget misses must not trip the breaker")

	metrics := client.PoolMetrics()
	require.Len(t, metrics, 1)
	cb := metrics[0].Breaker
	t.Logf("breaker: state=%s failures=%d successes=%d", cb.State, cb.TotalFailures, cb.TotalSuccesses)
	assert.Equal(t, "closed", cb.State, "the breaker must stay closed against a healthy-but-slow server")
	assert.Zero(t, cb.TotalFailures, "caller-budget misses must not be counted as breaker failures")
}

// TestStress_BreakerTripsOnHungServer is the contrast case to
// TestStress_BreakerCallerBudget: when the operator-configured Timeout is the
// binding deadline (long-lived caller context), a hung server IS a server
// failure. The breaker must open, subsequent operations must be shed fast
// with ErrBreakerOpen instead of each paying the full Timeout, and the breaker
// must close again once the server recovers.
func TestStress_BreakerTripsOnHungServer(t *testing.T) {
	proxy := newToxiproxy(t, stressMemcacheAddr)
	setLatency(t, proxy, time.Hour, 0)

	const opTimeout = 100 * time.Millisecond
	client := memcache.NewClient(memcache.StaticServers(proxy.Listen), memcache.Config{
		MaxSize:        4,
		Timeout:        opTimeout,
		ConnectTimeout: time.Second,
		Breaker: memcache.BreakerConfig{
			Enabled:         true,
			TripMinRequests: 5,
			OpenDuration:    500 * time.Millisecond, // short open interval so recovery is observable
		},
	})
	t.Cleanup(client.Close)

	// A long-lived context: Config.Timeout is the binding deadline, so the
	// failures are attributed to the server and must count.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	const keySpace = 50
	var stats stressStats
	var shedByBreaker atomic.Int64
	var slowest trackSlowest

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:trip:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		start := time.Now()
		var err error
		if rng.IntN(2) == 0 {
			err = client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
		} else {
			_, err = client.Get(ctx, key)
		}
		slowest.observe(time.Since(start))

		if err == nil {
			t.Errorf("operation against a hung server unexpectedly succeeded (key %q)", key)
			return
		}
		stats.errors.Add(1)
		if errors.Is(err, memcache.ErrBreakerOpen) {
			shedByBreaker.Add(1)
		}
	})

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(50), "the workload must actually run against the hung server")
	assert.Equal(t, stats.ops.Load(), stats.errors.Load(), "every op against a hung server must fail")
	assert.Positive(t, shedByBreaker.Load(),
		"the breaker must open on a hung server and shed load instead of paying the timeout on every op")
	assert.Less(t, slowest.get(), 5*opTimeout,
		"slowest op %s must be bounded by Config.Timeout (%s)", slowest.get(), opTimeout)

	// Recovery: once the server responds again, a half-open probe must
	// succeed and close the breaker.
	setLatency(t, proxy, time.Millisecond, 0)
	assert.Eventually(t, func() bool {
		key := "stress:trip:recovery"
		if err := client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|done")}); err != nil {
			return false
		}
		item, err := client.Get(ctx, key)
		return err == nil && item.Found
	}, 5*time.Second, 100*time.Millisecond, "client must recover once the server responds again")

	metrics := client.PoolMetrics()
	require.Len(t, metrics, 1)
	assert.Equal(t, "closed", metrics[0].Breaker.State, "the breaker must close again after recovery")
}

// TestStress_PartialOutage verifies outage isolation on a multi-server
// client: when one of three servers hangs mid-run, traffic to the two
// healthy servers must continue unaffected, every error must attribute to
// the hung server, only its breaker may open, and the client must fully
// recover once the server heals. Wrong data is never acceptable at any
// phase (all three proxies front the same memcached, so the key-embedding
// invariant holds across shards).
func TestStress_PartialOutage(t *testing.T) {
	proxies := make([]*toxiproxy.Proxy, 3)
	addrs := make([]string, 3)
	for i := range proxies {
		proxies[i] = newToxiproxy(t, stressMemcacheAddr)
		addrs[i] = proxies[i].Listen
	}
	hung := proxies[0]

	const opTimeout = 150 * time.Millisecond
	client := memcache.NewClient(memcache.StaticServers(addrs...), memcache.Config{
		MaxSize:        4,
		Timeout:        opTimeout,
		ConnectTimeout: time.Second,
		Breaker: memcache.BreakerConfig{
			Enabled:         true,
			TripMinRequests: 5,
			// A short trip window so the healthy phase-A successes age out of
			// the rolling ratio quickly: once the shard hangs it serves no
			// successes, so within a window the ratio reaches 1.0 and trips.
			TripWindow:   time.Second,
			OpenDuration: time.Second,
		},
	})
	t.Cleanup(client.Close)
	bc := memcache.NewBatchCommands(client)
	ctx := context.Background()

	const keySpace = 300
	workload := func(stats *stressStats, onError func(error)) func(t *testing.T, workerID int, rng *rand.Rand) {
		return func(t *testing.T, workerID int, rng *rand.Rand) {
			key := fmt.Sprintf("stress:po:%d", rng.IntN(keySpace))
			stats.ops.Add(1)

			var err error
			switch rng.IntN(4) {
			case 0:
				err = client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
			case 1, 2:
				var item memcache.Item
				item, err = client.Get(ctx, key)
				if err == nil && item.Found {
					checkValue(t, key, item.Value)
				}
			case 3:
				keys := make([]string, 1+rng.IntN(10))
				for i := range keys {
					keys[i] = fmt.Sprintf("stress:po:%d", rng.IntN(keySpace))
				}
				var items []memcache.Item
				items, err = bc.MultiGet(ctx, keys)
				if err == nil {
					for i, item := range items {
						if item.Found {
							checkValue(t, keys[i], item.Value)
						}
					}
				}
			}
			if err != nil {
				stats.errors.Add(1)
				if onError != nil {
					onError(err)
				}
			}
		}
	}

	// Phase A — all servers healthy: the workload must be error-free.
	var healthyStats stressStats
	runWorkers(t, stressWorkers(), stressDuration(), workload(&healthyStats, nil))
	healthyStats.report(t)
	require.Greater(t, healthyStats.ops.Load(), int64(100), "the healthy-phase workload must actually run")
	require.Zero(t, healthyStats.errors.Load(), "no errors expected while all servers are healthy")

	// Phase B — hang one server mid-traffic. A poller watches the per-server
	// breakers: the hung server's breaker must open; the healthy servers'
	// breakers must never leave closed.
	setLatency(t, hung, time.Hour, 0)

	var sawHungOpen, sawHealthyNotClosed atomic.Bool
	pollerStop := make(chan struct{})
	var poller sync.WaitGroup
	poller.Go(func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pollerStop:
				return
			case <-ticker.C:
				for _, pm := range client.PoolMetrics() {
					switch {
					case pm.Addr == hung.Listen && pm.Breaker.State == "open":
						sawHungOpen.Store(true)
					case pm.Addr != hung.Listen && pm.Breaker.State != "closed":
						sawHealthyNotClosed.Store(true)
					}
				}
			}
		}
	})

	var outageStats stressStats
	var misattributed atomic.Int64
	onError := func(err error) {
		var opErr *memcache.OpError
		if !errors.As(err, &opErr) || opErr.Server != hung.Listen {
			misattributed.Add(1)
		}
	}
	runWorkers(t, stressWorkers(), stressDuration(), workload(&outageStats, onError))
	close(pollerStop)
	poller.Wait()

	outageStats.report(t)
	successes := outageStats.ops.Load() - outageStats.errors.Load()
	require.Greater(t, outageStats.ops.Load(), int64(100), "the outage-phase workload must actually run")
	assert.Positive(t, outageStats.errors.Load(), "the hung server must actually cause failures")
	assert.Positive(t, successes, "traffic to the healthy servers must continue during the outage")
	assert.Zero(t, misattributed.Load(), "every outage error must attribute to the hung server")
	assert.True(t, sawHungOpen.Load(), "the hung server's breaker must open during the outage")
	assert.False(t, sawHealthyNotClosed.Load(), "the healthy servers' breakers must stay closed")

	// Phase C — heal the hung server: every shard must serve again, and the
	// hung server's breaker must close. 24 keys make the odds of none of
	// them hashing onto the healed server negligible ((2/3)^24 ≈ 6e-5).
	setLatency(t, hung, time.Millisecond, 0)
	assert.Eventually(t, func() bool {
		for i := range 24 {
			key := fmt.Sprintf("stress:po:recovery:%d", i)
			if err := client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|done")}); err != nil {
				return false
			}
			item, err := client.Get(ctx, key)
			if err != nil || !item.Found {
				return false
			}
		}
		for _, pm := range client.PoolMetrics() {
			if pm.Breaker.State != "closed" {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond, "all shards must serve again and all breakers must close after the outage heals")
}
