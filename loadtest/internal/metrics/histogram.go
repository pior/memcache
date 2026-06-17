package metrics

import (
	"fmt"
	"math/bits"
	"strings"
	"sync/atomic"
	"time"
)

// Latency histogram with HDR-style log-linear buckets: exact for small values,
// then a fixed number of linear sub-buckets per power of two (~6% relative
// resolution). Recording is lock-free (atomic per-bucket adds); buckets shared
// across workers contend only on the same latency band, which is rare enough at
// op rates. Microsecond unit; values above the range clamp into the last bucket.

const (
	subBucketBits  = 4
	subBucketCount = 1 << subBucketBits // 16 linear sub-buckets per octave
	numBuckets     = 1024               // covers ~0..>1000s in microseconds
)

// indexFor maps a microsecond value to a bucket index.
func indexFor(v int64) int {
	if v < 0 {
		v = 0
	}
	if v < subBucketCount {
		return int(v) // exact for the smallest values
	}
	bucketIndex := bits.Len64(uint64(v)) - subBucketBits - 1
	sub := int(v >> uint(bucketIndex)) // in [subBucketCount, 2*subBucketCount)
	idx := (bucketIndex+1)*subBucketCount + (sub - subBucketCount)
	if idx >= numBuckets {
		return numBuckets - 1
	}
	return idx
}

// valueAt returns the lower-bound microsecond value represented by an index.
func valueAt(index int) int64 {
	if index < subBucketCount {
		return int64(index)
	}
	bucketIndex := index/subBucketCount - 1
	sub := index%subBucketCount + subBucketCount
	return int64(sub) << uint(bucketIndex)
}

// Histogram accumulates latency samples.
type Histogram struct {
	counts [numBuckets]atomic.Int64
	sum    atomic.Int64 // total microseconds, for the mean
}

// Record adds one latency sample.
func (h *Histogram) Record(d time.Duration) {
	us := d.Microseconds()
	h.counts[indexFor(us)].Add(1)
	h.sum.Add(us)
}

// Data returns a sparse, mergeable, JSON-friendly snapshot.
func (h *Histogram) Data() HistogramData {
	d := HistogramData{Buckets: make(map[int]int64)}
	for i := range h.counts {
		if c := h.counts[i].Load(); c > 0 {
			d.Buckets[i] = c
			d.Total += c
		}
	}
	d.SumMicros = h.sum.Load()
	return d
}

// HistogramData is a point-in-time histogram: bucket index -> count.
type HistogramData struct {
	Buckets   map[int]int64 `json:"buckets"`
	Total     int64         `json:"total"`
	SumMicros int64         `json:"sum_micros"`
}

// Merge folds o into d (used to combine per-VM histograms).
func (d *HistogramData) Merge(o HistogramData) {
	if d.Buckets == nil {
		d.Buckets = make(map[int]int64)
	}
	for idx, c := range o.Buckets {
		d.Buckets[idx] += c
	}
	d.Total += o.Total
	d.SumMicros += o.SumMicros
}

// Percentile returns the latency at the given percentile (0..100), using the
// bucket lower bound — a conservative estimate within ~6% of the true value.
func (d HistogramData) Percentile(p float64) time.Duration {
	if d.Total == 0 {
		return 0
	}
	target := int64(float64(d.Total) * p / 100)
	if target >= d.Total {
		target = d.Total - 1
	}
	// Walk buckets in index order until the cumulative count passes target.
	var cum int64
	for idx := range numBuckets {
		c, ok := d.Buckets[idx]
		if !ok {
			continue
		}
		cum += c
		if cum > target {
			return time.Duration(valueAt(idx)) * time.Microsecond
		}
	}
	return d.Max()
}

// Max returns the lower bound of the highest populated bucket.
func (d HistogramData) Max() time.Duration {
	maxIdx := -1
	for idx := range d.Buckets {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx < 0 {
		return 0
	}
	return time.Duration(valueAt(maxIdx)) * time.Microsecond
}

// Mean returns the arithmetic mean latency.
func (d HistogramData) Mean() time.Duration {
	if d.Total == 0 {
		return 0
	}
	return time.Duration(d.SumMicros/d.Total) * time.Microsecond
}

// distBands are the upper bounds (microseconds) of the latency bands used by
// DistributionText; the final band catches everything above the last bound.
var distBands = []int64{
	50, 100, 200, 500, // sub-millisecond
	1_000, 2_000, 5_000, // 1–5 ms
	10_000, 20_000, 50_000, // 10–50 ms
	100_000, 200_000, 500_000, // 100–500 ms
	1_000_000, // 1 s
}

// DistributionText renders the histogram as a human-readable latency
// distribution: one row per band with its count, percentage, cumulative
// percentage, and a proportional bar. Empty bands are omitted. It answers
// "where does the latency actually sit?" at a glance during a long run.
func (d HistogramData) DistributionText() string {
	if d.Total == 0 {
		return "  (no samples)\n"
	}

	counts := make([]int64, len(distBands)+1) // +1 overflow band
	for idx, c := range d.Buckets {
		v := valueAt(idx)
		band := len(distBands) // overflow by default
		for i, ub := range distBands {
			if v < ub {
				band = i
				break
			}
		}
		counts[band] += c
	}

	var maxCount int64
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}

	var b strings.Builder
	var cum int64
	for i, c := range counts {
		if c == 0 {
			continue
		}
		cum += c
		bars := 0
		if maxCount > 0 {
			bars = int(c * 40 / maxCount)
		}
		fmt.Fprintf(&b, "  %-9s %10d %6.2f%% (cum %6.2f%%) %s\n",
			distBandLabel(i), c,
			100*float64(c)/float64(d.Total),
			100*float64(cum)/float64(d.Total),
			strings.Repeat("#", bars))
	}
	return b.String()
}

// distBandLabel returns the "< upper" label for band i (the last band is "≥ 1s").
func distBandLabel(i int) string {
	if i >= len(distBands) {
		return "≥ " + durLabel(distBands[len(distBands)-1])
	}
	return "< " + durLabel(distBands[i])
}

// durLabel formats a microsecond bound compactly (e.g. 500µs, 2ms, 1s).
func durLabel(us int64) string {
	switch {
	case us < 1_000:
		return fmt.Sprintf("%dµs", us)
	case us < 1_000_000:
		return fmt.Sprintf("%dms", us/1_000)
	default:
		return fmt.Sprintf("%ds", us/1_000_000)
	}
}
