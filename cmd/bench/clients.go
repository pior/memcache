package main

import (
	"context"
	"fmt"
	"time"

	bradfitz "github.com/bradfitz/gomemcache/memcache"
	"github.com/pior/memcache"
	"github.com/pior/memcache/meta"
)

// Client interface for both clients
type Client interface {
	Get(ctx context.Context, key string, opts ...memcache.GetOptions) (memcache.Item, error)
	Set(ctx context.Context, key string, value []byte, opts ...memcache.StoreOptions) (memcache.StoreResult, error)
	Delete(ctx context.Context, key string, opts ...memcache.DeleteOptions) (memcache.Status, error)
	Increment(ctx context.Context, key string, delta uint64, opts ...memcache.CounterOptions) (memcache.Counter, error)
	Decrement(ctx context.Context, key string, delta uint64, opts ...memcache.CounterOptions) (memcache.Counter, error)
	Close()
}

func createClient(config Config) (Client, *memcache.BatchCommands) {
	if config.bradfitz {
		bradfitzCli := bradfitz.New(config.addr)
		bradfitzCli.MaxIdleConns = config.concurrency * 2
		bradfitzWrapper := &bradfitzClient{bradfitzCli}
		batchCmd := memcache.NewBatchCommands(bradfitzWrapper)
		return bradfitzWrapper, batchCmd
	}

	cfg := memcache.Config{
		MaxSize:             int32(config.concurrency * 2),
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 0, // Disable for the benchmark
	}

	piorCli := memcache.NewClient(memcache.StaticServers(config.addr), cfg)
	batchCmd := memcache.NewBatchCommands(piorCli)
	return piorCli, batchCmd
}

// bradfitzClient wraps the bradfitz/gomemcache client to implement Querier
type bradfitzClient struct {
	*bradfitz.Client
}

var _ memcache.BatchExecutor = (*bradfitzClient)(nil)

func (c *bradfitzClient) Get(ctx context.Context, key string, _ ...memcache.GetOptions) (memcache.Item, error) {
	item, err := c.Client.Get(key)
	if err == bradfitz.ErrCacheMiss {
		return memcache.Item{Key: key, Found: false}, nil
	}
	if err != nil {
		return memcache.Item{}, err
	}
	return memcache.Item{
		Key:   item.Key,
		Value: item.Value,
		Found: true,
	}, nil
}

func (c *bradfitzClient) Set(ctx context.Context, key string, value []byte, opts ...memcache.StoreOptions) (memcache.StoreResult, error) {
	var opt memcache.StoreOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	// bradfitz's Expiration uses the same encoding as TTL.Expiration:
	// 0 for no expiration, relative seconds, or an absolute unix timestamp.
	err := c.Client.Set(&bradfitz.Item{
		Key:        key,
		Value:      value,
		Expiration: int32(opt.TTL.Expiration()),
	})
	if err != nil {
		return memcache.StoreResult{}, err
	}
	return memcache.StoreResult{Status: memcache.Applied}, nil
}

func (c *bradfitzClient) Delete(ctx context.Context, key string, _ ...memcache.DeleteOptions) (memcache.Status, error) {
	err := c.Client.Delete(key)
	if err == bradfitz.ErrCacheMiss {
		return memcache.NotFound, nil
	}
	if err != nil {
		return memcache.Applied, err
	}
	return memcache.Applied, nil
}

func (c *bradfitzClient) Increment(ctx context.Context, key string, delta uint64, _ ...memcache.CounterOptions) (memcache.Counter, error) {
	return c.arithmetic(key, func() (uint64, error) { return c.Client.Increment(key, delta) })
}

func (c *bradfitzClient) Decrement(ctx context.Context, key string, delta uint64, _ ...memcache.CounterOptions) (memcache.Counter, error) {
	return c.arithmetic(key, func() (uint64, error) { return c.Client.Decrement(key, delta) })
}

func (c *bradfitzClient) arithmetic(key string, operation func() (uint64, error)) (memcache.Counter, error) {
	value, err := operation()
	if err == bradfitz.ErrCacheMiss {
		return memcache.Counter{Key: key, Status: memcache.NotFound}, nil
	}
	if err != nil {
		return memcache.Counter{}, err
	}
	return memcache.Counter{Key: key, Value: value, Status: memcache.Applied}, nil
}

func (c *bradfitzClient) Execute(ctx context.Context, req *meta.Request, consume func(*meta.Response) error) error {
	// Not used directly, but needed for Executor interface
	panic("Execute not implemented for bradfitz client wrapper")
}

func (c *bradfitzClient) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	// bradfitz client doesn't support batching - fall back to individual operations
	responses := make([]*meta.Response, len(reqs))
	for i, req := range reqs {
		// Execute each request individually based on command type
		var err error
		switch req.Command {
		case meta.CmdGet:
			item, getErr := c.Get(ctx, req.Key)
			err = getErr
			if err == nil {
				if item.Found {
					responses[i] = &meta.Response{
						Status: meta.StatusVA,
						Data:   item.Value,
					}
				} else {
					responses[i] = &meta.Response{
						Status: meta.StatusEN,
					}
				}
			}
		case meta.CmdSet:
			_, err = c.Set(ctx, req.Key, req.Data)
			if err == nil {
				responses[i] = &meta.Response{
					Status: meta.StatusHD,
				}
			}
		case meta.CmdDelete:
			_, err = c.Delete(ctx, req.Key)
			if err == nil {
				responses[i] = &meta.Response{
					Status: meta.StatusHD,
				}
			}
		default:
			err = fmt.Errorf("unsupported command: %s", req.Command)
		}

		if err != nil {
			return nil, err
		}
	}
	return responses, nil
}

func (c *bradfitzClient) Close() {
}
