package workload

import "math/rand/v2"

// Op is a workload operation kind. The generator maps each Op to one or more
// memcache client calls.
type Op uint8

const (
	OpGet Op = iota
	OpSet
	OpAdd
	OpDelete
	OpIncr
	OpMetaGetTTL // low-level meta get returning value + TTL
	OpBatchGet
	OpBatchSet
	numOps
)

var opNames = [numOps]string{
	OpGet:        "get",
	OpSet:        "set",
	OpAdd:        "add",
	OpDelete:     "delete",
	OpIncr:       "incr",
	OpMetaGetTTL: "metaget",
	OpBatchGet:   "batchget",
	OpBatchSet:   "batchset",
}

func (o Op) String() string {
	if o < numOps {
		return opNames[o]
	}
	return "op?"
}

// NumOps is the number of distinct operation kinds, for sizing per-op arrays.
const NumOps = int(numOps)

// weights is the cumulative op distribution (out of 100). Read-heavy with a mix
// of writes, deletes, arithmetic, a low-level meta path, and pipelined batches
// so the server pool and batching paths are all exercised.
var cumulative = buildCumulative([numOps]int{
	OpGet:        34,
	OpSet:        24,
	OpAdd:        6,
	OpDelete:     8,
	OpIncr:       8,
	OpMetaGetTTL: 6,
	OpBatchGet:   8,
	OpBatchSet:   6,
})

func buildCumulative(w [numOps]int) [numOps]int {
	var c [numOps]int
	sum := 0
	for i := range w {
		sum += w[i]
		c[i] = sum
	}
	if sum != 100 {
		panic("workload: op weights must sum to 100")
	}
	return c
}

// SelectOp picks an operation following the weighted distribution.
func SelectOp(rng *rand.Rand) Op {
	r := rng.IntN(100)
	for i := range cumulative {
		if r < cumulative[i] {
			return Op(i)
		}
	}
	return OpGet
}
