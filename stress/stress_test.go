// Package stress holds the in-process stress and load tests for the memcache
// client. It lives in its own module so the heavy failure-injection
// dependencies (toxiproxy and its transitive packages) stay out of the main
// module's dependency graph.
//
// Run with:
//
//	go test -run TestStress -v ./...
//
// Requirements: memcached on 127.0.0.1:11211 (docker compose up).
//
// Tunables (environment variables):
//
//	STRESS_DURATION  duration of each scenario (default 5s)
//	STRESS_WORKERS   concurrent workers per scenario (default 16)
//
// The core invariant: every stored value embeds its key, so any response
// returning a value that doesn't match the requested key proves the
// connection got desynchronized — the worst possible failure for a cache
// client. Errors under churn/failure injection are acceptable; wrong data
// never is.
//
// Network failures are injected in-process: flakyProxy kills connections
// mid-stream, and an embedded toxiproxy adds latency and jitter (no
// toxiproxy daemon required).
package stress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pior/memcache"
	"github.com/pior/memcache/meta"
)

const stressMemcacheAddr = "127.0.0.1:11211"

func stressDuration() time.Duration {
	if v := os.Getenv("STRESS_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			panic("invalid STRESS_DURATION: " + err.Error())
		}
		return d
	}
	return 5 * time.Second
}

func stressWorkers() int {
	if v := os.Getenv("STRESS_WORKERS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
			panic("invalid STRESS_WORKERS: " + v)
		}
		return n
	}
	return 16
}

// stressValue builds a value that embeds its key, padded to a random size.
func stressValue(key string, rng *rand.Rand) []byte {
	padding := strings.Repeat("x", rng.IntN(512))
	return []byte(key + "|" + padding)
}

// checkValue verifies the key-embedding invariant.
func checkValue(t *testing.T, key string, value []byte) {
	t.Helper()
	if !strings.HasPrefix(string(value), key+"|") {
		t.Errorf("DESYNC: value for key %q starts with %q", key, truncate(string(value), 80))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// stressStats counts operation outcomes across workers.
type stressStats struct {
	ops    atomic.Int64
	errors atomic.Int64
}

func (s *stressStats) report(t *testing.T) {
	t.Helper()
	t.Logf("ops=%d errors=%d (%.2f%%)", s.ops.Load(), s.errors.Load(),
		100*float64(s.errors.Load())/max(float64(s.ops.Load()), 1))
}

// runWorkers runs fn concurrently until the duration elapses.
func runWorkers(t *testing.T, workers int, d time.Duration, fn func(t *testing.T, workerID int, rng *rand.Rand)) {
	t.Helper()
	deadline := time.Now().Add(d)

	var wg sync.WaitGroup
	for workerID := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(workerID), rand.Uint64()))
			for time.Now().Before(deadline) && !t.Failed() {
				fn(t, workerID, rng)
			}
		}()
	}
	wg.Wait()
}

// TestStress_MixedWorkload hammers the client with mixed operations on a
// shared key space and verifies that no operation ever observes data
// belonging to another key.
func TestStress_MixedWorkload(t *testing.T) {
	client := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{
		MaxSize: 8,
		Timeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	const keySpace = 200
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:mixed:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		switch rng.IntN(10) {
		case 0, 1, 2, 3: // 40% get
			item, err := client.Get(ctx, key)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if item.Found {
				checkValue(t, key, item.Value)
			}
		case 4, 5, 6: // 30% set
			if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
				stats.errors.Add(1)
			}
		case 7: // 10% delete
			if err := client.Delete(ctx, key); err != nil {
				stats.errors.Add(1)
			}
		case 8: // 10% add
			err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)})
			if err != nil {
				stats.errors.Add(1)
			}
		case 9: // 10% get with TTL flag via low-level API
			req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue().AddReturnTTL()
			resp, err := client.Execute(ctx, req)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if resp.Status == meta.StatusVA {
				checkValue(t, key, resp.Data)
			}
		}
	})

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(1000), "the workload must actually run")
	assert.Zero(t, stats.errors.Load(), "no errors expected against a healthy local server")
}

// TestStress_BatchWorkload runs concurrent pipelined batches and verifies
// positional integrity: response i must belong to key i.
func TestStress_BatchWorkload(t *testing.T) {
	client := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{
		MaxSize: 8,
		Timeout: 2 * time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()
	bc := memcache.NewBatchCommands(client)

	const keySpace = 500
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		batchSize := 1 + rng.IntN(50)
		stats.ops.Add(1)

		if rng.IntN(2) == 0 {
			items := make([]memcache.Item, batchSize)
			for i := range items {
				key := fmt.Sprintf("stress:batch:%d", rng.IntN(keySpace))
				items[i] = memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}
			}
			if err := bc.MultiSet(ctx, items); err != nil {
				stats.errors.Add(1)
			}
		} else {
			keys := make([]string, batchSize)
			for i := range keys {
				keys[i] = fmt.Sprintf("stress:batch:%d", rng.IntN(keySpace))
			}
			items, err := bc.MultiGet(ctx, keys)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			require.Len(t, items, len(keys))
			for i, item := range items {
				assert.Equal(t, keys[i], item.Key, "response %d must belong to key %d", i, i)
				if item.Found {
					checkValue(t, keys[i], item.Value)
				}
			}
		}
	})

	stats.report(t)
	assert.Zero(t, stats.errors.Load(), "no errors expected against a healthy local server")
}

// TestStress_ErrorInjection interleaves requests that produce per-request
// CLIENT_ERROR responses (arithmetic on non-numeric values) with normal
// operations. The protocol errors must never desynchronize other requests.
func TestStress_ErrorInjection(t *testing.T) {
	client := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{
		MaxSize: 4,
		Timeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	// Non-numeric keys for poisoned arithmetic requests.
	const poisonedKeys = 10
	for i := range poisonedKeys {
		key := fmt.Sprintf("stress:poison:%d", i)
		require.NoError(t, client.Set(ctx, memcache.Item{Key: key, Value: []byte("not-a-number")}))
	}

	const keySpace = 100
	var stats stressStats
	var poisonOps atomic.Int64

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		stats.ops.Add(1)

		switch rng.IntN(10) {
		case 0: // 10% poisoned arithmetic -> CLIENT_ERROR response
			poisonOps.Add(1)
			key := fmt.Sprintf("stress:poison:%d", rng.IntN(poisonedKeys))
			_, err := client.Increment(ctx, key, 1, memcache.NoTTL)
			if err == nil {
				t.Error("poisoned increment must fail")
			}
		case 1: // 10% poisoned arithmetic inside a batch
			poisonOps.Add(1)
			reqs := []*meta.Request{
				meta.NewRequest(meta.CmdArithmetic, fmt.Sprintf("stress:poison:%d", rng.IntN(poisonedKeys)), nil).AddReturnValue(),
				meta.NewRequest(meta.CmdGet, fmt.Sprintf("stress:err:%d", rng.IntN(keySpace)), nil).AddReturnValue(),
			}
			resps, err := client.ExecuteBatch(ctx, reqs)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			require.Len(t, resps, 2)
			if resps[1].Status == meta.StatusVA {
				checkValue(t, reqs[1].Key, resps[1].Data)
			}
		default: // 80% normal traffic
			key := fmt.Sprintf("stress:err:%d", rng.IntN(keySpace))
			if rng.IntN(2) == 0 {
				if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
					stats.errors.Add(1)
				}
			} else {
				item, err := client.Get(ctx, key)
				if err != nil {
					stats.errors.Add(1)
					return
				}
				if item.Found {
					checkValue(t, key, item.Value)
				}
			}
		}
	})

	stats.report(t)
	t.Logf("poisoned ops: %d", poisonOps.Load())
	assert.Zero(t, stats.errors.Load(), "normal traffic must not fail because of poisoned requests")
}

// TestStress_ConnectionChurn runs the workload with aggressive connection
// lifecycle limits and health checking, forcing constant reconnections.
// The pool is saturated (more workers than connections) on purpose:
// MaxConnLifetime must be enforced at release time, not only on idle
// connections by the health check loop.
func TestStress_ConnectionChurn(t *testing.T) {
	client := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{
		MaxSize:             4,
		Timeout:             time.Second,
		MaxConnLifetime:     100 * time.Millisecond,
		MaxConnIdleTime:     50 * time.Millisecond,
		HealthCheckInterval: 20 * time.Millisecond,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	const keySpace = 50
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:churn:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		if rng.IntN(2) == 0 {
			if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
				stats.errors.Add(1)
			}
		} else {
			item, err := client.Get(ctx, key)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if item.Found {
				checkValue(t, key, item.Value)
			}
		}
	})

	stats.report(t)
	for _, pm := range client.PoolMetrics() {
		t.Logf("pool %s: created=%d destroyed=%d", pm.Addr, pm.Conns.CreatedConns, pm.Conns.DestroyedConns)
		assert.Greater(t, pm.Conns.DestroyedConns, uint64(10), "lifecycle limits must actually churn connections")
	}
	assert.Zero(t, stats.errors.Load(), "connection churn must be invisible to callers")
}

// TestStress_Counters runs concurrent increments and verifies the final
// counter values are exact: lost or duplicated arithmetic would show here.
func TestStress_Counters(t *testing.T) {
	client := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{
		MaxSize: 8,
		Timeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	const counters = 5
	for i := range counters {
		require.NoError(t, client.Delete(ctx, fmt.Sprintf("stress:counter:%d", i)))
	}

	var increments [counters]atomic.Int64

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		idx := rng.IntN(counters)
		delta := int64(1 + rng.IntN(10))
		if _, err := client.Increment(ctx, fmt.Sprintf("stress:counter:%d", idx), delta, memcache.NoTTL); err != nil {
			t.Errorf("increment failed: %v", err)
			return
		}
		increments[idx].Add(delta)
	})

	for i := range counters {
		got, err := client.Increment(ctx, fmt.Sprintf("stress:counter:%d", i), 0, memcache.NoTTL)
		require.NoError(t, err)
		want := increments[i].Load()
		assert.Equal(t, want, got, "counter %d must equal the sum of recorded increments", i)
		t.Logf("counter %d: %d increments applied", i, want)
	}
}

// =============================================================================
// Failure injection via a flaky TCP proxy
// =============================================================================

// flakyProxy forwards TCP traffic to a backend and abruptly closes random
// connections, simulating network failures and server restarts.
type flakyProxy struct {
	listener net.Listener
	backend  string

	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	accepting atomic.Bool
	killRate  atomic.Int64 // per-mille chance to kill the connection after each chunk
}

func newFlakyProxy(t *testing.T, backend string) *flakyProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	p := &flakyProxy{
		listener: listener,
		backend:  backend,
		conns:    make(map[net.Conn]struct{}),
	}
	p.accepting.Store(true)
	t.Cleanup(p.Stop)

	go p.acceptLoop()
	return p
}

func (p *flakyProxy) Addr() string { return p.listener.Addr().String() }

func (p *flakyProxy) SetKillRatePerMille(rate int64) { p.killRate.Store(rate) }

func (p *flakyProxy) Stop() {
	p.accepting.Store(false)
	p.listener.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for conn := range p.conns {
		conn.Close()
	}
}

func (p *flakyProxy) track(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns[conn] = struct{}{}
}

func (p *flakyProxy) untrack(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, conn)
}

func (p *flakyProxy) acceptLoop() {
	for p.accepting.Load() {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(client)
	}
}

func (p *flakyProxy) handle(client net.Conn) {
	defer client.Close()
	p.track(client)
	defer p.untrack(client)

	server, err := net.DialTimeout("tcp", p.backend, time.Second)
	if err != nil {
		return
	}
	defer server.Close()
	p.track(server)
	defer p.untrack(server)

	done := make(chan struct{}, 2)
	go p.pump(client, server, done) // requests
	go p.pump(server, client, done) // responses
	<-done
}

// pump copies src to dst chunk by chunk, randomly killing the connection.
func (p *flakyProxy) pump(src, dst net.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if rate := p.killRate.Load(); rate > 0 && rand.Int64N(1000) < rate {
				// Abrupt close mid-stream: the client may have received a
				// partial response.
				src.Close()
				dst.Close()
				return
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				dst.Close()
			}
			return
		}
	}
}

// TestStress_FlakyNetwork runs the workload through a proxy that randomly
// kills connections. Operations may fail — but a returned value must always
// belong to the requested key, and the client must recover on its own.
func TestStress_FlakyNetwork(t *testing.T) {
	proxy := newFlakyProxy(t, stressMemcacheAddr)
	proxy.SetKillRatePerMille(20) // 2% of forwarded chunks kill the connection

	client := memcache.NewClient(memcache.StaticServers(proxy.Addr()), memcache.Config{
		MaxSize:        4,
		Timeout:        500 * time.Millisecond,
		ConnectTimeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	const keySpace = 100
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:flaky:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		switch rng.IntN(3) {
		case 0:
			if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
				stats.errors.Add(1)
			}
		case 1:
			item, err := client.Get(ctx, key)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if item.Found {
				checkValue(t, key, item.Value)
			}
		case 2:
			keys := make([]string, 1+rng.IntN(10))
			for i := range keys {
				keys[i] = fmt.Sprintf("stress:flaky:%d", rng.IntN(keySpace))
			}
			items, err := memcache.NewBatchCommands(client).MultiGet(ctx, keys)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			for i, item := range items {
				if item.Found {
					checkValue(t, keys[i], item.Value)
				}
			}
		}
	})

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(100), "the workload must actually run")
	assert.Positive(t, stats.errors.Load(), "the proxy must actually inject failures")

	// The client must fully recover once the network is stable again.
	proxy.SetKillRatePerMille(0)
	recovered := assert.Eventually(t, func() bool {
		key := "stress:flaky:recovery"
		if err := client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|done")}); err != nil {
			return false
		}
		item, err := client.Get(ctx, key)
		return err == nil && item.Found
	}, 5*time.Second, 100*time.Millisecond, "client must recover after the network stabilizes")
	if recovered {
		t.Log("client recovered after failure injection stopped")
	}
}

// =============================================================================
// Latency injection via an embedded toxiproxy
// =============================================================================

// newToxiproxy starts an in-process toxiproxy forwarding to backend.
// No toxiproxy daemon or HTTP API is involved: the proxy runs inside the
// test process and toxics are managed through its Go API.
func newToxiproxy(t *testing.T, backend string) *toxiproxy.Proxy {
	t.Helper()
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(nil), zerolog.Nop())
	proxy := toxiproxy.NewProxy(server, "stress-"+t.Name(), "127.0.0.1:0", backend)
	require.NoError(t, proxy.Start())
	t.Cleanup(proxy.Stop)
	return proxy
}

// setLatency installs or replaces the latency toxic on the response stream.
// Safe to call from non-test goroutines (it never calls FailNow).
//
// It removes any existing toxic and adds a fresh one rather than calling
// UpdateToxicJson: that method JSON-decodes the new latency/jitter values in
// place into the live toxic struct that the running pipe goroutine reads in
// LatencyToxic.delay(), a data race. RemoveToxic interrupts and joins the
// running stub before returning, and AddToxicJson decodes into a newly
// allocated toxic no goroutine is reading yet — so the decode write and the
// delay() read never touch the same memory.
func setLatency(t *testing.T, proxy *toxiproxy.Proxy, latency, jitter time.Duration) {
	t.Helper()
	spec := fmt.Sprintf(
		`{"name":"latency","type":"latency","stream":"downstream","toxicity":1,"attributes":{"latency":%d,"jitter":%d}}`,
		latency.Milliseconds(), jitter.Milliseconds())

	if proxy.Toxics.GetToxic("latency") != nil {
		assert.NoError(t, proxy.Toxics.RemoveToxic(context.Background(), "latency"))
	}
	_, err := proxy.Toxics.AddToxicJson(strings.NewReader(spec))
	assert.NoError(t, err)
}

// TestStress_SlowNetwork runs the workload over a connection with significant
// latency and jitter, below the client timeout. High RTT changes how responses
// split across reads and how deeply requests pipeline; correctness must not
// depend on packet timing, and no operation may fail.
func TestStress_SlowNetwork(t *testing.T) {
	proxy := newToxiproxy(t, stressMemcacheAddr)
	setLatency(t, proxy, 20*time.Millisecond, 10*time.Millisecond)

	client := memcache.NewClient(memcache.StaticServers(proxy.Listen), memcache.Config{
		MaxSize: 8,
		Timeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	const keySpace = 100
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:slow:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		switch rng.IntN(3) {
		case 0:
			if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
				stats.errors.Add(1)
			}
		case 1:
			item, err := client.Get(ctx, key)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if item.Found {
				checkValue(t, key, item.Value)
			}
		case 2:
			keys := make([]string, 1+rng.IntN(20))
			for i := range keys {
				keys[i] = fmt.Sprintf("stress:slow:%d", rng.IntN(keySpace))
			}
			items, err := memcache.NewBatchCommands(client).MultiGet(ctx, keys)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			require.Len(t, items, len(keys))
			for i, item := range items {
				assert.Equal(t, keys[i], item.Key, "response %d must belong to key %d", i, i)
				if item.Found {
					checkValue(t, keys[i], item.Value)
				}
			}
		}
	})

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(100), "the workload must actually run")
	assert.Zero(t, stats.errors.Load(), "latency below the timeout must not cause errors")
}

// TestStress_LatencySpikes alternates between mild latency and spikes well
// above the client timeout. A timed-out request must never desynchronize the
// connection: its late response must not be delivered to the next caller.
// Errors during spikes are expected; wrong data never, and the client must
// recover on its own once latency subsides.
func TestStress_LatencySpikes(t *testing.T) {
	proxy := newToxiproxy(t, stressMemcacheAddr)

	const calm = 2 * time.Millisecond
	const timeout = 150 * time.Millisecond
	setLatency(t, proxy, calm, time.Millisecond)

	client := memcache.NewClient(memcache.StaticServers(proxy.Listen), memcache.Config{
		MaxSize:        4,
		Timeout:        timeout,
		ConnectTimeout: time.Second,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	// Controller: toggle between calm latency and spikes above the timeout.
	stop := make(chan struct{})
	var controller sync.WaitGroup
	controller.Add(1)
	go func() {
		defer controller.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		spiking := false
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				spiking = !spiking
				if spiking {
					setLatency(t, proxy, 3*timeout, 0)
				} else {
					setLatency(t, proxy, calm, time.Millisecond)
				}
			}
		}
	}()

	const keySpace = 100
	var stats stressStats

	runWorkers(t, stressWorkers(), stressDuration(), func(t *testing.T, workerID int, rng *rand.Rand) {
		key := fmt.Sprintf("stress:spike:%d", rng.IntN(keySpace))
		stats.ops.Add(1)

		if rng.IntN(2) == 0 {
			if err := client.Set(ctx, memcache.Item{Key: key, Value: stressValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}); err != nil {
				stats.errors.Add(1)
			}
		} else {
			item, err := client.Get(ctx, key)
			if err != nil {
				stats.errors.Add(1)
				return
			}
			if item.Found {
				checkValue(t, key, item.Value)
			}
		}
	})

	close(stop)
	controller.Wait()

	stats.report(t)
	require.Greater(t, stats.ops.Load(), int64(100), "the workload must actually run")
	assert.Positive(t, stats.errors.Load(), "spikes above the timeout must actually cause failures")

	// The client must fully recover once latency is back to normal.
	setLatency(t, proxy, calm, 0)
	recovered := assert.Eventually(t, func() bool {
		key := "stress:spike:recovery"
		if err := client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|done")}); err != nil {
			return false
		}
		item, err := client.Get(ctx, key)
		return err == nil && item.Found
	}, 5*time.Second, 100*time.Millisecond, "client must recover after latency subsides")
	if recovered {
		t.Log("client recovered after latency spikes stopped")
	}
}

// TestStress_ServerOutage verifies behavior when the server becomes
// unreachable mid-workload and comes back: errors during the outage,
// full recovery after, and never wrong data.
func TestStress_ServerOutage(t *testing.T) {
	proxy := newFlakyProxy(t, stressMemcacheAddr)

	client := memcache.NewClient(memcache.StaticServers(proxy.Addr()), memcache.Config{
		MaxSize:        4,
		Timeout:        300 * time.Millisecond,
		ConnectTimeout: 300 * time.Millisecond,
	})
	t.Cleanup(client.Close)
	ctx := context.Background()

	key := "stress:outage:key"
	require.NoError(t, client.Set(ctx, memcache.Item{Key: key, Value: []byte(key + "|v1")}))

	// Outage: all connections die, new ones are refused.
	proxy.Stop()

	_, err := client.Get(ctx, key)
	require.Error(t, err, "operations during an outage must fail")

	// Recovery through a fresh proxy on a new address is not possible (the
	// client holds the address), so verify it against the real server.
	direct := memcache.NewClient(memcache.StaticServers(stressMemcacheAddr), memcache.Config{MaxSize: 2, Timeout: time.Second})
	t.Cleanup(direct.Close)

	item, err := direct.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, item.Found)
	assert.Equal(t, key+"|v1", string(item.Value), "data must be intact after the outage")
}
