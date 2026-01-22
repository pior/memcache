package memcache

import (
	"context"
	"fmt"

	"github.com/pior/memcache/meta"
)

// BatchCommands provides batch operations using a BatchExecutor.
// This struct is explicitly designed for batch operations and requires
// an executor that implements BatchExecutor.
type BatchCommands struct {
	executor BatchExecutor
}

// NewBatchCommands creates a new BatchCommands instance.
// The executor must implement BatchExecutor (e.g., ServerPool or Client).
func NewBatchCommands(executor BatchExecutor) *BatchCommands {
	return &BatchCommands{
		executor: executor,
	}
}

// MultiGet retrieves multiple items in a single batch operation.
// Returns items in the same order as the keys, with Found=false for missing items.
func (b *BatchCommands) MultiGet(ctx context.Context, keys []string) ([]Item, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Build batch requests
	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		req := meta.NewRequest(meta.CmdGet, key, nil)
		req.AddReturnValue()
		reqs[i] = req
	}

	// Execute batch
	responses, err := b.executor.ExecuteBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	// Process responses
	items := make([]Item, len(keys))
	for i, resp := range responses {
		if i >= len(keys) {
			break // Safety check
		}

		key := keys[i]

		if resp.HasError() {
			return nil, resp.Error
		}

		if resp.IsMiss() {
			items[i] = Item{Key: key, Found: false}
		} else if resp.IsSuccess() {
			items[i] = Item{
				Key:   key,
				Value: resp.Data,
				Found: true,
			}
		} else {
			return nil, fmt.Errorf("unexpected response status for key %s: %s", key, resp.Status)
		}
	}

	return items, nil
}

// MultiSet stores multiple items in a single batch operation.
// Returns error on first failure.
func (b *BatchCommands) MultiSet(ctx context.Context, items []Item) error {
	if len(items) == 0 {
		return nil
	}

	// Build batch requests
	reqs := make([]*meta.Request, len(items))
	for i, item := range items {
		req := meta.NewRequest(meta.CmdSet, item.Key, item.Value)
		if item.TTL > 0 {
			req.AddTTL(int(item.TTL.Seconds()))
		}
		reqs[i] = req
	}

	// Execute batch
	responses, err := b.executor.ExecuteBatch(ctx, reqs)
	if err != nil {
		return err
	}

	// Process responses - check for errors
	for i, resp := range responses {
		if i >= len(items) {
			break // Safety check
		}

		if resp.HasError() {
			return resp.Error
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("set failed for key %s with status: %s", items[i].Key, resp.Status)
		}
	}

	return nil
}

// MultiDelete removes multiple items in a single batch operation.
// Returns error on first failure.
func (b *BatchCommands) MultiDelete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Build batch requests
	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		reqs[i] = meta.NewRequest(meta.CmdDelete, key, nil)
	}

	// Execute batch
	responses, err := b.executor.ExecuteBatch(ctx, reqs)
	if err != nil {
		return err
	}

	// Process responses - check for errors
	for i, resp := range responses {
		if i >= len(keys) {
			break // Safety check
		}

		if resp.HasError() {
			return resp.Error
		}

		// Delete is successful even if key doesn't exist
		if resp.Status != meta.StatusHD && resp.Status != meta.StatusNF {
			return fmt.Errorf("delete failed for key %s with status: %s", keys[i], resp.Status)
		}
	}

	return nil
}
