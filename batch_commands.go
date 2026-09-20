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

// SetItem is one item to store with MultiSet, the write-side counterpart of
// Item. Options apply to that item only: the zero value is a plain set with no
// expiration.
type SetItem struct {
	Key     string
	Value   []byte
	Options StoreOptions
}

// MultiGet retrieves multiple items in a single batch operation, populating
// Value, Flags and CAS. Returns items in the same order as the keys, with
// Found=false for missing items. The options apply to every key: a non-zero
// TTL touches each item read (get-and-touch).
//
// The per-operation [Config.OperationTimeout] bounds each response read, not
// the whole batch: a server that answers slowly can hold the operation for up
// to len(keys) × OperationTimeout. Pass a context with a deadline to cap the
// total.
func (b *BatchCommands) MultiGet(ctx context.Context, keys []string, opts ...GetOptions) ([]Item, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	opt := firstOpt(opts)

	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		reqs[i] = getRequest(key, opt)
	}

	responses, err := b.executeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	items := make([]Item, len(keys))
	for i, resp := range responses {
		// Batch responses are caller-owned (the BatchExecutor contract), so
		// the value is kept without a clone.
		items[i], err = readItem(keys[i], resp, resp.Data)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// MultiSet stores multiple items in a single batch operation. Returns one
// StoreResult per item, in order; an item that did not land (CAS mismatch)
// is reported there, not as an error. The error is reserved for transport
// failures and protocol errors, in which case no results are returned.
func (b *BatchCommands) MultiSet(ctx context.Context, items []SetItem) ([]StoreResult, error) {
	if len(items) == 0 {
		return nil, nil
	}

	reqs := make([]*meta.Request, len(items))
	srs := make([]storeRequest, len(items))
	for i, item := range items {
		srs[i] = storeRequest{ttl: item.Options.TTL, flags: item.Options.Flags, cas: item.Options.CAS}
		reqs[i] = srs[i].request(item.Key, item.Value)
	}

	responses, err := b.executeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	results := make([]StoreResult, len(items))
	for i, resp := range responses {
		results[i], err = srs[i].result(resp)
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", items[i].Key, err)
		}
	}
	return results, nil
}

// MultiDelete removes multiple items in a single batch operation. Returns one
// Status per key, in order: Applied, or NotFound for a key that did not exist.
func (b *BatchCommands) MultiDelete(ctx context.Context, keys []string) ([]Status, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		reqs[i] = meta.NewRequest(meta.CmdDelete, key, nil)
	}

	responses, err := b.executeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	statuses := make([]Status, len(keys))
	for i, resp := range responses {
		statuses[i], err = deleteStatus(resp)
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", keys[i], err)
		}
	}
	return statuses, nil
}

// executeBatch runs the pipeline and returns exactly one well-formed response
// per request: a transport failure, a count mismatch, or a protocol error on
// any response is returned as the error.
func (b *BatchCommands) executeBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	responses, err := b.executor.ExecuteBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}
	if len(responses) != len(reqs) {
		return nil, fmt.Errorf("memcache: got %d responses for %d requests", len(responses), len(reqs))
	}
	for i, resp := range responses {
		if resp.HasError() {
			return nil, fmt.Errorf("key %s: %w", reqs[i].Key, resp.Error)
		}
	}
	return responses, nil
}
