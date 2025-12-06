// coarsetime provides a coarse time implementation to reduce the overhead of frequent time.Now() calls.
// It updates the current time at a fixed interval (50ms) in a separate goroutine.
//

package coarsetime

import (
	"sync/atomic"
	"time"
)

const tick = 50 * time.Millisecond

var now atomic.Value

func init() {
	now.Store(time.Now())

	tick := time.NewTicker(tick)
	go func() {
		for range tick.C {
			now.Store(time.Now())
		}
	}()
}

func Now() time.Time {
	return now.Load().(time.Time)
}
