package memcache

import (
	"context"
	"errors"
)

var (
	ErrCacheMiss    = errors.New("memcache: cache miss")
	ErrKeyTooLong   = errors.New("memcache: key too long")
	ErrEmptyKey     = errors.New("memcache: empty key")
	ErrServerError  = errors.New("memcache: server error")
	ErrClientClosed = errors.New("memcache: client closed")
)

// Client is a high-level memcache client that manages multiple servers
type Client struct {
	selector ServerSelector
	closed   bool
}

// ClientConfig contains configuration for the memcache client
type ClientConfig struct {
	Servers    []string
	PoolConfig *PoolConfig
	HashRing   *HashRingConfig
}

// HashRingConfig contains configuration for consistent hashing
type HashRingConfig struct {
	VirtualNodes int
}

// DefaultClientConfig returns a default client configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		Servers:    []string{"localhost:11211"},
		PoolConfig: DefaultPoolConfig(),
		HashRing: &HashRingConfig{
			VirtualNodes: 160,
		},
	}
}

// NewClient creates a new memcache client
func NewClient(config *ClientConfig) (*Client, error) {
	if config == nil {
		config = DefaultClientConfig()
	}

	if len(config.Servers) == 0 {
		return nil, errors.New("memcache: no servers specified")
	}

	// Create server selector
	selector, err := NewConsistentHashSelectorWithPools(config.Servers, config.PoolConfig, config.HashRing.VirtualNodes)
	if err != nil {
		return nil, err
	}

	return &Client{
		selector: selector,
	}, nil
}

// Get retrieves an item from the cache
func (c *Client) Get(ctx context.Context, key string) (*Item, error) {
	if c.closed {
		return nil, ErrClientClosed
	}

	if err := validateKey(key); err != nil {
		return nil, err
	}

	pool, err := c.selector.SelectServer(key)
	if err != nil {
		return nil, err
	}
	command := FormatGetCommand(key, []string{"v"}, GenerateOpaque())

	response, err := pool.Execute(ctx, command)
	if err != nil {
		return nil, err
	}

	if response.Status == "EN" {
		return nil, ErrCacheMiss
	}

	if response.Status != "HD" {
		return nil, ErrServerError
	}

	return &Item{
		Key:   key,
		Value: response.Value,
		Flags: response.Flags,
	}, nil
}

// Set stores an item in the cache
func (c *Client) Set(ctx context.Context, item *Item) error {
	if c.closed {
		return ErrClientClosed
	}

	if err := validateKey(item.Key); err != nil {
		return err
	}

	pool, err := c.selector.SelectServer(item.Key)
	if err != nil {
		return err
	}
	command := FormatSetCommand(item.Key, item.Value, item.Expiration, item.Flags, GenerateOpaque())

	response, err := pool.Execute(ctx, command)
	if err != nil {
		return err
	}

	if response.Status != "HD" {
		return ErrServerError
	}

	return nil
}

// Delete removes an item from the cache
func (c *Client) Delete(ctx context.Context, key string) error {
	if c.closed {
		return ErrClientClosed
	}

	if err := validateKey(key); err != nil {
		return err
	}

	pool, err := c.selector.SelectServer(key)
	if err != nil {
		return err
	}
	command := FormatDeleteCommand(key, GenerateOpaque())

	response, err := pool.Execute(ctx, command)
	if err != nil {
		return err
	}

	if response.Status == "EN" {
		return ErrCacheMiss
	}

	if response.Status != "HD" {
		return ErrServerError
	}

	return nil
}

// GetMulti retrieves multiple items from the cache
func (c *Client) GetMulti(ctx context.Context, keys []string) (map[string]*Item, error) {
	if c.closed {
		return nil, ErrClientClosed
	}

	if len(keys) == 0 {
		return make(map[string]*Item), nil
	}

	// Validate all keys first
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return nil, err
		}
	}

	// Group keys by server
	serverKeys := make(map[*Pool][]string)
	for _, key := range keys {
		pool, err := c.selector.SelectServer(key)
		if err != nil {
			return nil, err
		}
		serverKeys[pool] = append(serverKeys[pool], key)
	}

	// Execute requests per server
	results := make(map[string]*Item)
	for pool, poolKeys := range serverKeys {
		commands := make([][]byte, len(poolKeys))
		for i, key := range poolKeys {
			commands[i] = FormatGetCommand(key, []string{"v"}, GenerateOpaque())
		}

		responses, err := pool.ExecuteBatch(ctx, commands)
		if err != nil {
			return nil, err
		}

		// Process responses
		for i, response := range responses {
			key := poolKeys[i]
			if response.Status == "HD" {
				results[key] = &Item{
					Key:   key,
					Value: response.Value,
					Flags: response.Flags,
				}
			}
			// Ignore cache misses in GetMulti
		}
	}

	return results, nil
}

// SetMulti stores multiple items in the cache
func (c *Client) SetMulti(ctx context.Context, items []*Item) error {
	if c.closed {
		return ErrClientClosed
	}

	if len(items) == 0 {
		return nil
	}

	// Validate all keys first
	for _, item := range items {
		if err := validateKey(item.Key); err != nil {
			return err
		}
	}

	// Group items by server
	serverItems := make(map[*Pool][]*Item)
	for _, item := range items {
		pool, err := c.selector.SelectServer(item.Key)
		if err != nil {
			return err
		}
		serverItems[pool] = append(serverItems[pool], item)
	}

	// Execute requests per server
	for pool, poolItems := range serverItems {
		commands := make([][]byte, len(poolItems))
		for i, item := range poolItems {
			commands[i] = FormatSetCommand(item.Key, item.Value, item.Expiration, item.Flags, GenerateOpaque())
		}

		responses, err := pool.ExecuteBatch(ctx, commands)
		if err != nil {
			return err
		}

		// Check all responses succeeded
		for _, response := range responses {
			if response.Status != "HD" {
				return ErrServerError
			}
		}
	}

	return nil
}

// DeleteMulti removes multiple items from the cache
func (c *Client) DeleteMulti(ctx context.Context, keys []string) error {
	if c.closed {
		return ErrClientClosed
	}

	if len(keys) == 0 {
		return nil
	}

	// Validate all keys first
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return err
		}
	}

	// Group keys by server
	serverKeys := make(map[*Pool][]string)
	for _, key := range keys {
		pool, err := c.selector.SelectServer(key)
		if err != nil {
			return err
		}
		serverKeys[pool] = append(serverKeys[pool], key)
	}

	// Execute requests per server
	for pool, poolKeys := range serverKeys {
		commands := make([][]byte, len(poolKeys))
		for i, key := range poolKeys {
			commands[i] = FormatDeleteCommand(key, GenerateOpaque())
		}

		responses, err := pool.ExecuteBatch(ctx, commands)
		if err != nil {
			return err
		}

		// Check responses (ignore cache misses)
		for _, response := range responses {
			if response.Status != "HD" && response.Status != "EN" {
				return ErrServerError
			}
		}
	}

	return nil
}

// Ping checks connectivity to all servers
func (c *Client) Ping(ctx context.Context) error {
	if c.closed {
		return ErrClientClosed
	}

	return c.selector.Ping(ctx)
}

// Stats returns statistics from all servers
func (c *Client) Stats() []PoolStats {
	if c.closed {
		return nil
	}

	return c.selector.Stats()
}

// Close closes all connections to all servers
func (c *Client) Close() error {
	if c.closed {
		return nil
	}

	c.closed = true
	return c.selector.Close()
}

// validateKey validates a cache key
func validateKey(key string) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key) > 250 {
		return ErrKeyTooLong
	}
	if !isValidKey(key) {
		return ErrMalformedKey
	}
	return nil
}
