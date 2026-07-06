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
	Replace(ctx context.Context, item Item) (bool, error)
	Append(ctx context.Context, item Item) (bool, error)
	Prepend(ctx context.Context, item Item) (bool, error)
	Touch(ctx context.Context, key string, ttl TTL) (bool, error)
	GetAndTouch(ctx context.Context, key string, ttl TTL) (Item, error)
	FlushAll(ctx context.Context) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error)
	Decrement(ctx context.Context, key string, delta uint64, ttl TTL) (Counter, error)
}

// Executor executes a memcache request for a given key.
// The key is provided separately to allow server selection based on the key.
//
// Execute invokes consume with the decoded response while the underlying
// connection is still checked out. The Response and its Data and Flags storage
// belong to that connection and are only valid during the consume call:
// implementations reuse them for the next response on the same connection.
// Consume must copy anything it needs to retain (e.g. bytes.Clone the value).
// An error returned by consume is returned to the Execute caller unchanged; it
// does not affect connection handling or the circuit breaker.
type Executor interface {
	Execute(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) error
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

// FlushExecutor is an optional interface for executing the keyless text
// protocol flush_all command.
type FlushExecutor interface {
	ExecuteFlushAll(ctx context.Context) error
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

// FlushAll invalidates all items managed by the executor.
func (c *Commands) FlushAll(ctx context.Context) error {
	executor, ok := c.executor.(FlushExecutor)
	if !ok {
		return fmt.Errorf("memcache: executor does not support flush_all")
	}
	return executor.ExecuteFlushAll(ctx)
}

// Get retrieves a single item from memcache.
func (c *Commands) Get(ctx context.Context, key string) (Item, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue()

	var item Item
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			item = Item{Key: key, Found: false}
			return nil
		}

		if resp.HasError() {
			return resp.Error
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("unexpected response status: %s", resp.Status)
		}

		item = Item{
			Key: key,
			// resp.Data belongs to the connection and is reused after consume
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

	return c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.HasError() {
			return resp.Error
		}

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

	return c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.HasError() {
			return resp.Error
		}

		if resp.IsNotStored() {
			return fmt.Errorf("%w: key already exists", ErrNotStored)
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("add failed with status: %s", resp.Status)
		}

		return nil
	})
}

// Replace stores an item only if its key already exists.
// The returned boolean reports whether the item was stored.
func (c *Commands) Replace(ctx context.Context, item Item) (bool, error) {
	return c.conditionalStore(ctx, item, meta.ModeReplace, true, "replace")
}

// Append adds item.Value after the existing value without changing its TTL.
// The returned boolean reports whether the key existed and was updated.
func (c *Commands) Append(ctx context.Context, item Item) (bool, error) {
	return c.conditionalStore(ctx, item, meta.ModeAppend, false, "append")
}

// Prepend adds item.Value before the existing value without changing its TTL.
// The returned boolean reports whether the key existed and was updated.
func (c *Commands) Prepend(ctx context.Context, item Item) (bool, error) {
	return c.conditionalStore(ctx, item, meta.ModePrepend, false, "prepend")
}

func (c *Commands) conditionalStore(ctx context.Context, item Item, mode string, applyTTL bool, operation string) (bool, error) {
	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value).AddMode(mode)
	if applyTTL {
		if exptime := item.TTL.Expiration(); exptime != 0 {
			req.AddTTL(exptime)
		}
	}

	stored := false
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.HasError() {
			return resp.Error
		}
		if resp.IsNotStored() || resp.Status == meta.StatusNF {
			return nil
		}
		if !resp.IsSuccess() {
			return fmt.Errorf("%s failed with status: %s", operation, resp.Status)
		}
		stored = true
		return nil
	})
	return stored, err
}

// Touch updates the expiration of an existing key.
// The returned boolean reports whether the key existed.
func (c *Commands) Touch(ctx context.Context, key string, ttl TTL) (bool, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil).AddTTL(ttl.Expiration())

	found := false
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			return nil
		}
		if resp.HasError() {
			return resp.Error
		}
		if resp.Status != meta.StatusHD {
			return fmt.Errorf("touch failed with status: %s", resp.Status)
		}
		found = true
		return nil
	})
	return found, err
}

// GetAndTouch retrieves an item and updates its expiration atomically.
func (c *Commands) GetAndTouch(ctx context.Context, key string, ttl TTL) (Item, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil).AddReturnValue().AddTTL(ttl.Expiration())

	item := Item{Key: key}
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			return nil
		}
		if resp.HasError() {
			return resp.Error
		}
		if !resp.HasValue() {
			return fmt.Errorf("get and touch failed with status: %s", resp.Status)
		}
		item.Value = bytes.Clone(resp.Data)
		item.Found = true
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// Delete removes an item from memcache.
func (c *Commands) Delete(ctx context.Context, key string) error {
	req := meta.NewRequest(meta.CmdDelete, key, nil)
	return c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.HasError() {
			return resp.Error
		}

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
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			counter = Counter{Key: key}
			return nil
		}

		if resp.HasError() {
			return resp.Error
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
