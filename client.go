package memcache

import (
	"context"
	"errors"
	"time"
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

	// Ensure we have pool config
	if config.PoolConfig == nil {
		config.PoolConfig = DefaultPoolConfig()
	} else {
		if config.PoolConfig.ConnTimeout == 0 {
			config.PoolConfig.ConnTimeout = DefaultPoolConfig().ConnTimeout
		}
		if config.PoolConfig.IdleTimeout == 0 {
			config.PoolConfig.IdleTimeout = DefaultPoolConfig().IdleTimeout
		}
	}

	// Ensure we have hash ring config
	if config.HashRing == nil {
		config.HashRing = &HashRingConfig{
			VirtualNodes: 160,
		}
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

// Do executes one or more memcache commands
func (c *Client) Do(ctx context.Context, commands ...*Command) error {
	if c.closed {
		return ErrClientClosed
	}

	if len(commands) == 0 {
		return nil
	}

	// Validate all commands first
	for _, cmd := range commands {
		if err := c.validateCommand(cmd); err != nil {
			return err
		}
	}

	// Group commands by server
	serverCommands := make(map[ConnectionPool][]*Command)
	for _, cmd := range commands {
		pool, err := c.selector.SelectServer(cmd.Key)
		if err != nil {
			return err
		}
		serverCommands[pool] = append(serverCommands[pool], cmd)
	}

	// Execute commands per server
	for pool, poolCommands := range serverCommands {
		// Execute using the pool's With method
		err := pool.With(func(conn *Connection) error {
			return conn.ExecuteBatch(ctx, poolCommands)
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// WaitAll waits for all command responses to be ready.
//
// This function blocks until all the provided commands have their responses available,
// or until the context is cancelled. It's useful when you've executed multiple commands
// using client.Do() and want to ensure all responses are ready before proceeding.
//
// Returns nil if all commands complete successfully, or the first error encountered
// (including context cancellation or timeout).
//
// Example usage:
//
//	commands := []*Command{
//		NewSetCommand("key1", []byte("value1"), time.Hour),
//		NewSetCommand("key2", []byte("value2"), time.Hour),
//	}
//
//	// Execute commands asynchronously
//	err := client.Do(ctx, commands...)
//	if err != nil {
//		return err
//	}
//
//	// Wait for all responses to be ready
//	err = WaitAll(ctx, commands...)
//	if err != nil {
//		return err
//	}
//
//	// Now all responses are guaranteed to be available
//	for _, cmd := range commands {
//		resp, _ := cmd.GetResponse(ctx)
//		// Process response...
//	}
func WaitAll(ctx context.Context, commands ...*Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Create a channel to collect errors from goroutines
	errChan := make(chan error, len(commands))

	// Launch a goroutine for each command to wait for its response
	for _, cmd := range commands {
		if cmd == nil {
			errChan <- errors.New("memcache: nil command")
			continue
		}

		go func(c *Command) {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
			case <-c.ready:
				errChan <- nil
			}
		}(cmd)
	}

	// Wait for all commands to complete or context to be cancelled
	for i := 0; i < len(commands); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errChan:
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Get retrieves a single value from the cache
func (c *Client) Get(ctx context.Context, key string) (*Response, error) {
	cmd := NewGetCommand(key)
	if err := c.Do(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.GetResponse(ctx)
}

// Set stores a value in the cache
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := NewSetCommand(key, value, ttl)
	return c.Do(ctx, cmd)
}

// Delete removes a value from the cache
func (c *Client) Delete(ctx context.Context, key string) error {
	cmd := NewDeleteCommand(key)
	return c.Do(ctx, cmd)
}

// Increment increments a numeric value in the cache
func (c *Client) Increment(ctx context.Context, key string, delta int64) (*Response, error) {
	cmd := NewIncrementCommand(key, delta)
	if err := c.Do(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.GetResponse(ctx)
}

// Decrement decrements a numeric value in the cache
func (c *Client) Decrement(ctx context.Context, key string, delta int64) (*Response, error) {
	cmd := NewDecrementCommand(key, delta)
	if err := c.Do(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.GetResponse(ctx)
}

// validateCommand validates a command before execution
func (c *Client) validateCommand(cmd *Command) error {
	if cmd == nil {
		return errors.New("memcache: command cannot be nil")
	}

	if err := validateKey(cmd.Key); err != nil {
		return err
	}

	switch cmd.Type {
	case CmdMetaGet, CmdMetaDelete:
		// These commands only need a valid key
	case CmdMetaSet:
		// Set commands need a value
		if cmd.Value == nil {
			return errors.New("memcache: set command requires a value")
		}
	case CmdMetaArithmetic:
		// Arithmetic commands need a key and delta flag
		if _, exists := cmd.GetFlag(FlagDelta); !exists {
			return errors.New("memcache: arithmetic command requires delta flag")
		}
	case CmdMetaDebug, CmdMetaNoOp:
		// Debug and no-op commands are valid as-is
	default:
		return errors.New("memcache: unsupported command type: " + cmd.Type)
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
