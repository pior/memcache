package workload

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

func newRNG() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestValueRoundTrip(t *testing.T) {
	rng := newRNG()
	for _, id := range []int{0, 1, 42, 1000, 999999} {
		v := Value(id, rng)

		if err := CheckValue(id, v); err != nil {
			t.Errorf("CheckValue(%d) on its own value failed: %v", id, err)
		}

		got, ok := ParseKeyID(v)
		if !ok {
			t.Errorf("ParseKeyID(%q) not ok", v)
		}
		if got != id {
			t.Errorf("ParseKeyID round-trip = %d, want %d", got, id)
		}
	}
}

func TestCheckValueRejectsForeignKey(t *testing.T) {
	rng := newRNG()
	v := Value(7, rng)

	err := CheckValue(8, v) // value for key 7 checked against key 8
	if err == nil {
		t.Fatal("CheckValue accepted a foreign key's value")
	}
	var de *DesyncError
	if !errors.As(err, &de) {
		t.Fatalf("error type = %T, want *DesyncError", err)
	}
	if !strings.Contains(de.Error(), "DESYNC") {
		t.Errorf("error message missing DESYNC marker: %v", de)
	}
}

func TestParseKeyID(t *testing.T) {
	cases := map[string]struct {
		id int
		ok bool
	}{
		"stress:lt:0|x":   {0, true},
		"stress:lt:123|":  {123, true},
		"stress:lt:999":   {999, true}, // a bare key with no value
		"other:5|x":       {0, false},
		"stress:lt:abc|x": {0, false},
		"":                {0, false},
	}
	for in, want := range cases {
		id, ok := ParseKeyID([]byte(in))
		if ok != want.ok || (ok && id != want.id) {
			t.Errorf("ParseKeyID(%q) = (%d,%v), want (%d,%v)", in, id, ok, want.id, want.ok)
		}
	}
}

func TestSelectOpDistribution(t *testing.T) {
	rng := newRNG()
	var counts [NumOps]int
	const n = 100000
	for range n {
		counts[SelectOp(rng)]++
	}
	// Every op must be selected at least sometimes; the lowest weight is 6%.
	for op := range NumOps {
		if counts[op] == 0 {
			t.Errorf("op %s never selected", Op(op))
		}
	}
}
