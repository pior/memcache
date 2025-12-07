package main

import (
	"context"
	"fmt"
	"log"
	"time"

	bradfitz "github.com/bradfitz/gomemcache/memcache"
	"github.com/pior/memcache"
	"github.com/pior/memcache/meta"
)

// Client interface for both clients
type Client interface {
	Get(ctx context.Context, key string) (memcache.Item, error)
	Set(ctx context.Context, item memcache.Item) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
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
		HealthCheckInterval: 0, // Disable for speed test
	}

	if config.pool == "channel" {
		cfg.NewPool = memcache.NewChannelPool
	}

	piorCli, err := memcache.NewClient(memcache.NewStaticServers(config.addr), cfg)
	if err != nil {
		log.Fatalf("Failed to create pior client: %v\n", err)
	}
	batchCmd := memcache.NewBatchCommands(piorCli)
	return piorCli, batchCmd
}

// bradfitzClient wraps the bradfitz/gomemcache client to implement Querier
type bradfitzClient struct {
	*bradfitz.Client
}

func (c *bradfitzClient) Get(ctx context.Context, key string) (memcache.Item, error) {
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

func (c *bradfitzClient) Set(ctx context.Context, item memcache.Item) error {
	ttl := int32(0)
	if item.TTL > 0 {
		ttl = int32(item.TTL.Seconds())
	}
	return c.Client.Set(&bradfitz.Item{
		Key:        item.Key,
		Value:      item.Value,
		Expiration: ttl,
	})
}

func (c *bradfitzClient) Delete(ctx context.Context, key string) error {
	err := c.Client.Delete(key)
	if err == bradfitz.ErrCacheMiss {
		return nil // Delete is successful even if key doesn't exist
	}
	return err
}

func (c *bradfitzClient) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	var value uint64
	var err error

	if delta >= 0 {
		value, err = c.Client.Increment(key, uint64(delta))
	} else {
		value, err = c.Decrement(key, uint64(-delta))
	}

	if err != nil {
		return 0, err
	}
	return int64(value), nil
}

func (c *bradfitzClient) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
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
			err = c.Set(ctx, memcache.Item{
				Key:   req.Key,
				Value: req.Data,
				TTL:   0, // Extract from flags if needed
			})
			if err == nil {
				responses[i] = &meta.Response{
					Status: meta.StatusHD,
				}
			}
		case meta.CmdDelete:
			err = c.Delete(ctx, req.Key)
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
