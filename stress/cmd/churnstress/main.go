// churnstress drives a large local memcached fleet (dozens of docker
// containers) to push three robustness subsystems the fixed-set chaos runs
// never exercised under load:
//
//   - endpoint churn -> per-address pool reaping (reapDepartedPools, the 2-pass
//     mark-and-sweep grace) and rendezvous selector stability;
//   - the per-server circuit breaker at its limits: flap faster than the open
//     timeout, and a thundering herd of half-open probes on mass recovery;
//   - the reaper pass under dozens of simultaneously-frozen pools
//     (goroutine spike/return, reaping not stalled).
//
// It assumes the fleet containers (namePrefix + index, publishing basePort+index)
// are already running; a deploy script starts them. Chaos is driven in-process
// via `docker pause/unpause/kill/start` so fault events correlate tightly with
// the in-process samples (goroutines, pools, breaker states). Every stored value
// embeds its key; every read verifies it — a desync (another key's value) is the
// headline failure and must stay 0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/meta"
)

// ---- flags ----

var (
	fleet        = flag.Int("fleet", 48, "number of memcached containers")
	basePort     = flag.Int("base-port", 11300, "host port of container index 0")
	namePrefix   = flag.String("name-prefix", "cs", "container name prefix (cs00, cs01, ...)")
	workers      = flag.Int("workers", 64, "concurrent load workers")
	keyspace     = flag.Int("keyspace", 200000, "distinct keys")
	opTimeout    = flag.Duration("timeout", time.Second, "client Config.Timeout (per-op server-side cap)")
	callerBudget = flag.Duration("op-budget", 0, "per-op caller context budget (0 = 4×timeout). Must be looser than -timeout so a hung server's I/O timeout is attributed to the server (not the caller's deadline) and trips the breaker; a budget == timeout is excluded by the #118 rule and never sheds.")
	breakerTrip  = flag.Uint("breaker-trip", 5, "breaker trip minimum: failing ops in the trip window before it can open (BreakerConfig.TripMinRequests)")
	breakerOpen  = flag.Duration("breaker-open", 2*time.Second, "breaker open interval before half-open probe")
	reaperEvery  = flag.Duration("reaper-interval", 2*time.Second, "client ReaperInterval")
	idleCheck    = flag.Duration("idle-check", 5*time.Second, "IdleConnCheckThreshold")
	maxConns     = flag.Int("conns", 16, "max conns per server")
	sampleEvery  = flag.Duration("sample-interval", time.Second, "metrics sample cadence")
	out          = flag.String("out", "churnstress.jsonl", "metrics JSONL output")
	quick        = flag.Bool("quick", false, "short schedule to validate the harness")

	closeChurn = flag.Int("close-churn", 0, "S3: number of goroutines that repeatedly create short-lived clients, run ops, and Close them under load (best with -race)")
	outageHold = flag.Duration("outage-hold", 90*time.Second, "S2: how long the total-outage phase keeps every server down")

	mode          = flag.String("mode", "chaos", "chaos (fault schedule over a static fleet) | discovery (rolling service-discovery churn: deploy fresh / retire old)")
	duration      = flag.Duration("duration", 5*time.Minute, "discovery mode: total run duration")
	redeployEvery = flag.Duration("redeploy-interval", 3*time.Second, "discovery mode: cadence of a rolling redeploy")
	redeployBatch = flag.Int("redeploy-batch", 2, "discovery mode: servers replaced (new identity) per redeploy")
)

// discovery-mode gauges (0 in chaos mode).
var (
	distinctDeployed atomic.Int64 // cumulative distinct addresses ever deployed
	liveCount        atomic.Int64 // currently-deployed (in the Servers list)
)

// countFDs reports the process open file-descriptor count (Linux), a leak signal
// when reaping is off: pools/conns to retired servers keep their sockets.
func countFDs() int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(ents) - 1
}

// ---- mutable server set (the churn substrate) ----

type mutableServers struct {
	mu     sync.RWMutex
	active []memcache.Server
}

func (m *mutableServers) List() []memcache.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]memcache.Server, len(m.active))
	copy(out, m.active)
	return out
}

func (m *mutableServers) set(addrs []string) {
	s := make([]memcache.Server, len(addrs))
	for i, a := range addrs {
		s[i] = memcache.Server{Address: a}
	}
	m.mu.Lock()
	m.active = s
	m.mu.Unlock()
}

// ---- fleet identity ----

type node struct {
	addr string // 127.0.0.1:port
	name string // docker container name
}

func buildFleet(n int) []node {
	nodes := make([]node, n)
	for i := range n {
		nodes[i] = node{
			addr: fmt.Sprintf("127.0.0.1:%d", *basePort+i),
			name: fmt.Sprintf("%s%02d", *namePrefix, i),
		}
	}
	return nodes
}

// ---- breaker state tracking (OnStateChange hook) ----

type breakerTracker struct {
	mu          sync.Mutex
	state       map[string]string
	transitions atomic.Int64
}

func newBreakerTracker() *breakerTracker {
	return &breakerTracker{state: map[string]string{}}
}

func (b *breakerTracker) onChange(name, from, to string) {
	b.transitions.Add(1)
	b.mu.Lock()
	b.state[name] = to
	b.mu.Unlock()
}

// ---- value with embedded key (desync invariant) ----

func makeValue(key string, rng *rand.Rand) []byte {
	pad := 8 + rng.IntN(120)
	v := make([]byte, 0, len(key)+1+pad)
	v = append(v, key...)
	v = append(v, '|')
	for range pad {
		v = append(v, byte('a'+rng.IntN(26)))
	}
	return v
}

// checkValue distinguishes a miss (empty) from a desync (value belongs to
// another key). Returns true on desync.
func desynced(key string, value []byte) bool {
	if len(value) == 0 {
		return false
	}
	for i := range value {
		if value[i] == '|' {
			return string(value[:i]) != key
		}
	}
	return true // value present but no delimiter => corrupt/foreign
}

// ---- counters ----

type counters struct {
	ops, errs, timeouts, misses, desyncs atomic.Int64
}

// ---- docker chaos ----

func dockerDo(action string, names ...string) {
	args := append([]string{action}, names...)
	if err := exec.Command("docker", args...).Run(); err != nil {
		log.Printf("docker %s %v: %v", action, names, err)
	}
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	nodes := buildFleet(*fleet)
	allAddrs := make([]string, len(nodes))
	for i, n := range nodes {
		allAddrs[i] = n.addr
	}

	ms := &mutableServers{}
	if *mode != "discovery" {
		ms.set(allAddrs) // discovery manages its own set via runDiscovery
	}

	bt := newBreakerTracker()
	cfg := memcache.Config{
		Timeout:                *opTimeout,
		ConnectTimeout:         *opTimeout,
		MaxSize:                int32(*maxConns),
		ReaperInterval:         *reaperEvery,
		IdleConnCheckThreshold: *idleCheck,
		Breaker: memcache.BreakerConfig{
			Enabled:             true,
			HalfOpenMaxRequests: 1,
			TripMinRequests:     uint32(*breakerTrip),
			OpenDuration:        *breakerOpen,
			OnStateChange:       bt.onChange,
		},
	}
	client := memcache.NewClient(ms, cfg)
	defer client.Close()

	budget := *callerBudget
	if budget <= 0 {
		budget = 4 * *opTimeout
	}

	var cnt counters
	start := time.Now()

	outF, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer outF.Close()
	enc := json.NewEncoder(outF)

	runCtx, stopAll := context.WithCancel(context.Background())

	// ---- sampler ----
	var samplerWg sync.WaitGroup
	samplerWg.Go(func() {
		tick := time.NewTicker(*sampleEvery)
		defer tick.Stop()
		var phase atomic.Value
		_ = phase
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				pm := client.PoolMetrics()
				byState := map[string]int{}
				openAddrs := []string{}
				for _, p := range pm {
					st := p.Breaker.State
					if st == "" {
						st = "none"
					}
					byState[st]++
					if p.Breaker.State == "open" || p.Breaker.State == "half-open" {
						openAddrs = append(openAddrs, p.Addr)
					}
				}
				rec := map[string]any{
					"t":                   time.Since(start).Seconds(),
					"phase":               currentPhase.Load(),
					"active":              len(ms.List()),
					"pools":               len(pm),
					"goroutines":          runtime.NumGoroutine(),
					"heap_mb":             float64(mem.HeapAlloc) / 1e6,
					"heap_objects":        mem.HeapObjects,
					"ops":                 cnt.ops.Load(),
					"errors":              cnt.errs.Load(),
					"timeouts":            cnt.timeouts.Load(),
					"misses":              cnt.misses.Load(),
					"desyncs":             cnt.desyncs.Load(),
					"deployed_total":      distinctDeployed.Load(),
					"live":                liveCount.Load(),
					"open_fds":            countFDs(),
					"breaker_by_state":    byState,
					"breaker_open":        openAddrs,
					"breaker_transitions": bt.transitions.Load(),
				}
				_ = enc.Encode(rec)
			}
		}
	})

	// ---- load workers ----
	var workerWg sync.WaitGroup
	for w := range *workers {
		workerWg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w)+1, 0x9e3779b9))
			for runCtx.Err() == nil {
				key := fmt.Sprintf("cs:%d", rng.IntN(*keyspace))
				ctx, cancel := context.WithTimeout(runCtx, budget)
				if rng.IntN(2) == 0 {
					err := client.Set(ctx, memcache.Item{Key: key, Value: makeValue(key, rng), TTL: memcache.ExpiresIn(2 * time.Minute)})
					classify(&cnt, err)
				} else {
					req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()
					err := client.Execute(ctx, req, func(resp *meta.Response) error {
						if len(resp.Data) == 0 {
							cnt.misses.Add(1)
							return nil
						}
						if desynced(key, resp.Data) {
							cnt.desyncs.Add(1)
							log.Printf("DESYNC key=%q got=%q", key, truncate(resp.Data, 40))
						}
						return nil
					})
					classify(&cnt, err)
				}
				cancel()
			}
		})
	}

	// ---- S3: close-churn — create/op/Close short-lived clients under load ----
	for c := range *closeChurn {
		workerWg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(c)+7, 0xC0FFEE))
			for runCtx.Err() == nil {
				cc := memcache.NewClient(ms, cfg) // shares the concurrent-safe server set
				for i := 0; i < 20 && runCtx.Err() == nil; i++ {
					key := fmt.Sprintf("cs:%d", rng.IntN(*keyspace))
					ctx, cancel := context.WithTimeout(runCtx, budget)
					classify(&cnt, cc.Set(ctx, memcache.Item{Key: key, Value: makeValue(key, rng), TTL: memcache.ExpiresIn(time.Minute)}))
					cancel()
				}
				cc.Close() // Close under load, sometimes mid-fault
				sleepCtx(runCtx, 100*time.Millisecond)
			}
		})
	}

	// ---- schedule ----
	switch *mode {
	case "discovery":
		runDiscovery(runCtx, ms)
	default:
		runSchedule(runCtx, nodes, ms, allAddrs, *quick)
	}

	stopAll()
	workerWg.Wait()
	samplerWg.Wait()

	if *mode != "discovery" {
		// restore any lingering chaos on the static fleet
		names := make([]string, len(nodes))
		for i, n := range nodes {
			names[i] = n.name
		}
		dockerDo("unpause", names...)
		dockerDo("start", names...)
	}

	printDiscovery := *mode == "discovery"
	printSummary(client, ms, allAddrs, bt, &cnt, time.Since(start), printDiscovery)
}

func classify(c *counters, err error) {
	c.ops.Add(1)
	if err == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.timeouts.Add(1)
	}
	c.errs.Add(1)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

var currentPhase atomic.Value // string

func setPhase(p string) {
	currentPhase.Store(p)
	log.Printf("=== PHASE %s ===", p)
}

func printSummary(client *memcache.Client, ms *mutableServers, allAddrs []string, bt *breakerTracker, cnt *counters, dur time.Duration, discovery bool) {
	pm := client.PoolMetrics()
	openCount := 0
	for _, p := range pm {
		if p.Breaker.State == "open" || p.Breaker.State == "half-open" {
			openCount++
		}
	}
	fmt.Println("\n================ CHURNSTRESS SUMMARY ================")
	fmt.Printf("mode=%s duration=%s ops=%d errors=%d timeouts=%d misses=%d\n",
		*mode, dur.Round(time.Second), cnt.ops.Load(), cnt.errs.Load(), cnt.timeouts.Load(), cnt.misses.Load())
	fmt.Printf("DESYNCS=%d  (must be 0)\n", cnt.desyncs.Load())
	fmt.Printf("breaker transitions=%d\n", bt.transitions.Load())

	pass := true
	check := func(name string, ok bool, detail string) {
		status := "PASS"
		if !ok {
			status, pass = "FAIL", false
		}
		fmt.Printf("  [%s] %s %s\n", status, name, detail)
	}

	if discovery {
		live := liveCount.Load()
		deployed := distinctDeployed.Load()
		fmt.Printf("final: live_set=%d pools=%d deployed_total=%d breakers_open=%d goroutines=%d open_fds=%d\n",
			live, len(pm), deployed, openCount, runtime.NumGoroutine(), countFDs())
		fmt.Printf("LEAK RATIO pools/live = %.1f  (reaping on: ~1; reaping off: grows toward deployed_total/live)\n",
			float64(len(pm))/float64(max64(live, 1)))
		fmt.Println("ASSERTIONS:")
		check("desyncs==0", cnt.desyncs.Load() == 0, fmt.Sprintf("(%d)", cnt.desyncs.Load()))
		// Not a pass/fail — this is the D2 measurement (compare across reaping on/off).
		fmt.Printf("  [INFO] pools=%d vs live=%d vs deployed_total=%d — pools tracking live => reaping works; pools tracking deployed_total => LEAK (D2)\n",
			len(pm), live, deployed)
	} else {
		fmt.Printf("final: active_set=%d pools=%d breakers_open=%d goroutines=%d\n",
			len(ms.List()), len(pm), openCount, runtime.NumGoroutine())
		fmt.Println("ASSERTIONS:")
		check("desyncs==0", cnt.desyncs.Load() == 0, fmt.Sprintf("(%d)", cnt.desyncs.Load()))
		check("final pools == active set (reaping converged)", len(pm) == len(allAddrs),
			fmt.Sprintf("(pools=%d active=%d)", len(pm), len(allAddrs)))
		check("no breaker wedged open after final heal", openCount == 0,
			fmt.Sprintf("(open=%d)", openCount))
	}

	if pass {
		fmt.Println("RESULT: PASS")
	} else {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
