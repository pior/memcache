// Package recorder is the flight recorder: a small fixed-size ring of the most
// recent operations per worker, always on and cheap. When a desync or transport
// error occurs, the ring is dumped to capture the operations leading up to the
// anomaly — the context needed to debug it even when the full op-log is off.
//
// A Ring is owned by a single worker goroutine and is not safe for concurrent
// use; that keeps the per-op record path lock-free.
package recorder

import "github.com/pior/memcache/loadtest/internal/oplog"

// Ring is a per-worker circular buffer of recent records.
type Ring struct {
	buf    []oplog.Record
	pos    int  // next write position
	filled bool // whether the buffer has wrapped
}

// NewRing returns a ring holding the last size records.
func NewRing(size int) *Ring {
	if size <= 0 {
		size = 64
	}
	return &Ring{buf: make([]oplog.Record, size)}
}

// Add records one operation, overwriting the oldest when full.
func (r *Ring) Add(rec oplog.Record) {
	r.buf[r.pos] = rec
	r.pos++
	if r.pos == len(r.buf) {
		r.pos = 0
		r.filled = true
	}
}

// Dump returns the buffered records in chronological order (oldest first).
func (r *Ring) Dump() []oplog.Record {
	if !r.filled {
		out := make([]oplog.Record, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}
	out := make([]oplog.Record, 0, len(r.buf))
	out = append(out, r.buf[r.pos:]...) // older half
	out = append(out, r.buf[:r.pos]...) // newer half
	return out
}
