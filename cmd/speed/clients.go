package main

import (
	"context"
	"log"
	"time"

	bradfitz "github.com/bradfitz/gomemcache/memcache"
	"github.com/pior/memcache"
)

// Client interface for both clients
type Client interface {
	Get(ctx context.Context, key string) (memcache.Item, error)
	Set(ctx context.Context, item memcache.Item) error
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
	MultiGet(ctx context.Context, keys []string) ([]memcache.Item, error)
	MultiSet(ctx context.Context, items []memcache.Item) error
}

func createClient(config Config) (Client, func()) {
	if config.bradfitz {
		bradfitzCli := bradfitz.New(config.addr)
		bradfitzCli.MaxIdleConns = config.concurrency * 2
		return &bradfitzClient{bradfitzCli}, func() {} // bradfitz client has no Close method
	}

	cfg := memcache.Config{
		MaxSize:             int32(config.concurrency * 2),
		MaxConnLifetime:     5 * time.Minute,
		MaxConnIdleTime:     1 * time.Minute,
		HealthCheckInterval: 0, // Disable for speed test
	}

	if config.pool == "puddle" {
		cfg.NewPool = memcache.NewPuddlePool
	}
	// If config.pool == "channel" or empty, Pool stays nil and NewClient uses default

	piorCli, err := memcache.NewClient(memcache.NewStaticServers(config.addr), cfg)
	if err != nil {
		log.Fatalf("Failed to create pior client: %v\n", err)
	}
	return &piorClient{piorCli}, piorCli.Close
}

// piorClient wraps the pior/memcache client to implement Querier
type piorClient struct {
	*memcache.Client
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

func (c *bradfitzClient) MultiGet(ctx context.Context, keys []string) ([]memcache.Item, error) {
	// bradfitz client has GetMulti but returns map, not ordered slice
	// Fall back to individual Gets to maintain interface consistency
	items := make([]memcache.Item, len(keys))
	for i, key := range keys {
		item, err := c.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		items[i] = item
	}
	return items, nil
}

func (c *bradfitzClient) MultiSet(ctx context.Context, items []memcache.Item) error {
	// bradfitz client doesn't have batch set - fall back to individual Sets
	for _, item := range items {
		if err := c.Set(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
