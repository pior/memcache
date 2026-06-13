// Package generator drives the workload: concurrent workers issuing memcache
// operations against a real client, checking the key-embedding invariant on
// every read and recording latency/outcomes. It supports saturation (closed
// loop) and fixed-rate (paced) intensities.
package generator

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	memcache "github.com/pior/memcache"
	"github.com/pior/memcache/loadtest/internal/metrics"
	"github.com/pior/memcache/loadtest/internal/profile"
	"github.com/pior/memcache/loadtest/internal/workload"
	"github.com/pior/memcache/meta"
)

// counterKeyspace is a separate namespace for arithmetic ops so increments
// never land on the string-valued keys (which would return CLIENT_ERROR).
const counterKeyspace = 256

// maxBatch bounds the size of pipelined batch operations.
const maxBatch = 50

// Config parametrizes a run.
type Config struct {
	Workers    int
	Keyspace   int
	Duration   time.Duration
	Intensity  profile.Intensity
	TargetRate int // total ops/sec across workers for FixedRate; 0 = unlimited
}

// DesyncFunc is called with the offending key and value the first time (and
// every time) a read violates the invariant, for flight-recorder dumps.
type DesyncFunc func(keyID int, value []byte)

// Generator runs the workload against a client into a Metrics sink.
type Generator struct {
	client   *memcache.Client
	batch    *memcache.BatchCommands
	m        *metrics.Metrics
	cfg      Config
	onDesync DesyncFunc
}

// New creates a Generator. onDesync may be nil.
func New(client *memcache.Client, m *metrics.Metrics, cfg Config, onDesync DesyncFunc) *Generator {
	return &Generator{
		client:   client,
		batch:    memcache.NewBatchCommands(client),
		m:        m,
		cfg:      cfg,
		onDesync: onDesync,
	}
}

// Run executes the workload until cfg.Duration elapses or ctx is cancelled.
func (g *Generator) Run(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	for id := range g.cfg.Workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			g.worker(ctx, id)
		}(id)
	}
	wg.Wait()
}

func (g *Generator) worker(ctx context.Context, id int) {
	rng := rand.New(rand.NewPCG(uint64(id), rand.Uint64()))

	var pace *time.Ticker
	if g.cfg.Intensity == profile.FixedRate && g.cfg.TargetRate > 0 {
		interval := time.Duration(float64(time.Second) * float64(g.cfg.Workers) / float64(g.cfg.TargetRate))
		pace = time.NewTicker(interval)
		defer pace.Stop()
	}

	for {
		if pace != nil {
			select {
			case <-ctx.Done():
				return
			case <-pace.C:
			}
		} else if ctx.Err() != nil {
			return
		}

		op := workload.SelectOp(rng)
		start := time.Now()
		outcome := g.execOp(ctx, op, rng)
		// Don't record the op that lost a cancellation race at shutdown.
		if ctx.Err() != nil && outcome != metrics.OutcomeDesync {
			return
		}
		g.m.Record(op, time.Since(start), outcome)
	}
}

func (g *Generator) execOp(ctx context.Context, op workload.Op, rng *rand.Rand) metrics.Outcome {
	switch op {
	case workload.OpGet:
		return g.doGet(ctx, rng.IntN(g.cfg.Keyspace))
	case workload.OpSet:
		return classify(g.client.Set(ctx, g.item(rng.IntN(g.cfg.Keyspace), rng)))
	case workload.OpAdd:
		err := g.client.Add(ctx, g.item(rng.IntN(g.cfg.Keyspace), rng))
		if errors.Is(err, memcache.ErrNotStored) {
			return metrics.OutcomeOK // key already present — expected
		}
		return classify(err)
	case workload.OpDelete:
		return classify(g.client.Delete(ctx, workload.Key(rng.IntN(g.cfg.Keyspace))))
	case workload.OpIncr:
		key := workload.KeyPrefix + "ctr:" + itoa(rng.IntN(counterKeyspace))
		_, err := g.client.Increment(ctx, key, 1, memcache.NoTTL)
		return classify(err)
	case workload.OpMetaGetTTL:
		return g.doMetaGet(ctx, rng.IntN(g.cfg.Keyspace))
	case workload.OpBatchGet:
		return g.doBatchGet(ctx, rng)
	case workload.OpBatchSet:
		return g.doBatchSet(ctx, rng)
	}
	return metrics.OutcomeOK
}

func (g *Generator) doGet(ctx context.Context, keyID int) metrics.Outcome {
	item, err := g.client.Get(ctx, workload.Key(keyID))
	if err != nil {
		return classify(err)
	}
	if !item.Found {
		return metrics.OutcomeMiss
	}
	if cerr := workload.CheckValue(keyID, item.Value); cerr != nil {
		g.reportDesync(keyID, item.Value)
		return metrics.OutcomeDesync
	}
	return metrics.OutcomeHit
}

func (g *Generator) doMetaGet(ctx context.Context, keyID int) metrics.Outcome {
	req := meta.NewRequest(meta.CmdGet, workload.Key(keyID), nil).AddReturnValue().AddReturnTTL()
	resp, err := g.client.Execute(ctx, req)
	if err != nil {
		return classify(err)
	}
	if resp.Status != meta.StatusVA {
		return metrics.OutcomeMiss
	}
	if cerr := workload.CheckValue(keyID, resp.Data); cerr != nil {
		g.reportDesync(keyID, resp.Data)
		return metrics.OutcomeDesync
	}
	return metrics.OutcomeHit
}

func (g *Generator) doBatchGet(ctx context.Context, rng *rand.Rand) metrics.Outcome {
	n := 1 + rng.IntN(maxBatch)
	keyIDs := make([]int, n)
	keys := make([]string, n)
	for i := range keys {
		keyIDs[i] = rng.IntN(g.cfg.Keyspace)
		keys[i] = workload.Key(keyIDs[i])
	}
	items, err := g.batch.MultiGet(ctx, keys)
	if err != nil {
		return classify(err)
	}
	anyHit := false
	for i, item := range items {
		if item.Found {
			if cerr := workload.CheckValue(keyIDs[i], item.Value); cerr != nil {
				g.reportDesync(keyIDs[i], item.Value)
				return metrics.OutcomeDesync
			}
			anyHit = true
		}
	}
	if anyHit {
		return metrics.OutcomeHit
	}
	return metrics.OutcomeMiss
}

func (g *Generator) doBatchSet(ctx context.Context, rng *rand.Rand) metrics.Outcome {
	n := 1 + rng.IntN(maxBatch)
	items := make([]memcache.Item, n)
	for i := range items {
		items[i] = g.item(rng.IntN(g.cfg.Keyspace), rng)
	}
	return classify(g.batch.MultiSet(ctx, items))
}

func (g *Generator) item(keyID int, rng *rand.Rand) memcache.Item {
	return memcache.Item{
		Key:   workload.Key(keyID),
		Value: workload.Value(keyID, rng),
		TTL:   memcache.ExpiresIn(time.Minute),
	}
}

func (g *Generator) reportDesync(keyID int, value []byte) {
	if g.onDesync != nil {
		g.onDesync(keyID, value)
	}
}

// classify maps an error to an outcome (nil -> OK miss/non-hit).
func classify(err error) metrics.Outcome {
	switch {
	case err == nil:
		return metrics.OutcomeOK
	case errors.Is(err, context.DeadlineExceeded):
		return metrics.OutcomeTimeout
	default:
		return metrics.OutcomeError
	}
}

func itoa(n int) string {
	// small, alloc-light integer formatting for counter keys
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
