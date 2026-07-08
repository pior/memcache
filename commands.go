package memcache

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/pior/memcache/meta"
)

type Querier interface {
	Get(ctx context.Context, key string, opts ...GetOptions) (Item, error)
	Set(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Add(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Replace(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Append(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error)
	Prepend(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error)
	Touch(ctx context.Context, key string, ttl TTL) (bool, error)
	Delete(ctx context.Context, key string, opts ...DeleteOptions) (Status, error)
	Increment(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error)
	Decrement(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error)
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

// Get retrieves a single item from memcache, populating Value, Flags, and CAS.
// A GetOptions with a non-zero TTL also touches the item, updating its
// expiration while reading (get-and-touch).
func (c *Commands) Get(ctx context.Context, key string, opts ...GetOptions) (Item, error) {
	opt := firstOpt(opts)

	req := meta.NewRequest(meta.CmdGet, key, nil).
		AddReturnValue().
		AddReturnCAS().
		AddReturnClientFlags()
	if exptime := opt.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}

	item := Item{Key: key}
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			return nil
		}
		if resp.HasError() {
			return resp.Error
		}
		if !resp.HasValue() {
			return fmt.Errorf("unexpected response status: %s", resp.Status)
		}
		// resp.Data belongs to the connection and is reused after consume
		// returns; the Item must own its value.
		item.Value = bytes.Clone(resp.Data)
		item.Found = true
		if v, ok := resp.CAS(); ok {
			item.CAS = CAS(v)
		}
		if f, ok := resp.ClientFlags(); ok {
			item.Flags = f
		}
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// Set stores value at key. A StoreOptions with a non-zero CAS makes the write
// conditional: it applies only if the stored item's CAS matches, otherwise the
// result reports CASMismatch.
func (c *Commands) Set(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	// A plain set overwrites unconditionally, so it never reports NotFound or
	// Exists; nsMeans is unused.
	return c.store(ctx, key, value, "", opt.TTL, opt.Flags, opt.CAS, nil, Applied)
}

// Add stores value at key only if the key does not already exist. When it does,
// the result reports Exists.
func (c *Commands) Add(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, meta.ModeAdd, opt.TTL, opt.Flags, opt.CAS, nil, Exists)
}

// Replace stores value at key only if the key already exists. When it does not,
// the result reports NotFound.
func (c *Commands) Replace(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, meta.ModeReplace, opt.TTL, opt.Flags, opt.CAS, nil, NotFound)
}

// Append adds value after the existing value without changing its TTL. When the
// key is absent the result reports NotFound, unless ConcatOptions.CreateOnMiss
// is set, in which case the key is created and the result reports Applied.
func (c *Commands) Append(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, meta.ModeAppend, TTL{}, 0, opt.CAS, opt.CreateOnMiss, NotFound)
}

// Prepend adds value before the existing value without changing its TTL. When
// the key is absent the result reports NotFound, unless
// ConcatOptions.CreateOnMiss is set, in which case the key is created.
func (c *Commands) Prepend(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, meta.ModePrepend, TTL{}, 0, opt.CAS, opt.CreateOnMiss, NotFound)
}

// store builds and runs a meta set with the given mode. nsMeans says how to
// interpret an NS status for this mode (add: Exists; replace/concat: NotFound).
func (c *Commands) store(ctx context.Context, key string, value []byte, mode string, ttl TTL, flags uint32, cas CAS, create *CreateOnMiss, nsMeans Status) (StoreResult, error) {
	req := meta.NewRequest(meta.CmdSet, key, value).AddReturnCAS()
	if mode != "" {
		req.AddMode(mode)
	}
	if exptime := ttl.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}
	if flags != 0 {
		req.AddClientFlags(flags)
	}
	if cas != 0 {
		req.AddCAS(uint64(cas))
	}
	if create != nil {
		req.AddVivify(create.TTL.Expiration())
		if create.Flags != 0 {
			req.AddClientFlags(create.Flags)
		}
	}

	var result StoreResult
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		switch {
		case resp.HasError():
			return resp.Error
		case resp.IsSuccess():
			result.Status = Applied
			if v, ok := resp.CAS(); ok {
				result.CAS = CAS(v)
			}
		case resp.IsCASMismatch(): // EX
			result.Status = CASMismatch
		case resp.Status == meta.StatusNF:
			result.Status = NotFound
		case resp.IsNotStored(): // NS — meaning depends on the mode
			result.Status = nsMeans
		default:
			return fmt.Errorf("unexpected store status: %s", resp.Status)
		}
		return nil
	})
	if err != nil {
		return StoreResult{}, err
	}
	return result, nil
}

// Touch updates the expiration of an existing key without transferring its
// value. It reports whether the key existed.
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

// Delete removes the item at key. A DeleteOptions with a non-zero CAS deletes
// only if the CAS matches; a mismatch reports CASMismatch and a missing key
// reports NotFound.
func (c *Commands) Delete(ctx context.Context, key string, opts ...DeleteOptions) (Status, error) {
	opt := firstOpt(opts)

	req := meta.NewRequest(meta.CmdDelete, key, nil)
	if opt.CAS != 0 {
		req.AddCAS(uint64(opt.CAS))
	}

	status := Applied
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		switch {
		case resp.HasError():
			return resp.Error
		case resp.Status == meta.StatusHD:
			status = Applied
		case resp.IsCASMismatch():
			status = CASMismatch
		case resp.Status == meta.StatusNF:
			status = NotFound
		default:
			return fmt.Errorf("delete failed with status: %s", resp.Status)
		}
		return nil
	})
	if err != nil {
		return status, err
	}
	return status, nil
}

// Increment increments a counter key by delta. By default a missing key reports
// NotFound; set CounterOptions.Initial to create it on miss seeded with that
// value. NoTTL means infinite TTL.
func (c *Commands) Increment(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error) {
	return c.arithmetic(ctx, key, delta, firstOpt(opts), false)
}

// Decrement decrements a counter key by delta, stopping at zero. By default a
// missing key reports NotFound; set CounterOptions.Initial to create it on miss.
// NoTTL means infinite TTL.
func (c *Commands) Decrement(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error) {
	return c.arithmetic(ctx, key, delta, firstOpt(opts), true)
}

func (c *Commands) arithmetic(ctx context.Context, key string, delta uint64, opt CounterOptions, decrement bool) (Counter, error) {
	req := meta.NewRequest(meta.CmdArithmetic, key, nil).AddReturnValue().AddReturnCAS()
	req.AddDelta(delta)

	operation := "increment"
	if decrement {
		req.AddModeDecrement()
		operation = "decrement"
	}
	if opt.Initial != nil {
		req.AddInitialValue(*opt.Initial)
		req.AddVivify(opt.TTL.Expiration())
	}
	if opt.CAS != 0 {
		req.AddCAS(uint64(opt.CAS))
	}
	if exptime := opt.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}

	counter := Counter{Key: key}
	err := c.executor.Execute(ctx, req, func(resp *meta.Response) error {
		switch {
		case resp.HasError():
			return resp.Error
		case resp.IsCASMismatch():
			counter.Status = CASMismatch
			return nil
		case resp.IsMiss():
			counter.Status = NotFound
			return nil
		case !resp.HasValue():
			return fmt.Errorf("%s response missing value", operation)
		}

		parsed, err := strconv.ParseUint(string(resp.Data), 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse %s result: %w", operation, err)
		}
		counter.Value = parsed
		counter.Status = Applied
		if v, ok := resp.CAS(); ok {
			counter.CAS = CAS(v)
		}
		return nil
	})
	if err != nil {
		return Counter{Key: key}, err
	}
	return counter, nil
}
