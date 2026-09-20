package memcache_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/pior/memcache"
	"github.com/pior/memcache/meta"
)

// Example shows the shape of a typical client: one client per process, shared
// by every goroutine, closed on shutdown.
func Example() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211", "localhost:11212"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	ctx := context.Background()

	// A store reports its outcome in the result; the error is reserved for
	// transport failures.
	if _, err := client.Set(ctx, "greeting", []byte("hello"),
		memcache.StoreOptions{TTL: memcache.ExpiresIn(time.Hour)}); err != nil {
		log.Printf("set: %v", err)
	}

	item, err := client.Get(ctx, "greeting")
	if err != nil {
		log.Printf("get: %v", err)
	}
	if item.Found {
		fmt.Printf("%s\n", item.Value)
	}
}

// Cache-aside, the pattern most applications want: read the cache, fall back
// to the origin on a miss, then populate the cache for the next reader.
//
// A cache error is handled exactly like a miss. The origin is the source of
// truth, so a degraded cache costs latency, never correctness — and that is
// what makes the client's fail-fast timeouts safe to lean on.
func ExampleClient_Get_cacheAside() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	loadUser := func(ctx context.Context, userID string) ([]byte, error) {
		key := "user:" + userID

		item, err := client.Get(ctx, key)
		switch {
		case err != nil:
			log.Printf("memcache get: %v", err) // degraded: treat as a miss
		case item.Found:
			return item.Value, nil
		}

		user, err := fetchUserFromDatabase(ctx, userID)
		if err != nil {
			return nil, err
		}

		// Populating the cache is best-effort too.
		if _, err := client.Set(ctx, key, user,
			memcache.StoreOptions{TTL: memcache.ExpiresIn(5 * time.Minute)}); err != nil {
			log.Printf("memcache set: %v", err)
		}
		return user, nil
	}

	user, err := loadUser(context.Background(), "42")
	if err != nil {
		log.Printf("load user: %v", err)
		return
	}
	fmt.Printf("%s\n", user)
}

// Read-modify-write without losing a concurrent update: the read returns a CAS
// token, and passing it back makes the write conditional on the item not
// having changed since. A CASMismatch means someone else won the race, so
// re-read and try again.
//
// Bound the retries. Under contention a cache is the wrong place to serialize
// writes; the origin is.
func ExampleClient_Set_compareAndSwap() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	ctx := context.Background()
	const key = "cart:42"

	for attempt := range 3 {
		item, err := client.Get(ctx, key)
		if err != nil {
			log.Printf("get: %v", err)
			return
		}

		if !item.Found {
			// Claim the key. Add fails with Exists if another writer got
			// there first, which sends us around the loop to read theirs.
			res, err := client.Add(ctx, key, []byte("socks"),
				memcache.StoreOptions{TTL: memcache.ExpiresIn(time.Hour)})
			if err != nil {
				log.Printf("add: %v", err)
				return
			}
			if res.Stored() {
				fmt.Println("created")
				return
			}
			continue
		}

		updated := append(bytes.Clone(item.Value), ",shoes"...)

		res, err := client.Set(ctx, key, updated, memcache.StoreOptions{
			CAS: item.CAS, // the precondition
			TTL: memcache.ExpiresIn(time.Hour),
		})
		if err != nil {
			log.Printf("set: %v", err)
			return
		}
		if res.Stored() {
			fmt.Printf("updated on attempt %d\n", attempt+1)
			return
		}
		// res.Status is memcache.CASMismatch: re-read and retry.
	}
	fmt.Println("gave up after 3 attempts")
}

// Counters live entirely in the cache: Increment is atomic on the server, so
// concurrent callers never lose a tick. Create seeds the key on first use, and
// a TTL expires the whole counter with its window — the usual shape of a rate
// limiter or a rolling metric.
func ExampleClient_Increment() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	const limit = 100

	count, err := client.Increment(context.Background(), "ratelimit:user:42", 1,
		memcache.CounterOptions{
			Create:  true, // create on miss instead of reporting NotFound
			Initial: 1,    // the value the created key starts at
			TTL:     memcache.ExpiresIn(time.Minute),
		})
	if err != nil {
		// The counter is unavailable. Fail open or closed — a deliberate
		// choice; failing open keeps the service up, failing closed keeps
		// the limit honest.
		log.Printf("increment: %v", err)
		return
	}

	if count.Value > limit {
		fmt.Println("rate limited")
		return
	}
	fmt.Printf("request %d of %d\n", count.Value, limit)
}

// MultiGet is the convenient form of batching: keys are grouped by server,
// each group is pipelined as one round trip, the groups run concurrently, and
// results come back in the order the keys were passed.
//
// The per-operation timeout bounds each response read, not the whole batch, so
// pass a context with a deadline to cap the total.
func ExampleClient_MultiGet() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211", "localhost:11212"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	keys := []string{"user:1", "user:2", "user:3"}
	items, err := client.MultiGet(ctx, keys)
	if err != nil {
		log.Printf("multiget: %v", err) // whole batch failed; treat as all-miss
		return
	}

	var missing []string
	for i, item := range items {
		if !item.Found {
			missing = append(missing, keys[i])
			continue
		}
		fmt.Printf("%s = %s\n", item.Key, item.Value)
	}
	fmt.Printf("%d missing\n", len(missing))
}

// ExecuteBatch is the general form of batching: any mix of meta commands in
// one pipelined round trip per server. This is what MultiGet and friends are
// built on, and what the legacy text protocol cannot express.
//
// Requests are built with the meta package and responses come back by
// position: resps[i] answers reqs[i]. Unlike the response handed to a
// ResponseFunc, batch responses are owned by the caller, so their values can
// be kept without copying. Quiet requests are rejected — they suppress
// responses, which would break the positional matching.
func ExampleClient_ExecuteBatch() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reqs := []*meta.Request{
		// Read a profile, asking for the value and its CAS token.
		meta.NewRequest(meta.CmdGet, "profile:42", nil).
			AddReturnValue().
			AddReturnCAS(),
		// Refresh a session, expiring in 5 minutes.
		meta.NewRequest(meta.CmdSet, "session:42", []byte("live")).
			AddTTL(300),
		// Bump a hit counter, creating it on miss with a 1 hour TTL.
		meta.NewRequest(meta.CmdArithmetic, "hits:42", nil).
			AddModeIncrement().
			AddDelta(1).
			AddInitialValue(0).
			AddVivify(3600).
			AddReturnValue(),
	}

	resps, err := client.ExecuteBatch(ctx, reqs)
	if err != nil {
		log.Printf("batch: %v", err)
		return
	}

	// A per-request protocol error is carried on the response, not returned.
	for i, resp := range resps {
		if resp.HasError() {
			log.Printf("request %d: %v", i, resp.Error)
		}
	}

	if profile := resps[0]; profile.HasValue() {
		cas, _ := profile.CAS()
		fmt.Printf("profile=%s cas=%d\n", profile.Data, cas)
	}
	fmt.Printf("session stored: %t\n", resps[1].IsSuccess())
	if hits := resps[2]; hits.HasValue() {
		n, _ := strconv.ParseUint(string(hits.Data), 10, 64)
		fmt.Printf("hits=%d\n", n)
	}
}

// Errors report that an operation did not complete. A miss is not an error:
// it is Item.Found, or a Status on a write.
//
// Branch on the cause with errors.Is, and read the OpError for logging and
// metrics — the key is deliberately kept out of the error message.
func ExampleOpError() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211"),
		memcache.Config{
			MaxConnsPerServer: 20,
			Breaker:           memcache.BreakerConfig{Enabled: true},
		},
	)
	defer client.Close()

	_, err := client.Get(context.Background(), "user:42")
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, memcache.ErrBreakerOpen):
		fmt.Println("server is shedding load; serve from the origin")
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Println("budget spent; serve from the origin")
	case errors.Is(err, memcache.ErrClientClosed):
		fmt.Println("client is shut down")
	}

	if opErr, ok := errors.AsType[*memcache.OpError](err); ok {
		log.Printf("memcache %s on %s failed (key %q): %v",
			opErr.Op, opErr.Server, opErr.Key, opErr.Unwrap())
	}
}

// fetchUserFromDatabase stands in for the origin the cache fronts.
var fetchUserFromDatabase = func(ctx context.Context, userID string) ([]byte, error) {
	return []byte(`{"id":"` + userID + `"}`), ctx.Err()
}
