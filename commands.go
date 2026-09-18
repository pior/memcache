package memcache

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/pior/memcache/meta"
)

type Querier interface {
	Get(ctx context.Context, key string) (Item, error)
	Set(ctx context.Context, item Item) error
	Add(ctx context.Context, item Item) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error)
	Decrement(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error)
}

// ResponseFunc receives a response while its connection is still checked
// out. The Response and its Data and Flags storage belong to the connection
// and are valid only during the call: implementations reuse them for the next
// response on the same connection. Clone anything retained (e.g. bytes.Clone
// the value).
type ResponseFunc func(*meta.Response)

// Executor executes a memcache request for a given key.
// The key is provided separately to allow server selection based on the key.
//
// Execute runs req and calls fn with the decoded response. It returns
// transport and pool errors only; the command outcome (a miss, a protocol
// error, an unexpected status) is in the response, for fn to interpret.
type Executor interface {
	Execute(ctx context.Context, req *meta.Request, fn ResponseFunc) error
}

// BatchExecutor is an optional interface that Executors can implement to support
// efficient batch operations using pipelining.
// If the executor doesn't implement this, Commands will fall back to individual Execute calls.
type BatchExecutor interface {
	Executor
	ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error)
}

// StatsExecutor is an optional interface for executing the stats command.
// The stats command has a different response format than regular meta commands.
type StatsExecutor interface {
	ExecuteStats(ctx context.Context, args ...string) (map[string]string, error)
}

// Commands provides memcache command operations.
// This struct can be used independently with a custom Executor,
// or embedded in Client for full resilience features.
type Commands struct {
	executor Executor
}

var _ Querier = (*Commands)(nil)

// NewCommands creates a new Commands instance with the given executor.
func NewCommands(executor Executor) *Commands {
	return &Commands{
		executor: executor,
	}
}

// execute runs req and returns the transport error if any, else the protocol
// error carried by the response (ERROR, CLIENT_ERROR, SERVER_ERROR), else the
// outcome fn derives from a well-formed response. This is the one place where
// the command outcome and the transport error meet.
func (c *Commands) execute(ctx context.Context, req *meta.Request, fn func(*meta.Response) error) error {
	var outcome error
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) {
		if resp.HasError() {
			outcome = resp.Error
			return
		}
		outcome = fn(resp)
	})
	if err != nil {
		return err
	}
	return outcome
}

// Get retrieves a single item from memcache.
func (c *Commands) Get(ctx context.Context, key string) (Item, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()

	var item Item
	err := c.execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			item = Item{Key: key, Found: false}
			return nil
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("unexpected response status: %s", resp.Status)
		}

		item = Item{
			Key: key,
			// resp.Data belongs to the connection and is reused after fn
			// returns; the Item must own its value.
			Value: bytes.Clone(resp.Data),
			Found: true,
		}
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// Set stores an item in memcache.
func (c *Commands) Set(ctx context.Context, item Item) error {
	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value)

	// Add TTL flag if specified, otherwise use no expiration
	if exptime := item.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}

	return c.execute(ctx, req, func(resp *meta.Response) error {
		if !resp.IsSuccess() {
			return fmt.Errorf("set failed with status: %s", resp.Status)
		}

		return nil
	})
}

// Add stores an item in memcache only if the key doesn't already exist.
func (c *Commands) Add(ctx context.Context, item Item) error {
	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value).AddModeAdd()
	if exptime := item.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}

	return c.execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsNotStored() {
			return fmt.Errorf("%w: key already exists", ErrNotStored)
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("add failed with status: %s", resp.Status)
		}

		return nil
	})
}

// Delete removes an item from memcache.
func (c *Commands) Delete(ctx context.Context, key string) error {
	req := meta.NewRequest(meta.CmdDelete, key, nil)
	return c.execute(ctx, req, func(resp *meta.Response) error {
		// Delete is successful even if key doesn't exist
		if resp.Status != meta.StatusHD && resp.Status != meta.StatusNF {
			return fmt.Errorf("delete failed with status: %s", resp.Status)
		}

		return nil
	})
}

// Increment increments a counter key by delta. If the key does not exist, it is
// created with delta as its initial value. NoTTL means infinite TTL.
func (c *Commands) Increment(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error) {
	return c.arithmetic(ctx, key, delta, ttl, false)
}

// Decrement decrements a counter key by delta, stopping at zero. A missing key
// is reported with Found set to false. NoTTL means infinite TTL.
func (c *Commands) Decrement(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error) {
	return c.arithmetic(ctx, key, delta, ttl, true)
}

func (c *Commands) arithmetic(ctx context.Context, key string, delta uint64, ttl TTL, decrement bool) (Counter, error) {
	req := meta.NewRequest(meta.CmdArithmetic, key, nil).AddReturnValue()
	exptime := ttl.Expiration()

	req.AddDelta(delta)
	operation := "increment"
	if decrement {
		req.AddModeDecrement()
		operation = "decrement"
	} else {
		req.AddInitialValue(delta)
		req.AddVivify(exptime)
	}

	if exptime != 0 {
		req.AddTTL(exptime)
	}

	var counter Counter
	err := c.execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			counter = Counter{Key: key}
			return nil
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("%s failed with status: %s", operation, resp.Status)
		}

		if !resp.HasValue() {
			return fmt.Errorf("%s response missing value", operation)
		}

		parsed, err := strconv.ParseUint(string(resp.Data), 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse %s result: %w", operation, err)
		}

		counter = Counter{Key: key, Value: parsed, Found: true}
		return nil
	})
	if err != nil {
		return Counter{}, err
	}
	return counter, nil
}
