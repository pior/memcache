package recorder

import (
	"testing"

	"github.com/pior/memcache/loadtest/internal/oplog"
)

func rec(id uint32) oplog.Record { return oplog.Record{KeyID: id} }

func ids(recs []oplog.Record) []uint32 {
	out := make([]uint32, len(recs))
	for i, r := range recs {
		out[i] = r.KeyID
	}
	return out
}

func eq(a []uint32, b ...uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRingPartial(t *testing.T) {
	r := NewRing(4)
	r.Add(rec(1))
	r.Add(rec(2))
	if got := ids(r.Dump()); !eq(got, 1, 2) {
		t.Errorf("partial dump = %v, want [1 2]", got)
	}
}

func TestRingWrap(t *testing.T) {
	r := NewRing(3)
	for i := uint32(1); i <= 5; i++ {
		r.Add(rec(i))
	}
	// last 3 in chronological order
	if got := ids(r.Dump()); !eq(got, 3, 4, 5) {
		t.Errorf("wrapped dump = %v, want [3 4 5]", got)
	}
}

func TestRingExactFill(t *testing.T) {
	r := NewRing(3)
	r.Add(rec(1))
	r.Add(rec(2))
	r.Add(rec(3))
	if got := ids(r.Dump()); !eq(got, 1, 2, 3) {
		t.Errorf("exact-fill dump = %v, want [1 2 3]", got)
	}
}
