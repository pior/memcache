package memcache

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/pior/memcache/meta"
)

// Querier is the command surface of [Client]: the single-key and pipelined
// multi-key operations of [Commands].
type Querier interface {
	Get(ctx context.Context, key string, opts ...GetOptions) (Item, error)
	Set(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Add(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Replace(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error)
	Append(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error)
	Prepend(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error)
	Touch(ctx context.Context, key string, ttl TTL) (Status, error)
	Delete(ctx context.Context, key string, opts ...DeleteOptions) (Status, error)
	Increment(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error)
	Decrement(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error)
	MultiGet(ctx context.Context, keys []string, opts ...GetOptions) ([]Item, error)
	MultiSet(ctx context.Context, items []SetItem) ([]StoreResult, error)
	MultiDelete(ctx context.Context, keys []string) ([]Status, error)
}

// ResponseFunc receives a response while its connection is still checked
// out. The Response and its Data and Flags storage belong to the connection
// and are valid only during the call: implementations reuse them for the next
// response on the same connection. Clone anything retained (e.g. bytes.Clone
// the value).
type ResponseFunc func(*meta.Response)

// Executor executes memcache requests for the keys they carry.
// The key is part of the request so an implementation can select a server
// from it.
//
// Execute runs req and calls fn with the decoded response. It returns
// transport and pool errors only; the command outcome (a miss, a protocol
// error, a status the operation does not expect) is in the response, for fn
// to interpret. A reply the command cannot produce at all means the
// connection is out of sync: it is a transport error and fn is not called.
//
// ExecuteBatch pipelines reqs and returns the responses by position. Unlike
// Execute, it hands its responses over: each Response and its Data and Flags
// storage is freshly allocated and owned by the caller, so batch callers keep
// them without copying. Implementations must not reuse buffers across the
// responses of a batch or across batches.
type Executor interface {
	Execute(ctx context.Context, req *meta.Request, fn ResponseFunc) error
	ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error)
}

// StatsExecutor is an optional interface for executing the stats command.
// The stats command has a different response format than regular meta commands.
// args are the sub-command tokens sent after "stats", space-separated.
type StatsExecutor interface {
	ExecuteStats(ctx context.Context, args ...string) (map[string]string, error)
}

// Commands provides the memcache operations, single-key and multi-key, on top
// of an [Executor]. It can be used independently with a custom Executor, or
// embedded in Client for full resilience features. It covers the [Querier]
// interface.
type Commands struct {
	executor Executor
}

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

// getRequest builds the mg for Get and MultiGet: value, CAS and client flags
// are always returned; a non-zero TTL touches the item.
func getRequest(key string, opt GetOptions) *meta.Request {
	req := meta.NewRequest(meta.CmdGet, key, nil).
		AddReturnValue().
		AddReturnCAS().
		AddReturnClientFlags()
	if exptime := opt.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}
	return req
}

// readItem interprets a well-formed mg response. value is the response's Data
// when the caller owns it (batch responses), or a clone of it (Execute reuses
// the connection's buffers).
func readItem(key string, resp *meta.Response, value []byte) (Item, error) {
	item := Item{Key: key}
	if resp.IsMiss() {
		item.Status = StatusNotFound
		return item, nil
	}
	if !resp.HasValue() {
		return Item{}, fmt.Errorf("unexpected response status for key %s: %s", key, resp.Status)
	}
	item.Value = value
	item.Status = StatusApplied
	if v, ok := resp.CAS(); ok {
		item.CAS = CAS(v)
	}
	if f, ok := resp.ClientFlags(); ok {
		item.Flags = f
	}
	return item, nil
}

// Get retrieves a single item from memcache, populating Value, Flags, and CAS.
// A GetOptions with a non-zero TTL also touches the item, updating its
// expiration while reading (get-and-touch).
func (c *Commands) Get(ctx context.Context, key string, opts ...GetOptions) (Item, error) {
	req := getRequest(key, firstOpt(opts))

	var item Item
	err := c.execute(ctx, req, func(resp *meta.Response) (err error) {
		// resp.Data belongs to the connection and is reused after fn
		// returns; the Item must own its value.
		item, err = readItem(key, resp, bytes.Clone(resp.Data))
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// Set stores value at key. A StoreOptions with a non-zero CAS makes the write
// conditional: it applies only if the stored item's CAS matches, otherwise the
// result reports StatusCASMismatch.
func (c *Commands) Set(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, storeRequest{ttl: opt.TTL, flags: opt.Flags, cas: opt.CAS})
}

// Add stores value at key only if the key does not already exist. When it does,
// the result reports StatusExists.
func (c *Commands) Add(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, storeRequest{mode: meta.ModeAdd, ttl: opt.TTL, flags: opt.Flags, cas: opt.CAS, nsMeans: StatusExists})
}

// Replace stores value at key only if the key already exists. When it does not,
// the result reports StatusNotFound.
func (c *Commands) Replace(ctx context.Context, key string, value []byte, opts ...StoreOptions) (StoreResult, error) {
	opt := firstOpt(opts)
	return c.store(ctx, key, value, storeRequest{mode: meta.ModeReplace, ttl: opt.TTL, flags: opt.Flags, cas: opt.CAS, nsMeans: StatusNotFound})
}

// Append adds value after the existing value without changing its TTL. When
// the key is absent the result reports StatusNotFound, unless
// ConcatOptions.CreateOnMiss is set, in which case the key is created and the
// result reports StatusApplied.
func (c *Commands) Append(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error) {
	return c.store(ctx, key, value, concatRequest(meta.ModeAppend, firstOpt(opts)))
}

// Prepend adds value before the existing value without changing its TTL. When
// the key is absent the result reports StatusNotFound, unless
// ConcatOptions.CreateOnMiss is set, in which case the key is created.
func (c *Commands) Prepend(ctx context.Context, key string, value []byte, opts ...ConcatOptions) (StoreResult, error) {
	return c.store(ctx, key, value, concatRequest(meta.ModePrepend, firstOpt(opts)))
}

// storeRequest is what store needs beyond key and value: the meta set mode,
// its modifiers, and how to read an NS status for that mode.
type storeRequest struct {
	mode    string // "" for a plain set
	ttl     TTL
	flags   uint32
	cas     CAS
	vivify  bool   // create on miss: ttl and flags then describe the created item
	nsMeans Status // what NS means for this mode (add: StatusExists; replace/concat: StatusNotFound); zero: NS is unexpected
}

// concatRequest maps ConcatOptions onto a storeRequest. The server ignores
// expiration and flags on append/prepend of an existing item, so they are
// only sent, as the created item's, when CreateOnMiss is set.
func concatRequest(mode string, opt ConcatOptions) storeRequest {
	r := storeRequest{mode: mode, cas: opt.CAS, nsMeans: StatusNotFound}
	if opt.CreateOnMiss {
		r.vivify = true
		r.ttl = opt.TTL
		r.flags = opt.Flags
	}
	return r
}

// request builds the ms for this store.
func (sr storeRequest) request(key string, value []byte) *meta.Request {
	req := meta.NewRequest(meta.CmdSet, key, value).AddReturnCAS()
	if sr.mode != "" {
		req.AddMode(sr.mode)
	}
	if sr.vivify {
		req.AddVivify(sr.ttl.Expiration())
	} else if exptime := sr.ttl.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}
	if sr.flags != 0 {
		req.AddClientFlags(sr.flags)
	}
	if sr.cas != 0 {
		req.AddCAS(uint64(sr.cas))
	}
	return req
}

// result interprets a well-formed ms response for this store.
func (sr storeRequest) result(resp *meta.Response) (StoreResult, error) {
	switch {
	case resp.IsSuccess():
		result := StoreResult{Status: StatusApplied}
		if v, ok := resp.CAS(); ok {
			result.CAS = CAS(v)
		}
		return result, nil
	case resp.IsCASMismatch(): // EX
		return StoreResult{Status: StatusCASMismatch}, nil
	case resp.Status == meta.StatusNF:
		return StoreResult{Status: StatusNotFound}, nil
	case resp.IsNotStored() && sr.nsMeans != 0: // NS — meaning depends on the mode
		return StoreResult{Status: sr.nsMeans}, nil
	default:
		return StoreResult{}, fmt.Errorf("unexpected store status: %s", resp.Status)
	}
}

// store builds and runs a meta set.
func (c *Commands) store(ctx context.Context, key string, value []byte, sr storeRequest) (StoreResult, error) {
	var result StoreResult
	err := c.execute(ctx, sr.request(key, value), func(resp *meta.Response) (err error) {
		result, err = sr.result(resp)
		return err
	})
	if err != nil {
		return StoreResult{}, err
	}
	return result, nil
}

// Touch updates the expiration of an existing key without transferring its
// value. It reports StatusApplied when the key existed, StatusNotFound when
// it did not. The TTL is the point of the call, so it is a plain argument,
// like the delta of Increment.
func (c *Commands) Touch(ctx context.Context, key string, ttl TTL) (Status, error) {
	req := meta.NewRequest(meta.CmdGet, key, nil).AddTTL(ttl.Expiration())

	var status Status
	err := c.execute(ctx, req, func(resp *meta.Response) error {
		if resp.IsMiss() {
			status = StatusNotFound
			return nil
		}
		if resp.Status != meta.StatusHD {
			return fmt.Errorf("touch failed with status: %s", resp.Status)
		}
		status = StatusApplied
		return nil
	})
	if err != nil {
		return 0, err
	}
	return status, nil
}

// Delete removes the item at key. A DeleteOptions with a non-zero CAS deletes
// only if the CAS matches; a mismatch reports StatusCASMismatch and a missing key
// reports StatusNotFound.
func (c *Commands) Delete(ctx context.Context, key string, opts ...DeleteOptions) (Status, error) {
	opt := firstOpt(opts)

	req := meta.NewRequest(meta.CmdDelete, key, nil)
	if opt.CAS != 0 {
		req.AddCAS(uint64(opt.CAS))
	}

	var status Status
	err := c.execute(ctx, req, func(resp *meta.Response) (err error) {
		status, err = deleteStatus(resp)
		return err
	})
	if err != nil {
		return 0, err
	}
	return status, nil
}

// deleteStatus interprets a well-formed md response.
func deleteStatus(resp *meta.Response) (Status, error) {
	switch {
	case resp.Status == meta.StatusHD:
		return StatusApplied, nil
	case resp.IsCASMismatch():
		return StatusCASMismatch, nil
	case resp.Status == meta.StatusNF:
		return StatusNotFound, nil
	default:
		return 0, fmt.Errorf("delete failed with status: %s", resp.Status)
	}
}

// Increment increments a counter key by delta. By default a missing key
// reports StatusNotFound; set CounterOptions.Create to create it on miss,
// seeded with CounterOptions.Initial. NoTTL means infinite TTL.
func (c *Commands) Increment(ctx context.Context, key string, delta uint64, opts ...CounterOptions) (Counter, error) {
	return c.arithmetic(ctx, key, delta, firstOpt(opts), false)
}

// Decrement decrements a counter key by delta, stopping at zero. By default a
// missing key reports StatusNotFound; set CounterOptions.Create to create it
// on miss. NoTTL means infinite TTL.
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
	if opt.Create {
		req.AddInitialValue(opt.Initial)
		req.AddVivify(opt.TTL.Expiration())
	}
	if opt.CAS != 0 {
		req.AddCAS(uint64(opt.CAS))
	}
	if exptime := opt.TTL.Expiration(); exptime != 0 {
		req.AddTTL(exptime)
	}

	counter := Counter{Key: key}
	err := c.execute(ctx, req, func(resp *meta.Response) error {
		switch {
		case resp.IsCASMismatch():
			counter.Status = StatusCASMismatch
			return nil
		case resp.IsMiss():
			counter.Status = StatusNotFound
			return nil
		case !resp.HasValue():
			return fmt.Errorf("%s response missing value", operation)
		}

		parsed, err := strconv.ParseUint(string(resp.Data), 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse %s result: %w", operation, err)
		}
		counter.Value = parsed
		counter.Status = StatusApplied
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
// Status StatusNotFound for missing items. The options apply to every key: a
// non-zero TTL touches each item read (get-and-touch).
//
// [Config.OperationTimeout] bounds each response read, not the whole batch, so
// a slow server can hold the operation for a multiple of it (see the Timeouts
// section in the package documentation). Pass a context with a deadline to cap
// the total.
func (c *Commands) MultiGet(ctx context.Context, keys []string, opts ...GetOptions) ([]Item, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	opt := firstOpt(opts)

	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		reqs[i] = getRequest(key, opt)
	}

	responses, err := c.executeBatch(ctx, reqs)
	if err != nil {
		return nil, err
	}

	items := make([]Item, len(keys))
	for i, resp := range responses {
		// Batch responses are caller-owned (the Executor contract), so the
		// value is kept without a clone.
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
func (c *Commands) MultiSet(ctx context.Context, items []SetItem) ([]StoreResult, error) {
	if len(items) == 0 {
		return nil, nil
	}

	reqs := make([]*meta.Request, len(items))
	srs := make([]storeRequest, len(items))
	for i, item := range items {
		srs[i] = storeRequest{ttl: item.Options.TTL, flags: item.Options.Flags, cas: item.Options.CAS}
		reqs[i] = srs[i].request(item.Key, item.Value)
	}

	responses, err := c.executeBatch(ctx, reqs)
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
// Status per key, in order: StatusApplied, or StatusNotFound for a key that
// did not exist.
func (c *Commands) MultiDelete(ctx context.Context, keys []string) ([]Status, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	reqs := make([]*meta.Request, len(keys))
	for i, key := range keys {
		reqs[i] = meta.NewRequest(meta.CmdDelete, key, nil)
	}

	responses, err := c.executeBatch(ctx, reqs)
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
func (c *Commands) executeBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	responses, err := c.executor.ExecuteBatch(ctx, reqs)
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
