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
	"github.com/pior/memcache/loadtest/internal/oplog"
	"github.com/pior/memcache/loadtest/internal/profile"
	"github.com/pior/memcache/loadtest/internal/recorder"
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

	// OpLog, if non-nil, receives every operation as a compact record (the
	// opt-in full per-op log).
	OpLog *oplog.Writer
	// FlightRing sizes the per-worker flight recorder; 0 disables it.
	FlightRing int
}

// DesyncInfo describes an invariant violation and the operations leading to it.
type DesyncInfo struct {
	Worker int
	KeyID  int
	Value  []byte
	Recent []oplog.Record // flight-recorder dump (nil if disabled)
}

// DesyncFunc is called on every desync with the offending key/value and the
// flight-recorder dump.
type DesyncFunc func(DesyncInfo)

// Generator runs the workload against a client into a Metrics sink.
type Generator struct {
	client   *memcache.Client
	batch    *memcache.BatchCommands
	m        *metrics.Metrics
	cfg      Config
	onDesync DesyncFunc
	start    time.Time
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
	g.start = time.Now()
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

	var ring *recorder.Ring
	if g.cfg.FlightRing > 0 {
		ring = recorder.NewRing(g.cfg.FlightRing)
	}
	logging := ring != nil || g.cfg.OpLog != nil

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
		outcome, keyID, badValue := g.execOp(ctx, op, rng)
		lat := time.Since(start)
		// Don't record the op that lost a cancellation race at shutdown.
		if ctx.Err() != nil && outcome != metrics.OutcomeDesync {
			return
		}
		g.m.Record(op, lat, outcome)

		if logging {
			rec := oplog.Record{
				TimeNanos:     start.Sub(g.start).Nanoseconds(),
				Worker:        uint16(id),
				Op:            uint8(op),
				Status:        uint8(outcome),
				KeyID:         uint32(keyID),
				LatencyMicros: uint32(lat.Microseconds()),
			}
			if ring != nil {
				ring.Add(rec)
			}
			if g.cfg.OpLog != nil {
				_ = g.cfg.OpLog.Write(rec)
			}
		}

		if outcome == metrics.OutcomeDesync && g.onDesync != nil {
			info := DesyncInfo{Worker: id, KeyID: keyID, Value: badValue}
			if ring != nil {
				info.Recent = ring.Dump()
			}
			g.onDesync(info)
		}
	}
}

// execOp runs one operation and returns its outcome, a representative key id
// (for logging), and the offending value when the outcome is a desync.
func (g *Generator) execOp(ctx context.Context, op workload.Op, rng *rand.Rand) (metrics.Outcome, int, []byte) {
	switch op {
	case workload.OpGet:
		return g.doGet(ctx, rng.IntN(g.cfg.Keyspace))
	case workload.OpSet:
		keyID := rng.IntN(g.cfg.Keyspace)
		return classify(g.client.Set(ctx, g.item(keyID, rng))), keyID, nil
	case workload.OpAdd:
		keyID := rng.IntN(g.cfg.Keyspace)
		err := g.client.Add(ctx, g.item(keyID, rng))
		if errors.Is(err, memcache.ErrNotStored) {
			return metrics.OutcomeOK, keyID, nil // key already present — expected
		}
		return classify(err), keyID, nil
	case workload.OpDelete:
		keyID := rng.IntN(g.cfg.Keyspace)
		return classify(g.client.Delete(ctx, workload.Key(keyID))), keyID, nil
	case workload.OpIncr:
		id := rng.IntN(counterKeyspace)
		_, err := g.client.Increment(ctx, workload.KeyPrefix+"ctr:"+itoa(id), 1, memcache.NoTTL)
		return classify(err), id, nil
	case workload.OpMetaGetTTL:
		return g.doMetaGet(ctx, rng.IntN(g.cfg.Keyspace))
	case workload.OpBatchGet:
		return g.doBatchGet(ctx, rng)
	case workload.OpBatchSet:
		return g.doBatchSet(ctx, rng)
	}
	return metrics.OutcomeOK, 0, nil
}

func (g *Generator) doGet(ctx context.Context, keyID int) (metrics.Outcome, int, []byte) {
	item, err := g.client.Get(ctx, workload.Key(keyID))
	if err != nil {
		return classify(err), keyID, nil
	}
	if !item.Found {
		return metrics.OutcomeMiss, keyID, nil
	}
	if cerr := workload.CheckValue(keyID, item.Value); cerr != nil {
		return metrics.OutcomeDesync, keyID, item.Value
	}
	return metrics.OutcomeHit, keyID, nil
}

func (g *Generator) doMetaGet(ctx context.Context, keyID int) (metrics.Outcome, int, []byte) {
	req := meta.NewRequest(meta.CmdGet, workload.Key(keyID), nil).AddReturnValue().AddReturnTTL()
	resp, err := g.client.Execute(ctx, req)
	if err != nil {
		return classify(err), keyID, nil
	}
	if resp.Status != meta.StatusVA {
		return metrics.OutcomeMiss, keyID, nil
	}
	if cerr := workload.CheckValue(keyID, resp.Data); cerr != nil {
		return metrics.OutcomeDesync, keyID, resp.Data
	}
	return metrics.OutcomeHit, keyID, nil
}

func (g *Generator) doBatchGet(ctx context.Context, rng *rand.Rand) (metrics.Outcome, int, []byte) {
	n := 1 + rng.IntN(maxBatch)
	keyIDs := make([]int, n)
	keys := make([]string, n)
	for i := range keys {
		keyIDs[i] = rng.IntN(g.cfg.Keyspace)
		keys[i] = workload.Key(keyIDs[i])
	}
	items, err := g.batch.MultiGet(ctx, keys)
	if err != nil {
		return classify(err), keyIDs[0], nil
	}
	anyHit := false
	for i, item := range items {
		if item.Found {
			if cerr := workload.CheckValue(keyIDs[i], item.Value); cerr != nil {
				return metrics.OutcomeDesync, keyIDs[i], item.Value
			}
			anyHit = true
		}
	}
	if anyHit {
		return metrics.OutcomeHit, keyIDs[0], nil
	}
	return metrics.OutcomeMiss, keyIDs[0], nil
}

func (g *Generator) doBatchSet(ctx context.Context, rng *rand.Rand) (metrics.Outcome, int, []byte) {
	n := 1 + rng.IntN(maxBatch)
	first := rng.IntN(g.cfg.Keyspace)
	items := make([]memcache.Item, n)
	items[0] = g.item(first, rng)
	for i := 1; i < n; i++ {
		items[i] = g.item(rng.IntN(g.cfg.Keyspace), rng)
	}
	return classify(g.batch.MultiSet(ctx, items)), first, nil
}

func (g *Generator) item(keyID int, rng *rand.Rand) memcache.Item {
	return memcache.Item{
		Key:   workload.Key(keyID),
		Value: workload.Value(keyID, rng),
		TTL:   memcache.ExpiresIn(time.Minute),
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
