package memcache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/pior/memcache/meta"
)

type Querier interface {
	Get(ctx context.Context, key string) (Item, error)
	Set(ctx context.Context, item Item) error
	Add(ctx context.Context, item Item) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
}

// Executor executes a memcache request for a given key.
// The key is provided separately to allow server selection based on the key.
type Executor interface {
	Execute(ctx context.Context, req *meta.Request) (*meta.Response, error)
}

// BatchExecutor is an optional interface that Executors can implement to support
// efficient batch operations using pipelining.
// If the executor doesn't implement this, Commands will fall back to individual Execute calls.
type BatchExecutor interface {
	Executor
	ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error)
}

// Commands provides memcache command operations.
// This struct can be used independently with a custom ExecuteFunc,
// or embedded in Client for full resilience features.
type Commands struct {
	executor Executor
}

var _ Querier = (*Commands)(nil)

// NewCommands creates a new Commands instance with the given execute function.
func NewCommands(executor Executor) *Commands {
	return &Commands{
		executor: executor,
	}
}

// Get retrieves a single item from memcache.
func (c *Commands) Get(ctx context.Context, key string) (Item, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil, []meta.Flag{{Type: meta.FlagReturnValue}})
	resp, err := c.executor.Execute(ctx, req)
	if err != nil {
		return Item{}, err
	}

	if resp.IsMiss() {
		return Item{Key: key, Found: false}, nil
	}

	if resp.HasError() {
		return Item{}, resp.Error
	}

	if !resp.IsSuccess() {
		return Item{}, fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	return Item{
		Key:   key,
		Value: resp.Data,
		Found: true,
	}, nil
}

// Set stores an item in memcache.
func (c *Commands) Set(ctx context.Context, item Item) error {
	// Build flags - mode is Set by default, no need to specify
	var flags []meta.Flag

	// Add TTL flag if specified, otherwise use no expiration
	if item.TTL > 0 {
		flags = []meta.Flag{meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds()))}
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.executor.Execute(ctx, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		return resp.Error
	}

	if !resp.IsSuccess() {
		return fmt.Errorf("set failed with status: %s", resp.Status)
	}

	return nil
}

// Add stores an item in memcache only if the key doesn't already exist.
func (c *Commands) Add(ctx context.Context, item Item) error {
	// Build flags
	flags := []meta.Flag{
		{Type: meta.FlagMode, Token: string(meta.ModeAdd)},
	}

	if item.TTL > 0 {
		flags = append(flags, meta.FormatFlagInt(meta.FlagTTL, int(item.TTL.Seconds())))
	}

	req := meta.NewRequest(meta.CmdSet, item.Key, item.Value, flags)
	resp, err := c.executor.Execute(ctx, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		return resp.Error
	}

	if resp.IsNotStored() {
		return fmt.Errorf("key already exists")
	}

	if !resp.IsSuccess() {
		return fmt.Errorf("add failed with status: %s", resp.Status)
	}

	return nil
}

// Delete removes an item from memcache.
func (c *Commands) Delete(ctx context.Context, key string) error {
	req := meta.NewRequest(meta.CmdDelete, key, nil, nil)
	resp, err := c.executor.Execute(ctx, req)
	if err != nil {
		return err
	}

	if resp.HasError() {
		return resp.Error
	}

	// Delete is successful even if key doesn't exist
	if resp.Status != meta.StatusHD && resp.Status != meta.StatusNF {
		return fmt.Errorf("delete failed with status: %s", resp.Status)
	}

	return nil
}

// Increment increments a counter key by the given delta.
// Creates the key with the delta value if it doesn't exist.
// This uses auto-vivify (N flag) with initial value (J flag) set to the delta,
// so the returned value is correct even on first call.
// TTL of 0 means infinite TTL.
func (c *Commands) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	// Build flags for increment/decrement with auto-vivify
	var flags []meta.Flag

	// Calculate TTL in seconds for vivify flag
	ttlSeconds := int64(0)
	if ttl > 0 {
		ttlSeconds = int64(ttl.Seconds())
	}

	if delta >= 0 {
		// Positive delta - use increment mode (default)
		flags = []meta.Flag{
			{Type: meta.FlagReturnValue},
			{Type: meta.FlagDelta, Token: strconv.FormatInt(delta, 10)},
			{Type: meta.FlagInitialValue, Token: strconv.FormatInt(delta, 10)}, // Initialize to delta on creation
			{Type: meta.FlagVivify, Token: strconv.FormatInt(ttlSeconds, 10)},  // Auto-create with specified TTL
		}
	} else {
		// Negative delta - use decrement mode with absolute value
		// For decrement, initialize to 0 since we can't have negative counters
		flags = []meta.Flag{
			{Type: meta.FlagReturnValue},
			{Type: meta.FlagDelta, Token: strconv.FormatInt(-delta, 10)}, // Use absolute value
			{Type: meta.FlagMode, Token: string(meta.ModeDecrement)},
			{Type: meta.FlagInitialValue, Token: "0"},                         // Initialize to 0 on creation
			{Type: meta.FlagVivify, Token: strconv.FormatInt(ttlSeconds, 10)}, // Auto-create with specified TTL
		}
	}

	// Add TTL flag to update TTL on existing keys if TTL > 0
	if ttl > 0 {
		flags = append(flags, meta.Flag{Type: meta.FlagTTL, Token: strconv.FormatInt(ttlSeconds, 10)})
	}

	req := meta.NewRequest(meta.CmdArithmetic, key, nil, flags)
	resp, err := c.executor.Execute(ctx, req)
	if err != nil {
		return 0, err
	}

	if resp.HasError() {
		return 0, resp.Error
	}

	if !resp.IsSuccess() {
		return 0, fmt.Errorf("increment failed with status: %s", resp.Status)
	}

	// Parse the returned value
	if !resp.HasValue() {
		return 0, fmt.Errorf("increment response missing value")
	}

	value, err := strconv.ParseInt(string(resp.Data), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse increment result: %w", err)
	}

	return value, nil
}

// MultiGet retrieves multiple items in a batch.
// If the executor implements BatchExecutor, uses pipelined requests for efficiency.
// Otherwise, falls back to individual Get calls.
// Returns items in the same order as keys. Missing keys have Found=false.
//
// Note: This assumes all keys go to the same executor (single server).
// For multi-server scenarios, use Client.MultiGet which handles server routing.
